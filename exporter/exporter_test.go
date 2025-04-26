package exporter

/*
  to run the tests with redis running on anything but localhost:6379 use
  $ go test   --redis.addr=<host>:<port>

  for html coverage report run
  $ go test -coverprofile=coverage.out  && go tool cover -html=coverage.out
*/

import (
	"fmt"
	"github.com/mna/redisc"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
)

const (
	dbNumStr        = "11"
	altDBNumStr     = "12"
	invalidDBNumStr = "16"

	anotherAltDbNumStr = "14"
)

const (
	TestKeysSetName    = "test-set"
	TestKeysZSetName   = "test-zset"
	TestKeysStreamName = "test-stream"
	TestKeysHllName    = "test-hll"
	TestKeysHashName   = "test-hash"
	TestKeyGroup1      = "test_group_1"
	TestKeyGroup2      = "test_group_2"
)

var (
	AllTestKeys = []string{
		TestKeysSetName, TestKeysZSetName,
		TestKeysStreamName,
		TestKeysHllName, TestKeysHashName,
		TestKeyGroup1, TestKeyGroup2,
	}
)

var (
	testKeys         []string
	testKeysExpiring []string
	testKeysList     []string

	testKeySingleString string

	dbNumStrFull = fmt.Sprintf("db%s", dbNumStr)
)

var (
	TestKeyNameSingleString = "" // initialized with a timestamp at runtime
	TestKeyNameSet          = "test-set"
	TestKeyNameStream       = "test-stream"
	TestKeyNameHll          = "test-hll"
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

func getTestExporterWithAddr(addr string) *Exporter {
	e, _ := NewRedisExporter(addr, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	return e
}

func getTestExporterWithAddrAndOptions(addr string, opt Options) *Exporter {
	e, _ := NewRedisExporter(addr, opt)
	return e
}

func setupKeys(t *testing.T, c redis.Conn, dbNum string) error {
	if _, err := doRedisCmd(c, "SELECT", dbNum); err != nil {
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
		log.Printf("setupTestKeys() - couldn't setup redis, err: %s ", err)
	}

	testValue := 1234.56
	for _, key := range testKeys {
		if _, err := doRedisCmd(c, "SET", key, testValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	// set to expire in 600 seconds, should be plenty for a test run
	for _, key := range testKeysExpiring {
		if _, err := doRedisCmd(c, "SETEX", key, "600", testValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	for _, key := range testKeysList {
		for _, val := range testKeys {
			if _, err := doRedisCmd(c, "LPUSH", key, val); err != nil {
				t.Errorf("couldn't setup redis, err: %s ", err)
				return err
			}
		}
	}

	if _, err := c.Do("PFADD", TestKeyNameHll, "val1"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}
	if _, err := c.Do("PFADD", TestKeyNameHll, "val22"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}
	if _, err := c.Do("PFADD", TestKeyNameHll, "val333"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}

	if _, err := c.Do("SADD", TestKeyNameSet, "test-val-1"); err != nil {
		t.Errorf("SADD err: %s", err)
		return err
	}
	if _, err := c.Do("SADD", TestKeyNameSet, "test-val-2"); err != nil {
		t.Errorf("SADD err: %s", err)
		return err
	}

	if _, err := c.Do("SET", TestKeyNameSingleString, "this-is-a-string"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "ZADD", TestKeysZSetName, "23", "test-zzzval-2"); err != nil {
		t.Errorf("ZADD err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "ZADD", TestKeysZSetName, "45", "test-zzzval-3"); err != nil {
		t.Errorf("ZADD err: %s", err)
		return err
	}

	if _, err := doRedisCmd(c, "SET", testKeySingleString, "this-is-a-string"); err != nil {
		t.Errorf("SET %s err: %s", testKeySingleString, err)
		return err
	}

	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field1", "Hello"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field2", "World"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field3", "What's"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field4", "new?"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}

	if x, err := redis.String(doRedisCmd(c, "HGET", TestKeysHashName, "field4")); err != nil || x != "new?" {
		t.Errorf("HGET %s err: %s  x: %s", TestKeysHashName, err, x)
	}

	// Create test streams
	c.Do("XGROUP", "CREATE", TestKeyNameStream, "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", TestKeyNameStream, "test_group_2", "$", "MKSTREAM")
	c.Do("XADD", TestKeyNameStream, TestStreamTimestamps[0], "field_1", "str_1")
	c.Do("XADD", TestKeyNameStream, TestStreamTimestamps[1], "field_2", "str_2")

	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", TestKeyNameStream, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", TestKeyNameStream, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_2", "test_consumer_1", "COUNT", "1", "STREAMS", TestKeyNameStream, "0")

	time.Sleep(time.Millisecond * 100)
	return nil
}

func deleteKeys(c redis.Conn, dbNum string) {
	if _, err := doRedisCmd(c, "SELECT", dbNum); err != nil {
		log.Printf("deleteTestKeys() - couldn't setup redis, err: %s ", err)
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
	}

	for _, key := range AllTestKeys {
		doRedisCmd(c, "DEL", key)
	}

	for _, key := range testKeysExpiring {
		c.Do("DEL", key)
	}

	for _, key := range testKeysList {
		c.Do("DEL", key)
	}

	c.Do("DEL", TestKeyNameHll)
	c.Do("DEL", TestKeyNameSet)
	c.Do("DEL", TestKeyNameStream)
	c.Do("DEL", TestKeyNameSingleString)
}

func setupTestKeys(t *testing.T, uri string) {
	log.Debugf("setupTestKeys uri: %s", uri)
	c, err := redis.DialURL(uri)
	if err != nil {
		t.Fatalf("couldn't setup redis for uri %s, err: %s ", uri, err)
		return
	}
	defer c.Close()

	if err := setupKeys(t, c, dbNumStr); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
	}
	if err := setupKeys(t, c, altDBNumStr); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
	}
	if err := setupKeys(t, c, anotherAltDbNumStr); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
	}
}

func setupTestKeysCluster(t *testing.T, uri string) {
	log.Debugf("Creating cluster object")
	cluster := redisc.Cluster{
		StartupNodes: []string{
			strings.Replace(uri, "redis://", "", 1),
		},
		DialOptions: []redis.DialOption{},
	}

	if err := cluster.Refresh(); err != nil {
		log.Fatalf("Refresh failed: %v", err)
	}

	conn, err := cluster.Dial()
	if err != nil {
		log.Errorf("Dial() failed: %v", err)
	}

	c, err := redisc.RetryConn(conn, 10, 100*time.Millisecond)
	if err != nil {
		log.Errorf("RetryConn() failed: %v", err)
	}

	// cluster only supports db==0
	if err := setupKeys(t, c, "0"); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
		return
	}

	time.Sleep(time.Second)

	if x, err := redis.Strings(doRedisCmd(c, "KEYS", "*")); err != nil {
		t.Errorf("KEYS * err: %s", err)
	} else {
		t.Logf("KEYS * -> %#v", x)
	}
}

func deleteTestKeys(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	deleteKeys(c, dbNumStr)
	deleteKeys(c, altDBNumStr)
	deleteKeys(c, anotherAltDbNumStr)

	return nil
}

func deleteTestKeysCluster(t *testing.T, addr string) error {
	e, _ := NewRedisExporter(addr, Options{})
	c, err := e.connectToRedisCluster()
	if err != nil {
		t.Errorf("couldn't setup redis CLUSTER, err: %s ", err)
		return err
	}

	defer c.Close()

	// cluster only supports db==0
	deleteKeys(c, "0")

	return nil
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

func TestIncludeConfigMetrics(t *testing.T) {
	for _, inc := range []bool{false, true} {
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", InclConfigMetrics: inc})
		r.Register(e)

		what := `test_config_key_value{key="appendonly",value="no"}`

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && !strings.Contains(body, what) {
			t.Errorf("want metrics to include test_config_key_value, have:\n%s", body)
		} else if !inc && strings.Contains(body, what) {
			t.Errorf("did NOT want metrics to include test_config_key_value, have:\n%s", body)
		}

		ts.Close()
	}
}

func TestClientOutputBufferLimitMetrics(t *testing.T) {
	for _, class := range []string{
		`normal`,
		`pubsub`,
		`slave`,
	} {
		for _, limit := range []string{
			`hard`,
			`soft`,
		} {
			want := fmt.Sprintf("%s{class=\"%s\",limit=\"%s\"}", "config_client_output_buffer_limit_bytes", class, limit)
			r := prometheus.NewRegistry()
			ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
			e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test"})
			r.Register(e)

			body := downloadURL(t, ts.URL+"/metrics")

			if !strings.Contains(body, want) {
				t.Errorf("want metrics to include %s, have:\n%s", want, body)
			}
		}

		want := fmt.Sprintf("%s{class=\"%s\",limit=\"soft\"}", "config_client_output_buffer_limit_overcome_seconds", class)
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test"})
		r.Register(e)

		body := downloadURL(t, ts.URL+"/metrics")

		if !strings.Contains(body, want) {
			t.Errorf("want metrics to include %s, have:\n%s", want, body)
		}
	}
}

func TestExcludeConfigMetricsViaCONFIGCommand(t *testing.T) {
	for _, inc := range []bool{false, true} {
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"),
			Options{
				Namespace:         "test",
				ConfigCommandName: "-",
				InclConfigMetrics: inc})
		r.Register(e)

		what := `test_config_key_value{key="appendonly",value="no"}`

		body := downloadURL(t, ts.URL+"/metrics")
		if strings.Contains(body, what) {
			t.Fatalf("found test_config_key_value but should have skipped CONFIG call")
		}

		ts.Close()
	}
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

func TestKeysReset(t *testing.T) {
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: dbNumStrFull + "=" + testKeys[0], Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	setupTestKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteTestKeys(t, os.Getenv("TEST_REDIS_URI"))

	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, testKeys[0]) {
		t.Errorf("Did not find key %q\n%s", testKeys[0], body)
	}

	deleteTestKeys(t, os.Getenv("TEST_REDIS_URI"))

	body = downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, testKeys[0]) {
		t.Errorf("Key %q (from check-single-keys) should be available in metrics with default value 0\n%s", testKeys[0], body)
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

func TestKeyDbMetrics(t *testing.T) {
	if os.Getenv("TEST_KEYDB01_URI") == "" {
		t.Skipf("Skipping due to missing TEST_KEYDB01_URI")
	}

	setupTestKeys(t, os.Getenv("TEST_KEYDB01_URI"))
	defer deleteTestKeys(t, os.Getenv("TEST_KEYDB01_URI"))

	for _, want := range []string{
		`test_db_keys_cached`,
		`test_storage_provider_read_hits`,
	} {
		r := prometheus.NewRegistry()
		ts := httptest.NewServer(promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		e, _ := NewRedisExporter(os.Getenv("TEST_KEYDB01_URI"), Options{Namespace: "test"})
		r.Register(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if !strings.Contains(body, want) {
			t.Errorf("want metrics to include %s, have:\n%s", want, body)
		}

		ts.Close()
	}
}

func init() {
	ll := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if pl, err := log.ParseLevel(ll); err == nil {
		log.Printf("Setting log level to: %s", ll)
		log.SetLevel(pl)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	testTimestamp := time.Now().Unix()

	for _, n := range []string{"john", "paul", "ringo", "george"} {
		testKeys = append(testKeys, fmt.Sprintf("key_%s_%d", n, testTimestamp))
	}

	TestKeyNameSingleString = fmt.Sprintf("key_string_%d", testTimestamp)

	testKeysList = append(testKeysList, "test_beatles_list")

	for _, n := range []string{"A.J.", "Howie", "Nick", "Kevin", "Brian"} {
		testKeysExpiring = append(testKeysExpiring, fmt.Sprintf("key_exp_%s_%d", n, testTimestamp))
	}

	AllTestKeys = append(AllTestKeys, testKeys...)
	AllTestKeys = append(AllTestKeys, testKeysList...)
	AllTestKeys = append(AllTestKeys, testKeysExpiring...)
}
