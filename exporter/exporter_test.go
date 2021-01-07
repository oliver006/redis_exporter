package exporter

/*
  to run the tests with redis running on anything but localhost:6379 use
  $ go test   --redis.addr=<host>:<port>

  for html coverage report run
  $ go test -coverprofile=coverage.out  && go tool cover -html=coverage.out
*/

import (
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
)

const (
	TestValue   = 1234.56
	TimeToSleep = 200
)

var (
	keys         = []string{}
	keysExpiring = []string{}
	listKeys     = []string{}
	ts           = int32(time.Now().Unix())

	dbNumStr     = "11"
	altDBNumStr  = "12"
	dbNumStrFull = fmt.Sprintf("db%s", dbNumStr)
)

const (
	TestSetName    = "test-set"
	TestStreamName = "test-stream"
)

func getTestExporter() *Exporter {
	return getTestExporterWithOptions(Options{Namespace: "test", Registry: prometheus.NewRegistry()})
}

func getTestExporterWithOptions(opt Options) *Exporter {
	addr := os.Getenv("TEST_REDIS_URI")
	if addr == "" {
		panic("missing env var TEST_REDIS_URI")
	}
	e, _ := NewRedisExporter(addr, opt)
	return e
}

func setupLatency(t *testing.T, addr string) error {

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("CONFIG", "SET", "LATENCY-MONITOR-THRESHOLD", 100)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	// Have to pass in the sleep time in seconds so we have to divide
	// the number of milliseconds by 1000 to get number of seconds
	_, err = c.Do("DEBUG", "SLEEP", TimeToSleep/1000.0)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func resetLatency(t *testing.T, addr string) error {

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("LATENCY", "RESET")
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func setupSlowLog(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("CONFIG", "SET", "SLOWLOG-LOG-SLOWER-THAN", 10000)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	// Have to pass in the sleep time in seconds so we have to divide
	// the number of milliseconds by 1000 to get number of seconds
	_, err = c.Do("DEBUG", "SLEEP", TimeToSleep/1000.0)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func resetSlowLog(t *testing.T, addr string) error {

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("SLOWLOG", "RESET")
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func downloadURL(t *testing.T, u string) string {
	_, res := downloadURLWithStatusCode(t, u)
	return res
}

func downloadURLWithStatusCode(t *testing.T, u string) (int, string) {
	log.Debugf("downloadURL() %s", u)

	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode, string(body)
}

func TestLatencySpike(t *testing.T) {
	e := getTestExporter()

	setupLatency(t, os.Getenv("TEST_REDIS_URI"))
	defer resetLatency(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		if strings.Contains(m.Desc().String(), "latency_spike_duration_seconds") {
			got := &dto.Metric{}
			m.Write(got)

			// The metric value is in seconds, but our sleep interval is specified
			// in milliseconds, so we need to convert
			val := got.GetGauge().GetValue() * 1000
			// Because we're dealing with latency, there might be a slight delay
			// even after sleeping for a specific amount of time so checking
			// to see if we're between +-5 of our expected value
			if math.Abs(float64(TimeToSleep)-val) > 5 {
				t.Errorf("values not matching, %f != %f", float64(TimeToSleep), val)
			}
		}
	}

	resetLatency(t, os.Getenv("TEST_REDIS_URI"))

	chM = make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "latency_spike_duration_seconds") {
				t.Errorf("latency threshold was not reset")
			}
		}
	}
}

func TestTile38(t *testing.T) {
	if os.Getenv("TEST_TILE38_URI") == "" {
		t.Skipf("TEST_TILE38_URI not set - skipping")
	}

	for _, isTile38 := range []bool{true, false} {
		e, _ := NewRedisExporter(os.Getenv("TEST_TILE38_URI"), Options{Namespace: "test", IsTile38: isTile38})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		found := false
		want := "tile38_threads_total"
		for m := range chM {
			if strings.Contains(m.Desc().String(), want) {
				found = true
			}
		}

		if isTile38 && !found {
			t.Errorf("%s was *not* found in tile38 metrics but expected", want)
		} else if !isTile38 && found {
			t.Errorf("%s was *found* in tile38 metrics but *not* expected", want)
		}
	}
}

func TestExportClientList(t *testing.T) {
	for _, isExportClientList := range []bool{true, false} {
		e := getTestExporterWithOptions(Options{
			Namespace: "test", Registry: prometheus.NewRegistry(),
			ExportClientList: isExportClientList,
		})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		found := false
		for m := range chM {
			if strings.Contains(m.Desc().String(), "connected_clients_details") {
				found = true
			}
		}

		if isExportClientList && !found {
			t.Errorf("connected_clients_details was *not* found in isExportClientList metrics but expected")
		} else if !isExportClientList && found {
			t.Errorf("connected_clients_details was *found* in isExportClientList metrics but *not* expected")
		}
	}
}

func TestExportClientListInclPort(t *testing.T) {
	for _, inclPort := range []bool{true, false} {
		e := getTestExporterWithOptions(Options{
			Namespace: "test", Registry: prometheus.NewRegistry(),
			ExportClientList:      true,
			ExportClientsInclPort: inclPort,
		})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		found := false
		for m := range chM {
			desc := m.Desc().String()
			if strings.Contains(desc, "connected_clients_details") {
				if strings.Contains(desc, "port") {
					found = true
				}
			}
		}

		if inclPort && !found {
			t.Errorf(`connected_clients_details did *not* include "port" in isExportClientList metrics but was expected`)
		} else if !inclPort && found {
			t.Errorf(`connected_clients_details did *include* "port" in isExportClientList metrics but was *not* expected`)
		}
	}
}

func TestSlowLog(t *testing.T) {
	e := getTestExporter()

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	oldSlowLogID := float64(0)

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "slowlog_last_id") {
				got := &dto.Metric{}
				m.Write(got)

				oldSlowLogID = got.GetGauge().GetValue()
			}
		}
	}

	setupSlowLog(t, os.Getenv("TEST_REDIS_URI"))
	defer resetSlowLog(t, os.Getenv("TEST_REDIS_URI"))

	chM = make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "slowlog_last_id") {
				got := &dto.Metric{}
				m.Write(got)

				val := got.GetGauge().GetValue()

				if oldSlowLogID > val {
					t.Errorf("no new slowlogs found")
				}
			}
			if strings.Contains(m.Desc().String(), "slowlog_length") {
				got := &dto.Metric{}
				m.Write(got)

				val := got.GetGauge().GetValue()
				if val == 0 {
					t.Errorf("slowlog length is zero")
				}
			}
		}
	}

	resetSlowLog(t, os.Getenv("TEST_REDIS_URI"))

	chM = make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "slowlog_length") {
				got := &dto.Metric{}
				m.Write(got)

				val := got.GetGauge().GetValue()
				if val != 0 {
					t.Errorf("Slowlog was not reset")
				}
			}
		}
	}
}

func setupDBKeys(t *testing.T, uri string) error {
	c, err := redis.DialURL(uri)
	if err != nil {
		t.Errorf("couldn't setup redis for uri %s, err: %s ", uri, err)
		return err
	}
	defer c.Close()

	if _, err := c.Do("SELECT", dbNumStr); err != nil {
		log.Printf("setupDBKeys() - couldn't setup redis, err: %s ", err)
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
	}

	for _, key := range keys {
		_, err = c.Do("SET", key, TestValue)
		if err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	// setting to expire in 300 seconds, should be plenty for a test run
	for _, key := range keysExpiring {
		_, err = c.Do("SETEX", key, "300", TestValue)
		if err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	for _, key := range listKeys {
		for _, val := range keys {
			_, err = c.Do("LPUSH", key, val)
			if err != nil {
				t.Errorf("couldn't setup redis, err: %s ", err)
				return err
			}
		}
	}

	c.Do("SADD", TestSetName, "test-val-1")
	c.Do("SADD", TestSetName, "test-val-2")

	// Create test streams
	c.Do("XGROUP", "CREATE", TestStreamName, "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", TestStreamName, "test_group_2", "$", "MKSTREAM")
	c.Do("XADD", TestStreamName, "*", "field_1", "str_1")
	c.Do("XADD", TestStreamName, "*", "field_2", "str_2")
	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", TestStreamName, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", TestStreamName, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_2", "test_consumer_1", "COUNT", "1", "STREAMS", TestStreamName, "0")

	time.Sleep(time.Millisecond * 50)

	return nil
}

func deleteKeysFromDB(t *testing.T, addr string) error {

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	if _, err := c.Do("SELECT", dbNumStr); err != nil {
		log.Printf("deleteKeysFromDB() - couldn't setup redis, err: %s ", err)
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
	}

	for _, key := range keys {
		c.Do("DEL", key)
	}

	for _, key := range keysExpiring {
		c.Do("DEL", key)
	}

	for _, key := range listKeys {
		c.Do("DEL", key)
	}

	c.Do("DEL", TestSetName)
	c.Do("DEL", TestStreamName)
	return nil
}

func TestHostVariations(t *testing.T) {
	host := strings.ReplaceAll(os.Getenv("TEST_REDIS_URI"), "redis://", "")

	for _, prefix := range []string{"", "redis://", "tcp://", ""} {
		e, _ := NewRedisExporter(prefix+host, Options{SkipTLSVerification: true})
		c, err := e.connectToRedis()
		if err != nil {
			t.Errorf("connectToRedis() err: %s", err)
			continue
		}

		if _, err := c.Do("PING", ""); err != nil {
			t.Errorf("PING err: %s", err)
		}

		c.Close()
	}
}

func TestKeyspaceStringParser(t *testing.T) {
	tsts := []struct {
		db                        string
		stats                     string
		keysTotal, keysEx, avgTTL float64
		ok                        bool
	}{
		{db: "xxx", stats: "", ok: false},
		{db: "xxx", stats: "keys=1,expires=0,avg_ttl=0", ok: false},
		{db: "db0", stats: "xxx", ok: false},
		{db: "db1", stats: "keys=abcd,expires=0,avg_ttl=0", ok: false},
		{db: "db2", stats: "keys=1234=1234,expires=0,avg_ttl=0", ok: false},

		{db: "db3", stats: "keys=abcde,expires=0", ok: false},
		{db: "db3", stats: "keys=213,expires=xxx", ok: false},
		{db: "db3", stats: "keys=123,expires=0,avg_ttl=zzz", ok: false},

		{db: "db0", stats: "keys=1,expires=0,avg_ttl=0", keysTotal: 1, keysEx: 0, avgTTL: 0, ok: true},
	}

	for _, tst := range tsts {
		if kt, kx, ttl, ok := parseDBKeyspaceString(tst.db, tst.stats); true {

			if ok != tst.ok {
				t.Errorf("failed for: db:%s stats:%s", tst.db, tst.stats)
				continue
			}

			if ok && (kt != tst.keysTotal || kx != tst.keysEx || ttl != tst.avgTTL) {
				t.Errorf("values not matching, db:%s stats:%s   %f %f %f", tst.db, tst.stats, kt, kx, ttl)
			}
		}
	}
}

type slaveData struct {
	k, v            string
	ip, state, port string
	offset          float64
	lag             float64
	ok              bool
}

func TestParseConnectedSlaveString(t *testing.T) {
	tsts := []slaveData{
		{k: "slave0", v: "ip=10.254.11.1,port=6379,state=online,offset=1751844676,lag=0", offset: 1751844676, ip: "10.254.11.1", port: "6379", state: "online", ok: true, lag: 0},
		{k: "slave0", v: "ip=2a00:1450:400e:808::200e,port=6379,state=online,offset=1751844676,lag=0", offset: 1751844676, ip: "2a00:1450:400e:808::200e", port: "6379", state: "online", ok: true, lag: 0},
		{k: "slave1", v: "offset=1,lag=0", offset: 1, ok: true},
		{k: "slave1", v: "offset=1", offset: 1, ok: true, lag: -1},
		{k: "slave2", v: "ip=1.2.3.4,state=online,offset=123,lag=42", offset: 123, ip: "1.2.3.4", state: "online", ok: true, lag: 42},

		{k: "slave", v: "offset=1751844676,lag=0", ok: false},
		{k: "slaveA", v: "offset=1751844676,lag=0", ok: false},
		{k: "slave0", v: "offset=abc,lag=0", ok: false},
		{k: "slave0", v: "offset=0,lag=abc", ok: false},
	}

	for _, tst := range tsts {
		name := fmt.Sprintf("%s---%s", tst.k, tst.v)
		t.Run(name, func(t *testing.T) {
			if offset, ip, port, state, lag, ok := parseConnectedSlaveString(tst.k, tst.v); true {
				if ok != tst.ok {
					t.Errorf("failed for: db:%s stats:%s", tst.k, tst.v)
					return
				}
				if offset != tst.offset || ip != tst.ip || port != tst.port || state != tst.state || lag != tst.lag {
					t.Errorf("values not matching, string:%s %f %s %s %s %f", tst.v, offset, ip, port, state, lag)
				}
			}
		})
	}
}

func TestKeyValuesAndSizes(t *testing.T) {
	e, _ := NewRedisExporter(
		os.Getenv("TEST_REDIS_URI"),
		Options{Namespace: "test", CheckSingleKeys: dbNumStrFull + "=" + url.QueryEscape(keys[0])},
	)

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := map[string]bool{"test_key_size": false, "test_key_value": false}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}

	}
}

type keyFixture struct {
	command string
	key     string
	args    []interface{}
}

func newKeyFixture(command string, key string, args ...interface{}) keyFixture {
	return keyFixture{command, key, args}
}

func createKeyFixtures(t *testing.T, c redis.Conn, fixtures []keyFixture) {
	for _, f := range fixtures {
		args := append([]interface{}{f.key}, f.args...)
		if _, err := c.Do(f.command, args...); err != nil {
			t.Errorf("Error creating fixture: %#v, %#v", f, err)
		}
	}
}

func deleteKeyFixtures(t *testing.T, c redis.Conn, fixtures []keyFixture) {
	for _, f := range fixtures {
		if _, err := c.Do("DEL", f.key); err != nil {
			t.Errorf("Error deleting fixture: %#v, %#v", f, err)
		}
	}
}

func TestParseKeyArg(t *testing.T) {
	if parsed, err := parseKeyArg(""); len(parsed) != 0 || err != nil {
		t.Errorf("Parsing an empty string into a keys arg should yield an empty slice")
		return
	}

	if parsed, err := parseKeyArg("my-key"); err != nil || len(parsed) != 1 || parsed[0].db != "0" || parsed[0].key != "my-key" {
		t.Errorf("Expected DB: 0 and key: my-key, got: %#v", parsed[0])
		return
	}

	if _, err := parseKeyArg("wrong=wrong=wrong"); err == nil {
		t.Errorf("Expected an error")
		return
	}
}

func TestScanForKeys(t *testing.T) {
	numKeys := 1000
	fixtures := []keyFixture{}

	// Make 1000 keys that match
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("get_keys_test_shouldmatch_%v", i)
		fixtures = append(fixtures, newKeyFixture("SET", key, "Woohoo!"))
	}

	// And 1000 that don't
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("get_keys_test_shouldnotmatch_%v", i)
		fixtures = append(fixtures, newKeyFixture("SET", key, "Rats!"))
	}

	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()

	createKeyFixtures(t, c, fixtures)

	matches, err := scanForKeys(c, "get_keys_test_*shouldmatch*")
	if err != nil {
		t.Errorf("Error getting keys matching a pattern: %#v", err)
	}

	numMatches := len(matches)
	if numMatches != numKeys {
		t.Errorf("Expected %#v matches, got %#v.", numKeys, numMatches)
	}

	for _, match := range matches {
		if !strings.HasPrefix(match, "get_keys_test_shouldmatch") {
			t.Errorf("Expected match to have prefix: get_keys_test_shouldmatch")
		}
	}
}

func TestGetKeysFromPatterns(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_URI")
	dbMain := dbNumStr
	dbAlt := altDBNumStr

	dbMainFixtures := []keyFixture{
		newKeyFixture("SET", "dbMainNoPattern1", "woohoo!"),
		newKeyFixture("SET", "dbMainSomePattern1", "woohoo!"),
		newKeyFixture("SET", "dbMainSomePattern2", "woohoo!"),
	}

	dbAltFixtures := []keyFixture{
		newKeyFixture("SET", "dbAltNoPattern1", "woohoo!"),
		newKeyFixture("SET", "dbAltSomePattern1", "woohoo!"),
		newKeyFixture("SET", "dbAltSomePattern2", "woohoo!"),
	}

	keys := []dbKeyPair{
		{db: dbMain, key: "dbMainNoPattern1"},
		{db: dbMain, key: "*SomePattern*"},
		{db: dbAlt, key: "dbAltNoPattern1"},
		{db: dbAlt, key: "*SomePattern*"},
	}

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	defer func() {
		_, err = c.Do("SELECT", dbMain)
		if err != nil {
			t.Errorf("Couldn't select database %#v", dbMain)
		}
		deleteKeyFixtures(t, c, dbMainFixtures)

		_, err = c.Do("SELECT", dbAlt)
		if err != nil {
			t.Errorf("Couldn't select database %#v", dbAlt)
		}
		deleteKeyFixtures(t, c, dbAltFixtures)
		c.Close()
	}()

	_, err = c.Do("SELECT", dbMain)
	if err != nil {
		t.Errorf("Couldn't select database %#v", dbMain)
	}
	createKeyFixtures(t, c, dbMainFixtures)

	_, err = c.Do("SELECT", dbAlt)
	if err != nil {
		t.Errorf("Couldn't select database %#v", dbAlt)
	}
	createKeyFixtures(t, c, dbAltFixtures)

	expandedKeys, err := getKeysFromPatterns(c, keys)
	if err != nil {
		t.Errorf("Error getting keys from patterns: %#v", err)
	}

	expectedKeys := []dbKeyPair{
		{db: dbMain, key: "dbMainNoPattern1"},
		{db: dbMain, key: "dbMainSomePattern1"},
		{db: dbMain, key: "dbMainSomePattern2"},
		{db: dbAlt, key: "dbAltNoPattern1"},
		{db: dbAlt, key: "dbAltSomePattern1"},
		{db: dbAlt, key: "dbAltSomePattern2"},
	}

	sort.Slice(expectedKeys, func(i, j int) bool {
		return (expectedKeys[i].db + expectedKeys[i].key) < (expectedKeys[j].db + expectedKeys[j].key)
	})

	sort.Slice(expandedKeys, func(i, j int) bool {
		return (expandedKeys[i].db + expandedKeys[i].key) < (expandedKeys[j].db + expandedKeys[j].key)
	})

	if !reflect.DeepEqual(expectedKeys, expandedKeys) {
		t.Errorf("When expanding keys:\nexpected: %#v\nactual:   %#v", expectedKeys, expandedKeys)
	}
}

func TestGetKeyInfo(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"SET", "key_info_test_string", []interface{}{"Woohoo!"}},
		{"HSET", "key_info_test_hash", []interface{}{"hashkey1", "hashval1"}},
		{"PFADD", "key_info_test_hll", []interface{}{"hllval1", "hllval2"}},
		{"LPUSH", "key_info_test_list", []interface{}{"listval1", "listval2", "listval3"}},
		{"SADD", "key_info_test_set", []interface{}{"setval1", "setval2", "setval3", "setval4"}},
		{"ZADD", "key_info_test_zset", []interface{}{
			"1", "zsetval1",
			"2", "zsetval2",
			"3", "zsetval3",
			"4", "zsetval4",
			"5", "zsetval5",
		}},
		{"XADD", "key_info_test_stream", []interface{}{"*", "field1", "str1"}},
	}

	createKeyFixtures(t, c, fixtures)

	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()

	expectedSizes := map[string]float64{
		"key_info_test_string": 7,
		"key_info_test_hash":   1,
		"key_info_test_hll":    2,
		"key_info_test_list":   3,
		"key_info_test_set":    4,
		"key_info_test_zset":   5,
		"key_info_test_stream": 1,
	}

	// Test all known types
	for _, f := range fixtures {
		info, err := getKeyInfo(c, f.key)
		if err != nil {
			t.Errorf("Error getting key info for %#v.", f.key)
		}

		expected := expectedSizes[f.key]
		if info.size != expected {
			t.Logf("%#v", info)
			t.Errorf("Wrong size for key: %#v. Expected: %#v; Actual: %#v", f.key, expected, info.size)
		}
	}

	// Test absent key returns the correct error
	_, err = getKeyInfo(c, "absent_key")
	if err != errNotFound {
		t.Error("Expected `errNotFound` for absent key.  Got a different error.")
	}
}

func TestKeySizeList(t *testing.T) {
	s := dbNumStrFull + "=" + listKeys[0]
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: s})

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	found := false
	for m := range chM {
		if strings.Contains(m.Desc().String(), "test_key_size") {
			found = true
		}
	}

	if !found {
		t.Errorf("didn't find the key")
	}
}

func TestLuaScript(t *testing.T) {
	e := getTestExporter()

	for _, tst := range []struct {
		Script        string
		ExpectedKeys  int
		ExpectedError bool
	}{
		{
			Script:       `return {"a", "11", "b", "12", "c", "13"}`,
			ExpectedKeys: 3,
		},
		{
			Script:       `return {"key1", "6389"}`,
			ExpectedKeys: 1,
		},
		{
			Script:       `return {} `,
			ExpectedKeys: 0,
		},
		{
			Script:        `return {"key1"   BROKEN `,
			ExpectedKeys:  0,
			ExpectedError: true,
		},
	} {

		e.options.LuaScript = []byte(tst.Script)
		nKeys := tst.ExpectedKeys

		setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
		defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()
		scrapeErrorFound := false

		for m := range chM {
			if strings.Contains(m.Desc().String(), "test_script_value") {
				nKeys--
			}

			if strings.Contains(m.Desc().String(), "exporter_last_scrape_error") {
				g := &dto.Metric{}
				m.Write(g)
				if g.GetGauge() != nil && *g.GetGauge().Value > 0 {
					scrapeErrorFound = true
				}
			}
		}
		if nKeys != 0 {
			t.Error("didn't find expected script keys")
		}

		if tst.ExpectedError {
			if !scrapeErrorFound {
				t.Error("didn't find expected scrape errors")
			}
		}
	}
}

func TestKeyValueInvalidDB(t *testing.T) {
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: "999=" + url.QueryEscape(keys[0])})

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	dontWant := map[string]bool{"test_key_size": false}
	for m := range chM {
		switch m.(type) {
		case prometheus.Gauge:
			for k := range dontWant {
				if strings.Contains(m.Desc().String(), k) {
					log.Println(m.Desc().String())
					dontWant[k] = true
				}
			}
		default:
			log.Debugf("default: m: %#v", m)
		}
	}
	for k, found := range dontWant {
		if found {
			t.Errorf("we found %s but it shouldn't be there", k)
		}

	}
}

func TestCommandStats(t *testing.T) {
	e := getTestExporter()

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := map[string]bool{"test_commands_duration_seconds_total": false, "test_commands_total": false}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}

	}
}

func TestIncludeSystemMemoryMetric(t *testing.T) {
	for _, inc := range []bool{false, true} {
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", InclSystemMetrics: inc})
		r.Register(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && !strings.Contains(body, "total_system_memory_bytes") {
			t.Errorf("want metrics to include total_system_memory_bytes, have:\n%s", body)
		} else if !inc && strings.Contains(body, "total_system_memory_bytes") {
			t.Errorf("did NOT want metrics to include total_system_memory_bytes, have:\n%s", body)
		}

		ts.Close()
	}
}

func TestHTTPScrapeMetricsEndpoints(t *testing.T) {
	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))
	setupDBKeys(t, os.Getenv("TEST_PWD_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_PWD_REDIS_URI"))

	csk := dbNumStrFull + "=" + url.QueryEscape(keys[0]) // check-single-keys
	css := dbNumStrFull + "=" + TestStreamName           // check-single-streams
	cntk := dbNumStrFull + "=" + keys[0] + "*"           // count-keys

	testRedisIPAddress := ""
	testRedisHostname := ""
	if u, err := url.Parse(os.Getenv("TEST_REDIS_URI")); err == nil {
		testRedisHostname = u.Hostname()
		ips, err := net.LookupIP(testRedisHostname)
		if err != nil {
			t.Fatalf("Could not get IP address: %s", err)
		}
		if len(ips) == 0 {
			t.Fatal("No IP addresses found")
		}
		testRedisIPAddress = ips[0].String()
		t.Logf("testRedisIPAddress: %s", testRedisIPAddress)
		t.Logf("testRedisHostname: %s", testRedisHostname)
	}

	for _, tst := range []struct {
		name           string
		addr           string
		ck             string
		csk            string
		cs             string
		css            string
		cntk           string
		pwd            string
		scrape         bool
		target         string
		wantStatusCode int
	}{
		{name: "ip-addr", addr: testRedisIPAddress, csk: csk, css: css, cntk: cntk},
		{name: "hostname", addr: testRedisHostname, csk: csk, css: css, cntk: cntk},

		{name: "check-keys", addr: os.Getenv("TEST_REDIS_URI"), ck: csk, cs: css, cntk: cntk},
		{name: "check-single-keys", addr: os.Getenv("TEST_REDIS_URI"), csk: csk, css: css, cntk: cntk},

		{name: "addr-no-prefix", addr: strings.TrimPrefix(os.Getenv("TEST_REDIS_URI"), "redis://"), csk: csk, css: css, cntk: cntk},

		{name: "scrape-target-no-prefix", pwd: "", scrape: true, target: strings.TrimPrefix(os.Getenv("TEST_REDIS_URI"), "redis://"), ck: csk, cs: css, cntk: cntk},

		{name: "scrape-ck", pwd: "", scrape: true, target: os.Getenv("TEST_REDIS_URI"), ck: csk, cs: css, cntk: cntk},
		{name: "scrape-csk", pwd: "", scrape: true, target: os.Getenv("TEST_REDIS_URI"), csk: csk, css: css, cntk: cntk},

		{name: "scrape-pwd-ck", pwd: "redis-password", scrape: true, target: os.Getenv("TEST_PWD_REDIS_URI"), ck: csk, cs: css, cntk: cntk},
		{name: "scrape-pwd-csk", pwd: "redis-password", scrape: true, target: os.Getenv("TEST_PWD_REDIS_URI"), csk: csk, cs: css, cntk: cntk},

		{name: "error-scrape-no-target", wantStatusCode: http.StatusBadRequest, scrape: true, target: ""},
	} {
		t.Run(tst.name, func(t *testing.T) {
			options := Options{
				Namespace: "test",
				Password:  tst.pwd,
				LuaScript: []byte(`return {"a", "11", "b", "12", "c", "13"}`),
				Registry:  prometheus.NewRegistry(),
			}

			options.CheckSingleKeys = tst.csk
			options.CheckKeys = tst.ck
			options.CheckSingleStreams = tst.css
			options.CheckStreams = tst.cs
			options.CountKeys = tst.cntk

			e, _ := NewRedisExporter(tst.addr, options)
			ts := httptest.NewServer(e)

			u := ts.URL
			if tst.scrape {
				u += "/scrape"
				v := url.Values{}
				v.Add("target", tst.target)
				v.Add("check-single-keys", tst.csk)
				v.Add("check-keys", tst.ck)
				v.Add("check-streams", tst.cs)
				v.Add("check-single-streams", tst.css)
				v.Add("count-keys", tst.cntk)

				up, _ := url.Parse(u)
				up.RawQuery = v.Encode()
				u = up.String()
			} else {
				u += "/metrics"
			}

			wantStatusCode := http.StatusOK
			if tst.wantStatusCode != 0 {
				wantStatusCode = tst.wantStatusCode
			}

			gotStatusCode, body := downloadURLWithStatusCode(t, u)

			if gotStatusCode != wantStatusCode {
				t.Fatalf("got status code: %d   wanted: %d", gotStatusCode, wantStatusCode)
				return
			}

			// we can stop here if we expected a non-200 response
			if wantStatusCode != http.StatusOK {
				return
			}

			wants := []string{
				// metrics
				`test_connected_clients`,
				`test_commands_processed_total`,
				`test_instance_info`,

				"db_keys",
				"db_avg_ttl_seconds",
				"cpu_sys_seconds_total",
				"loading_dump_file", // testing renames
				"config_maxmemory",  // testing config extraction
				"config_maxclients", // testing config extraction
				"slowlog_length",
				"slowlog_last_id",
				"start_time_seconds",
				"uptime_in_seconds",

				// labels and label values
				`redis_mode`,
				`standalone`,
				`cmd="config`,
				"maxmemory_policy",

				`test_script_value`, // lua script

				`test_key_size{db="db11",key="` + keys[0] + `"} 7`,
				`test_key_value{db="db11",key="` + keys[0] + `"} 1234.56`,

				`test_keys_count{db="db11",key="` + keys[0] + `*"} 1`,

				`test_db_keys{db="db11"} `,
				`test_db_keys_expiring{db="db11"} `,
				// streams
				`stream_length`,
				`stream_groups`,
				`stream_radix_tree_keys`,
				`stream_radix_tree_nodes`,
				`stream_group_consumers`,
				`stream_group_messages_pending`,
				`stream_group_consumer_messages_pending`,
				`stream_group_consumer_idle_seconds`,
				`test_up 1`,
			}

			for _, want := range wants {
				if !strings.Contains(body, want) {
					t.Errorf("url: %s    want metrics to include %q, have:\n%s", u, want, body)
					break
				}
			}
			ts.Close()
		})
	}
}

func TestSimultaneousRequests(t *testing.T) {
	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	e, _ := NewRedisExporter("", Options{Namespace: "test", InclSystemMetrics: false, Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	uris := []string{
		os.Getenv("TEST_REDIS_URI"),
		os.Getenv("TEST_REDIS_2_8_URI"),

		os.Getenv("TEST_KEYDB01_URI"),
		os.Getenv("TEST_KEYDB02_URI"),

		os.Getenv("TEST_REDIS5_URI"),
		os.Getenv("TEST_REDIS6_URI"),

		os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI"),
		os.Getenv("TEST_REDIS_CLUSTER_SLAVE_URI"),

		os.Getenv("TEST_TILE38_URI"),
	}

	t.Logf("uris: %#v", uris)

	goroutines := 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for ; goroutines > 0; goroutines-- {
		go func() {
			requests := 100
			for ; requests > 0; requests-- {
				v := url.Values{}
				target := uris[rand.Intn(len(uris))]
				v.Add("target", target)
				v.Add("check-single-keys", dbNumStrFull+"="+url.QueryEscape(keys[0]))
				up, _ := url.Parse(ts.URL + "/scrape")
				up.RawQuery = v.Encode()
				fullURL := up.String()

				body := downloadURL(t, fullURL)
				wants := []string{
					`test_connected_clients`,
					`test_commands_processed_total`,
					`test_instance_info`,
					`test_up 1`,
				}
				for _, want := range wants {
					if !strings.Contains(body, want) {
						t.Errorf("fullURL: %s    - want metrics to include %q, have:\n%s", fullURL, want, body)
						break
					}
				}
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestNonExistingHost(t *testing.T) {
	e, _ := NewRedisExporter("unix:///tmp/doesnt.exist", Options{Namespace: "test"})

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := map[string]float64{"test_exporter_last_scrape_error": 1.0, "test_exporter_scrapes_total": 1.0}

	for m := range chM {
		descString := m.Desc().String()
		for k := range want {
			if strings.Contains(descString, k) {
				g := &dto.Metric{}
				m.Write(g)
				val := 0.0

				if g.GetGauge() != nil {
					val = *g.GetGauge().Value
				} else if g.GetCounter() != nil {
					val = *g.GetCounter().Value
				} else {
					continue
				}

				if val == want[k] {
					want[k] = -1.0
				}
			}
		}
	}
	for k, v := range want {
		if v > 0 {
			t.Errorf("didn't find %s", k)
		}
	}
}

func TestSanitizeMetricName(t *testing.T) {
	tsts := map[string]string{
		"cluster_stats_messages_auth-req_received": "cluster_stats_messages_auth_req_received",
		"cluster_stats_messages_auth_req_received": "cluster_stats_messages_auth_req_received",
	}

	for m, want := range tsts {
		if got := sanitizeMetricName(m); got != want {
			t.Errorf("sanitizeMetricName( %s ) error, want: %s, got: %s", m, want, got)
		}
	}
}

func TestParseClientListString(t *testing.T) {
	tsts := map[string][]string{
		"id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex":    []string{"", "6321", "6320", "N", "0", "0", "setex", "127.0.0.1", "63508"},
		"id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client": []string{"foo", "5", "0", "N", "1", "0", "client", "127.0.0.1", "64958"},
	}

	for k, v := range tsts {
		lbls, ok := parseClientListString(k)
		mismatch := false
		for idx, l := range lbls {
			if l != v[idx] {
				mismatch = true
				break
			}
		}
		if !ok || mismatch {
			t.Errorf("TestParseClientListString( %s ) error. Given: %s Wanted: %s", k, lbls, v)
		}
	}
}

func TestKeysReset(t *testing.T) {
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: dbNumStrFull + "=" + keys[0], Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, keys[0]) {
		t.Errorf("Did not found key %q\n%s", keys[0], body)
	}

	deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	body = downloadURL(t, ts.URL+"/metrics")
	if strings.Contains(body, keys[0]) {
		t.Errorf("Metric is present in metrics list %q\n%s", keys[0], body)
	}
}

func TestClusterMaster(t *testing.T) {
	if os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI") == "" {
		t.Skipf("TEST_REDIS_CLUSTER_MASTER_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI")
	e, _ := NewRedisExporter(addr, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	log.Debugf("master - body: %s", body)
	for _, want := range []string{
		"test_instance_info{",
		"test_master_repl_offset",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Did not find key [%s] \nbody: %s", want, body)
		}
	}
}

func TestPasswordProtectedInstance(t *testing.T) {
	userAddr := os.Getenv("TEST_USER_PWD_REDIS_URI")

	parsedPassword := ""
	parsed, err := url.Parse(userAddr)
	if err == nil && parsed.User != nil {
		parsedPassword, _ = parsed.User.Password()
	}

	tsts := []struct {
		name string
		addr string
		user string
		pwd  string
	}{
		{
			name: "TEST_PWD_REDIS_URI",
			addr: os.Getenv("TEST_PWD_REDIS_URI"),
		},
		{
			name: "TEST_USER_PWD_REDIS_URI",
			addr: userAddr,
		},
		{
			name: "parsed-TEST_USER_PWD_REDIS_URI",
			addr: parsed.Host,
			user: parsed.User.Username(),
			pwd:  parsedPassword,
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			e, _ := NewRedisExporter(
				tst.addr,
				Options{
					Namespace: "test",
					Registry:  prometheus.NewRegistry(),
					User:      tst.user,
					Password:  tst.pwd,
				})
			ts := httptest.NewServer(e)
			defer ts.Close()

			chM := make(chan prometheus.Metric, 10000)
			go func() {
				e.Collect(chM)
				close(chM)
			}()

			body := downloadURL(t, ts.URL+"/metrics")
			if !strings.Contains(body, "test_up 1") {
				t.Errorf(`%s - response to /metric doesn't contain "test_up 1"`, tst)
			}
		})
	}
}

func TestPasswordInvalid(t *testing.T) {
	if os.Getenv("TEST_PWD_REDIS_URI") == "" {
		t.Skipf("TEST_PWD_REDIS_URI not set - skipping")
	}

	testPwd := "redis-password"
	uri := strings.Replace(os.Getenv("TEST_PWD_REDIS_URI"), testPwd, "wrong-pwd", -1)

	e, _ := NewRedisExporter(uri, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := `test_exporter_last_scrape_error{err="dial redis: unknown network redis"} 1`
	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, want) {
		t.Errorf(`error, expected string "%s" in body, got body: \n\n%s`, want, body)
	}
}

func TestClusterSlave(t *testing.T) {
	if os.Getenv("TEST_REDIS_CLUSTER_SLAVE_URI") == "" {
		t.Skipf("TEST_REDIS_CLUSTER_SLAVE_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_CLUSTER_SLAVE_URI")
	e, _ := NewRedisExporter(addr, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	log.Debugf("slave - body: %s", body)
	for _, want := range []string{
		"test_instance_info",
		"test_master_last_io_seconds",
		"test_slave_info",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Did not find key [%s] \nbody: %s", want, body)
		}
	}
	hostReg, _ := regexp.Compile(`master_host="([0,1]?\d{1,2}|2([0-4][0-9]|5[0-5]))(\.([0,1]?\d{1,2}|2([0-4][0-9]|5[0-5]))){3}"`)
	masterHost := hostReg.FindString(string(body))
	portReg, _ := regexp.Compile(`master_port="(\d+)"`)
	masterPort := portReg.FindString(string(body))
	for wantedKey, wantedVal := range map[string]int{
		masterHost: 5,
		masterPort: 5,
	} {
		if res := strings.Count(body, wantedKey); res != wantedVal {
			t.Errorf("Result: %s -> %d, Wanted: %d \nbody: %s", wantedKey, res, wantedVal, body)
		}
	}
}

func TestCheckKeys(t *testing.T) {
	for _, tst := range []struct {
		SingleCheckKey string
		CheckKeys      string
		ExpectSuccess  bool
	}{
		{"", "", true},
		{"db1=key3", "", true},
		{"check-key-01", "", true},
		{"", "check-key-02", true},
		{"wrong=wrong=1", "", false},
		{"", "wrong=wrong=2", false},
	} {
		_, err := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: tst.SingleCheckKey, CheckKeys: tst.CheckKeys})
		if tst.ExpectSuccess && err != nil {
			t.Errorf("Expected success for test: %#v, got err: %s", tst, err)
			return
		}

		if !tst.ExpectSuccess && err == nil {
			t.Errorf("Expected failure for test: %#v, got no err", tst)
			return
		}
	}
}

func TestHTTPHTMLPages(t *testing.T) {
	if os.Getenv("TEST_PWD_REDIS_URI") == "" {
		t.Skipf("TEST_PWD_REDIS_URI not set - skipping")
	}

	e, _ := NewRedisExporter(os.Getenv("TEST_PWD_REDIS_URI"), Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	for _, tst := range []struct {
		path string
		want string
	}{
		{
			path: "/",
			want: `<head><title>Redis Exporter `,
		},
		{
			path: "/health",
			want: `ok`,
		},
	} {
		t.Run(fmt.Sprintf("path: %s", tst.path), func(t *testing.T) {
			body := downloadURL(t, ts.URL+tst.path)
			if !strings.Contains(body, tst.want) {
				t.Fatalf(`error, expected string "%s" in body, got body: \n\n%s`, tst.want, body)
			}
		})
	}
}

func TestRedisMetricsOnly(t *testing.T) {
	for _, inc := range []bool{false, true} {
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		_, err := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", Registry: r, RedisMetricsOnly: inc})
		if err != nil {
			t.Fatalf(`error when creating exporter with registry: %s`, err)
		}

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && strings.Contains(body, "exporter_build_info") {
			t.Errorf("want metrics to include exporter_build_info, have:\n%s", body)
		} else if !inc && !strings.Contains(body, "exporter_build_info") {
			t.Errorf("did NOT want metrics to include exporter_build_info, have:\n%s", body)
		}

		ts.Close()
	}
}

func TestConnectionDurations(t *testing.T) {
	metric1 := "exporter_last_scrape_ping_time_seconds"
	metric2 := "exporter_last_scrape_connect_time_seconds"

	for _, inclPing := range []bool{false, true} {
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", PingOnConnect: inclPing})
		r.Register(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if inclPing && !strings.Contains(body, metric1) {
			t.Fatalf("want metrics to include %s, have:\n%s", metric1, body)
		} else if !inclPing && strings.Contains(body, metric1) {
			t.Fatalf("did NOT want metrics to include %s, have:\n%s", metric1, body)
		}

		// always expect this one
		if !strings.Contains(body, metric2) {
			t.Fatalf("want metrics to include %s, have:\n%s", metric2, body)
		}
		ts.Close()
	}
}

func init() {
	rand.Seed(time.Now().UnixNano())

	ll := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if pl, err := log.ParseLevel(ll); err == nil {
		log.Printf("Setting log level to: %s", ll)
		log.SetLevel(pl)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	for _, n := range []string{"john", "paul", "ringo", "george"} {
		key := fmt.Sprintf("key_%s_%d", n, ts)
		keys = append(keys, key)
	}

	listKeys = append(listKeys, "beatles_list")

	for _, n := range []string{"A.J.", "Howie", "Nick", "Kevin", "Brian"} {
		key := fmt.Sprintf("key_exp_%s_%d", n, ts)
		keysExpiring = append(keysExpiring, key)
	}
}

type scanStreamFixture struct {
	name       string
	stream     string
	pass       bool
	streamInfo streamInfo
	groups     []streamGroupsInfo
	consumers  []streamGroupConsumersInfo
}

func TestGetStreamInfo(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	setupDBKeys(t, addr)
	defer deleteKeysFromDB(t, addr)

	_, err = c.Do("SELECT", dbNumStr)
	if err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	tsts := []scanStreamFixture{
		{
			name:   "Stream test",
			stream: TestStreamName,
			pass:   true,
			streamInfo: streamInfo{
				Length:         2,
				RadixTreeKeys:  1,
				RadixTreeNodes: 2,
				Groups:         2,
			},
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			info, err := getStreamInfo(c, tst.stream)
			if err != nil {
				t.Fatalf("Error getting stream info for %#v: %s", tst.stream, err)
			}

			if info.Length != tst.streamInfo.Length {
				t.Errorf("Stream length mismatch.\nActual: %#v;\nExpected: %#v\n", info.Length, tst.streamInfo.Length)
			}
			if info.RadixTreeKeys != tst.streamInfo.RadixTreeKeys {
				t.Errorf("Stream RadixTreeKeys mismatch.\nActual: %#v;\nExpected: %#v\n", info.RadixTreeKeys, tst.streamInfo.RadixTreeKeys)
			}
			if info.RadixTreeNodes != tst.streamInfo.RadixTreeNodes {
				t.Errorf("Stream RadixTreeNodes mismatch.\nActual: %#v;\nExpected: %#v\n", info.RadixTreeNodes, tst.streamInfo.RadixTreeNodes)
			}
			if info.Groups != tst.streamInfo.Groups {
				t.Errorf("Stream Groups mismatch.\nActual: %#v;\nExpected: %#v\n", info.Groups, tst.streamInfo.Groups)
			}
		})
	}
}

func TestScanStreamGroups(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"XADD", "test_stream_1", []interface{}{"*", "field_1", "str_1"}},
		{"XADD", "test_stream_2", []interface{}{"*", "field_pattern_1", "str_pattern_1"}},
	}
	// Create test streams
	_, err = c.Do("XGROUP", "CREATE", "test_stream_1", "test_group_1", "$", "MKSTREAM")
	_, err = c.Do("XGROUP", "CREATE", "test_stream_2", "test_group_1", "$", "MKSTREAM")
	_, err = c.Do("XGROUP", "CREATE", "test_stream_2", "test_group_2", "$")
	// Add simple values
	createKeyFixtures(t, c, fixtures)
	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()
	// Process messages to assign Consumers to their groups
	_, err = c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "test_stream_1", ">")
	_, err = c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "test_stream_2", ">")
	_, err = c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", "test_stream_2", "0")

	tsts := []scanStreamFixture{
		{
			name:   "Single group test",
			stream: "test_stream_1",
			groups: []streamGroupsInfo{
				{
					Name:      "test_group_1",
					Consumers: 1,
					Pending:   1,
					StreamGroupConsumersInfo: []streamGroupConsumersInfo{
						{
							Name:    "test_consumer_1",
							Pending: 1,
						},
					},
				},
			}},
		{
			name:   "Multiple groups test",
			stream: "test_stream_2",
			groups: []streamGroupsInfo{
				{
					Name:      "test_group_1",
					Consumers: 2,
					Pending:   1,
				},
				{
					Name:      "test_group_2",
					Consumers: 0,
					Pending:   0,
				},
			}},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			scannedGroup, _ := scanStreamGroups(c, tst.stream)
			if err != nil {
				t.Errorf("Err: %s", err)
			}

			if len(scannedGroup) == len(tst.groups) {
				for i := range scannedGroup {
					if scannedGroup[i].Name != tst.groups[i].Name {
						t.Errorf("Group name mismatch.\nExpected: %#v;\nActual: %#v\n", tst.groups[i].Name, scannedGroup[i].Name)
					}
					if scannedGroup[i].Consumers != tst.groups[i].Consumers {
						t.Errorf("Consumers count mismatch.\nExpected: %#v;\nActual: %#v\n", tst.groups[i].Consumers, scannedGroup[i].Consumers)
					}
					if scannedGroup[i].Pending != tst.groups[i].Pending {
						t.Errorf("Pending items mismatch.\nExpected: %#v;\nActual: %#v\n", tst.groups[i].Pending, scannedGroup[i].Pending)
					}

				}
			} else {
				t.Errorf("Consumers entries mismatch.\nExpected: %d;\nActual: %d\n", len(tst.consumers), len(scannedGroup))
			}
		})
	}
}

func TestScanStreamGroupsConsumers(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"XADD", "single_consumer_stream", []interface{}{"*", "field_1", "str_1"}},
		{"XADD", "multiple_consumer_stream", []interface{}{"*", "field_pattern_1", "str_pattern_1"}},
	}
	// Create test streams
	_, err = c.Do("XGROUP", "CREATE", "single_consumer_stream", "test_group_1", "$", "MKSTREAM")
	_, err = c.Do("XGROUP", "CREATE", "multiple_consumer_stream", "test_group_1", "$", "MKSTREAM")
	// Add simple test items to streams
	createKeyFixtures(t, c, fixtures)
	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()
	// Process messages to assign Consumers to their groups
	_, err = c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "single_consumer_stream", ">")
	_, err = c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "multiple_consumer_stream", ">")
	_, err = c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", "multiple_consumer_stream", "0")

	tsts := []scanStreamFixture{
		{
			name:   "Single group test",
			stream: "single_consumer_stream",
			groups: []streamGroupsInfo{{Name: "test_group_1"}},
			consumers: []streamGroupConsumersInfo{
				{
					Name:    "test_consumer_1",
					Pending: 1,
				},
			},
		},
		{
			name:   "Multiple consumers test",
			stream: "multiple_consumer_stream",
			groups: []streamGroupsInfo{{Name: "test_group_1"}},
			consumers: []streamGroupConsumersInfo{
				{
					Name:    "test_consumer_1",
					Pending: 1,
				},
				{
					Name:    "test_consumer_2",
					Pending: 0,
				},
			},
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {

			// For each group
			for _, g := range tst.groups {
				g.StreamGroupConsumersInfo, err = scanStreamGroupConsumers(c, tst.stream, g.Name)
				if err != nil {
					t.Errorf("Err: %s", err)
				}
				if len(g.StreamGroupConsumersInfo) == len(tst.consumers) {
					for i := range g.StreamGroupConsumersInfo {
						if g.StreamGroupConsumersInfo[i].Name != tst.consumers[i].Name {
							t.Errorf("Consumer name mismatch.\nExpected: %#v;\nActual: %#v\n", tst.consumers[i].Name, g.StreamGroupConsumersInfo[i].Name)
						}
						if g.StreamGroupConsumersInfo[i].Pending != tst.consumers[i].Pending {
							t.Errorf("Pending items mismatch for %s.\nExpected: %#v;\nActual: %#v\n", g.StreamGroupConsumersInfo[i].Name, tst.consumers[i].Pending, g.StreamGroupConsumersInfo[i].Pending)
						}

					}
				} else {
					t.Errorf("Consumers entries mismatch.\nExpected: %d;\nActual: %d\n", len(tst.consumers), len(g.StreamGroupConsumersInfo))
				}
			}

		})
	}
}

func TestExtractStreamMetrics(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test", CheckSingleStreams: dbNumStrFull + "=" + TestStreamName},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	setupDBKeys(t, addr)
	defer deleteKeysFromDB(t, addr)

	chM := make(chan prometheus.Metric)
	go func() {
		e.extractStreamMetrics(chM, c)
		close(chM)
	}()
	want := map[string]bool{
		"stream_length":                          false,
		"stream_radix_tree_keys":                 false,
		"stream_radix_tree_nodes":                false,
		"stream_groups":                          false,
		"stream_group_consumers":                 false,
		"stream_group_messages_pending":          false,
		"stream_group_consumer_messages_pending": false,
		"stream_group_consumer_idle_seconds":     false,
	}

	for m := range chM {
		for k := range want {
			log.Debugf("metric: %s", m.Desc().String())
			log.Debugf("want: %s", k)
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}

	}
}

func TestExtractInfoMetricsSentinel(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil {
		t.Logf("Redis INFO ALL err: %s", err)
		infoAll, err = redis.String(doRedisCmd(c, "INFO"))
		if err != nil {
			t.Fatalf("Redis INFO err: %s", err)
		}
	}

	chM := make(chan prometheus.Metric)
	go func() {
		e.extractInfoMetrics(chM, infoAll, 0)
		close(chM)
	}()
	want := map[string]bool{
		"sentinel_tilt":                   false,
		"sentinel_running_scripts":        false,
		"sentinel_scripts_queue_length":   false,
		"sentinel_simulate_failure_flags": false,
		"sentinel_masters":                false,
		"sentinel_master_status":          false,
		"sentinel_master_slaves":          false,
		"sentinel_master_sentinels":       false,
	}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}

	}
}

type sentinelData struct {
	k, v                  string
	name, status, address string
	slaves, sentinels     float64
	ok                    bool
}

func TestParseSentinelMasterString(t *testing.T) {
	tsts := []sentinelData{
		{k: "master0", v: "name=user03,status=sdown,address=192.169.2.52:6381,slaves=1,sentinels=5", name: "user03", status: "sdown", address: "192.169.2.52:6381", slaves: 1, sentinels: 5, ok: true},
		{k: "master1", v: "name=master,status=ok,address=127.0.0.1:6379,slaves=999,sentinels=500", name: "master", status: "ok", address: "127.0.0.1:6379", slaves: 999, sentinels: 500, ok: true},

		{k: "master", v: "name=user03", ok: false},
		{k: "masterA", v: "status=ko", ok: false},
		{k: "master0", v: "slaves=abc,sentinels=0", ok: false},
		{k: "master0", v: "slaves=0,sentinels=abc", ok: false},
	}

	for _, tst := range tsts {
		name := fmt.Sprintf("%s---%s", tst.k, tst.v)
		t.Run(name, func(t *testing.T) {
			if masterName, masterStatus, masterAddress, masterSlaves, masterSentinels, ok := parseSentinelMasterString(tst.k, tst.v); true {
				if ok != tst.ok {
					t.Errorf("failed for: master:%s data:%s", tst.k, tst.v)
					return
				}
				if masterName != tst.name || masterStatus != tst.status || masterAddress != tst.address || masterSlaves != tst.slaves || masterSentinels != tst.sentinels {
					t.Errorf("values not matching:\nstring:%s\ngot:%s %s %s %f %f", tst.v, masterName, masterStatus, masterAddress, masterSlaves, masterSentinels)
				}
			}
		})
	}
}

func TestExtractSentinelMetricsForRedis(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	defer c.Close()

	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	chM := make(chan prometheus.Metric)
	go func() {
		e.extractSentinelMetrics(chM, c)
		close(chM)
	}()

	want := map[string]bool{
		"sentinel_master_ok_sentinels": false,
		"sentinel_master_ok_slaves":    false,
	}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if found {
			t.Errorf("Found sentinel metric %s for redis instance", k)
		}
	}
}

func TestExtractSentinelMetricsForSentinel(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil {
		t.Logf("Redis INFO ALL err: %s", err)
		infoAll, err = redis.String(doRedisCmd(c, "INFO"))
		if err != nil {
			t.Fatalf("Redis INFO err: %s", err)
		}
	}

	chM := make(chan prometheus.Metric)
	if strings.Contains(infoAll, "# Sentinel") {
		go func() {
			e.extractSentinelMetrics(chM, c)
			close(chM)
		}()
	} else {
		t.Fatalf("Couldn't find sentinel section in Redis INFO: %s", infoAll)
	}

	want := map[string]bool{
		"sentinel_master_ok_sentinels": false,
		"sentinel_master_ok_slaves":    false,
	}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find metric %s", k)
		}
	}
}

type sentinelSentinelsData struct {
	name                string
	sentinelDetails     []interface{}
	labels              []string
	expectedMetricValue map[string]int
}

func TestProcessSentinelSentinels(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)

	oneOkSentinelExpectedMetricValue := map[string]int{
		"sentinel_master_ok_sentinels": 1,
	}
	twoOkSentinelExpectedMetricValue := map[string]int{
		"sentinel_master_ok_sentinels": 2,
	}
	tsts := []sentinelSentinelsData{
		{"1/1 okay sentinel", []interface{}{[]interface{}{[]byte("")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSentinelExpectedMetricValue},
		{"1/3 okay sentinel", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("o_down,s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), []byte("s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSentinelExpectedMetricValue},
		{"2/3 okay sentinel(string is not byte slice)", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), ("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue},
		{"2/3 okay sentinel", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), []byte("s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue},
		{"2/3 okay sentinel(missing flags)", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			chM := make(chan prometheus.Metric)
			go func() {
				e.processSentinelSentinels(chM, tst.sentinelDetails, tst.labels...)
				close(chM)
			}()
			want := map[string]bool{
				"sentinel_master_ok_sentinels": false,
			}

			for m := range chM {
				for k := range want {
					if strings.Contains(m.Desc().String(), k) {
						want[k] = true
						got := &dto.Metric{}
						m.Write(got)

						val := got.GetGauge().GetValue()
						if int(val) != tst.expectedMetricValue[k] {
							t.Errorf("Expected metric value %d didn't match to reported value %d for test %s", tst.expectedMetricValue[k], int(val), tst.name)
						}
					}
				}
			}
			for k, found := range want {
				if !found {
					t.Errorf("didn't find metric %s", k)
				}
			}
		})
	}
}

type sentinelSlavesData struct {
	name                string
	slaveDetails        []interface{}
	labels              []string
	expectedMetricValue map[string]int
}

func TestProcessSentinelSlaves(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	zeroOkSlaveExpectedMetricValue := map[string]int{
		"sentinel_master_ok_slaves": 0,
	}
	oneOkSlaveExpectedMetricValue := map[string]int{
		"sentinel_master_ok_slaves": 1,
	}
	twoOkSlaveExpectedMetricValue := map[string]int{
		"sentinel_master_ok_slaves": 2,
	}

	tsts := []sentinelSlavesData{
		{"0/1 okay slave(string is not byte slice)", []interface{}{[]interface{}{[]string{"name"}, []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("490"), []byte("last-ping-reply"), []byte("490"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("2636"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("48279581"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("765829")}}, []string{"mymaster", "172.17.0.7:26379"}, zeroOkSlaveExpectedMetricValue},
		{"1/1 okay slave", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("490"), []byte("last-ping-reply"), []byte("490"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("2636"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("48279581"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("765829")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSlaveExpectedMetricValue},
		{"1/3 okay slave", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("s_down,slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("flags"), []byte("o_down,s_down,slave"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSlaveExpectedMetricValue},
		{"2/3 okay slave", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("flags"), []byte("s_down,slave"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSlaveExpectedMetricValue},
		{"2/3 okay slave(missing flags)", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSlaveExpectedMetricValue},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			chM := make(chan prometheus.Metric)
			go func() {
				e.processSentinelSlaves(chM, tst.slaveDetails, tst.labels...)
				close(chM)
			}()
			want := map[string]bool{
				"sentinel_master_ok_slaves": false,
			}

			for m := range chM {
				for k := range want {
					if strings.Contains(m.Desc().String(), k) {
						want[k] = true
						got := &dto.Metric{}
						m.Write(got)

						val := got.GetGauge().GetValue()
						if int(val) != tst.expectedMetricValue[k] {
							t.Errorf("Expected metric value %d didn't match to reported value %d for test %s", tst.expectedMetricValue[k], int(val), tst.name)
						}
					}
				}
			}
			for k, found := range want {
				if !found {
					t.Errorf("didn't find metric %s", k)
				}
			}
		})
	}
}

func TestHTTPScrapeWithPasswordFile(t *testing.T) {
	if os.Getenv("TEST_REDIS_PWD_FILE") == "" {
		t.Fatalf("TEST_REDIS_PWD_FILE not set!")
	}

	passwordMap, err := LoadPwdFile(os.Getenv("TEST_REDIS_PWD_FILE"))
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	if len(passwordMap) == 0 {
		t.Skipf("Password map is empty -skipping")
	}
	for target, pwd := range passwordMap {
		for _, tst := range []struct {
			name           string
			wants          []string
			password       string
			wantStatusCode int
		}{
			{name: "scrape-pwd-file", password: pwd, wants: []string{
				"uptime_in_seconds",
				"test_up 1",
			}},
			{name: "scrape-pwd-file-error-password", password: "error_password", wants: []string{
				"test_up 0",
			}},
		} {
			options := Options{
				Namespace: "test",
				Password:  tst.password,
				LuaScript: []byte(`return {"a", "11", "b", "12", "c", "13"}`),
				Registry:  prometheus.NewRegistry(),
			}
			t.Run(tst.name, func(t *testing.T) {
				e, _ := NewRedisExporter(target, options)
				ts := httptest.NewServer(e)

				u := ts.URL
				u += "/scrape"
				v := url.Values{}
				v.Add("target", target)

				up, _ := url.Parse(u)
				up.RawQuery = v.Encode()
				u = up.String()

				wantStatusCode := http.StatusOK
				if tst.wantStatusCode != 0 {
					wantStatusCode = tst.wantStatusCode
				}

				gotStatusCode, body := downloadURLWithStatusCode(t, u)

				if gotStatusCode != wantStatusCode {
					t.Fatalf("got status code: %d   wanted: %d", gotStatusCode, wantStatusCode)
					return
				}

				// we can stop here if we expected a non-200 response
				if wantStatusCode != http.StatusOK {
					return
				}

				for _, want := range tst.wants {
					if !strings.Contains(body, want) {
						t.Errorf("url: %s    want metrics to include %q, have:\n%s", u, want, body)
						break
					}
				}
				ts.Close()
			})

		}
	}
}
