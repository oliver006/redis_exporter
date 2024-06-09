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
	TestValue   = 1234.56
	TimeToSleep = 200
)

var (
	keys            []string
	keysExpiring    []string
	listKeys        []string
	singleStringKey string
	ts              = int32(time.Now().Unix())

	dbNumStr        = "11"
	altDBNumStr     = "12"
	invalidDBNumStr = "16"
	dbNumStrFull    = fmt.Sprintf("db%s", dbNumStr)
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

	for _, key := range keys {
		if _, err := c.Do("SET", key, TestValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	// setting to expire in 300 seconds, should be plenty for a test run
	for _, key := range keysExpiring {
		if _, err := c.Do("SETEX", key, "300", TestValue); err != nil {
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

	c.Do("SADD", TestSetName, "test-val-1")
	c.Do("SADD", TestSetName, "test-val-2")

	c.Do("SET", singleStringKey, "this-is-a-string")

	// Create test streams
	c.Do("XGROUP", "CREATE", TestStreamName, "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", TestStreamName, "test_group_2", "$", "MKSTREAM")
	c.Do("XADD", TestStreamName, TestStreamTimestamps[0], "field_1", "str_1")
	c.Do("XADD", TestStreamName, TestStreamTimestamps[1], "field_2", "str_2")
	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", TestStreamName, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", TestStreamName, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_2", "test_consumer_1", "COUNT", "1", "STREAMS", TestStreamName, "0")

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

	c.Do("DEL", TestSetName)
	c.Do("DEL", TestStreamName)
	c.Do("DEL", singleStringKey)
}

func setupDBKeys(t *testing.T, uri string) error {
	c, err := redis.DialURL(uri)
	if err != nil {
		t.Errorf("couldn't setup redis for uri %s, err: %s ", uri, err)
		return err
	}
	defer c.Close()

	err = setupKeys(t, c, dbNumStr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func setupDBKeysCluster(t *testing.T, uri string) error {
	e := Exporter{redisAddr: uri}
	c, err := e.connectToRedisCluster()
	if err != nil {
		return err
	}

	defer c.Close()

	err = setupKeys(t, c, "0")
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

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

	deleteKeys(c, dbNumStr)

	return nil
}

func deleteKeysFromDBCluster(addr string) error {
	e := Exporter{redisAddr: addr}
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

	for _, n := range []string{"john", "paul", "ringo", "george"} {
		keys = append(keys, fmt.Sprintf("key_%s_%d", n, ts))
	}

	singleStringKey = fmt.Sprintf("key_string_%d", ts)

	listKeys = append(listKeys, "beatles_list")

	for _, n := range []string{"A.J.", "Howie", "Nick", "Kevin", "Brian"} {
		keysExpiring = append(keysExpiring, fmt.Sprintf("key_exp_%s_%d", n, ts))
	}
}
