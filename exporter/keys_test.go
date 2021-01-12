package exporter

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

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
	if err != errKeyTypeNotFound {
		t.Error("Expected `errKeyTypeNotFound` for absent key.  Got a different error.")
	}
}

func TestKeySizeList(t *testing.T) {
	s := dbNumStrFull + "=" + listKeys[0]
	e, _ := NewRedisExporter(
		os.Getenv("TEST_REDIS_URI"),
		Options{Namespace: "test", CheckSingleKeys: s},
	)

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

func TestKeyValueInvalidDB(t *testing.T) {
	e, _ := NewRedisExporter(
		os.Getenv("TEST_REDIS_URI"),
		Options{
			Namespace:       "test",
			CheckSingleKeys: "999=" + url.QueryEscape(keys[0]),
		},
	)

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
