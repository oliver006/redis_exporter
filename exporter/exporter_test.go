package exporter

/*
  to run the tests with redis running on anything but localhost:6379 use
  $ go test   --redis.addr=<host>:<port>

  for html coverage report run
  $ go test -coverprofile=coverage.out  && go tool cover -html=coverage.out
*/

import (
	"fmt"
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
)

var (
	keys         []string
	keysExpiring []string
	listKeys     []string

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

func setupKeys(t *testing.T, c redis.Conn, dbNumStr string) error {
	if _, err := c.Do("SELECT", dbNumStr); err != nil {
		log.Printf("setupDBKeys() - couldn't setup redis, err: %s ", err)
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
	}

	testValue := 1234.56
	for _, key := range keys {
		if _, err := c.Do("SET", key, testValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	// set to expire in 300 seconds, should be plenty for a test run
	for _, key := range keysExpiring {
		if _, err := c.Do("SETEX", key, "300", testValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	for _, key := range listKeys {
		for _, val := range keys {
			if _, err := c.Do("LPUSH", key, val); err != nil {
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

func deleteKeys(c redis.Conn, dbNumStr string) {
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

	c.Do("DEL", TestKeyNameHll)
	c.Do("DEL", TestKeyNameSet)
	c.Do("DEL", TestKeyNameStream)
	c.Do("DEL", TestKeyNameSingleString)
}

func setupDBKeys(t *testing.T, uri string) {
	log.Debugf("setupDBKeys uri: %s", uri)
	c, err := redis.DialURL(uri)
	if err != nil {
		t.Fatalf("couldn't setup redis for uri %s, err: %s ", uri, err)
		return
	}
	defer c.Close()

	if err = setupKeys(t, c, dbNumStr); err != nil {
		t.Fatalf("couldn't setup redis, err: %s ", err)
	}
}

func setupDBKeysCluster(t *testing.T, uri string) {
	e, _ := NewRedisExporter(uri, Options{})
	c, err := e.connectToRedisCluster()
	if err != nil {
		t.Fatalf("connectToRedisCluster() err: %s ", err)
		return
	}

	defer c.Close()

	if err = setupKeys(t, c, "0"); err != nil {
		t.Fatalf("couldn't setup redis, err: %s ", err)
		return
	}
}

func deleteKeysFromDB(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	deleteKeys(c, dbNumStr)

	return nil
}

func deleteKeysFromDBCluster(addr string) error {
	e, _ := NewRedisExporter(addr, Options{})
	c, err := e.connectToRedisCluster()
	if err != nil {
		return err
	}

	defer c.Close()

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
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: dbNumStrFull + "=" + keys[0], Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, keys[0]) {
		t.Errorf("Did not find key %q\n%s", keys[0], body)
	}

	deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	body = downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, keys[0]) {
		t.Errorf("Key %q (from check-single-keys) should be available in metrics with default value 0\n%s", keys[0], body)
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

	setupDBKeys(t, os.Getenv("TEST_KEYDB01_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_KEYDB01_URI"))

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
		keys = append(keys, fmt.Sprintf("key_%s_%d", n, testTimestamp))
	}

	TestKeyNameSingleString = fmt.Sprintf("key_string_%d", testTimestamp)

	listKeys = append(listKeys, "beatles_list")

	for _, n := range []string{"A.J.", "Howie", "Nick", "Kevin", "Brian"} {
		keysExpiring = append(keysExpiring, fmt.Sprintf("key_exp_%s_%d", n, testTimestamp))
	}
}
