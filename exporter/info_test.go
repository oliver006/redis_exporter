package exporter

import (
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func TestKeyspaceStringParser(t *testing.T) {
	tsts := []struct {
		name    string
		db      string
		stats   string
		metrics []dbKeyspaceMetric
		ok      bool
	}{
		{
			name: "wrong key prefix", db: "xxx", stats: "keys=1,expires=0,avg_ttl=0", ok: false,
		},
		{name: "malformed", db: "db0", stats: "xxx", ok: false},
		{name: "invalid keys", db: "db1", stats: "keys=abcd,expires=0,avg_ttl=0", ok: false},
		{name: "invalid field", db: "db2", stats: "keys=1234=1234,expires=0,avg_ttl=0", ok: false},
		{name: "missing keys", db: "db3", stats: "expires=0,avg_ttl=0", ok: false},
		{name: "missing expires", db: "db3", stats: "keys=213,avg_ttl=0", ok: false},
		{name: "invalid expires", db: "db3", stats: "keys=213,expires=xxx", ok: false},
		{name: "invalid average ttl", db: "db3", stats: "keys=123,expires=0,avg_ttl=zzz", ok: false},
		{name: "invalid cached keys", db: "db3", stats: "keys=1,expires=0,avg_ttl=0,cached_keys=zzz", ok: false},
		{
			name: "redis without subexpiry", db: "db0", stats: "keys=1,expires=0,avg_ttl=2000",
			metrics: []dbKeyspaceMetric{{name: "db_keys", value: 1}, {name: "db_keys_expiring", value: 0}, {name: "db_avg_ttl_seconds", value: 2}}, ok: true,
		},
		{
			name: "redis subexpiry", db: "db0", stats: "subexpiry=7,avg_ttl=685620459,expires=25091314,keys=25714011",
			metrics: []dbKeyspaceMetric{{name: "db_keys", value: 25714011}, {name: "db_keys_expiring", value: 25091314}, {name: "db_avg_ttl_seconds", value: 685620.459}, {name: "db_keys_with_expiring_items", value: 7}}, ok: true,
		},
		{
			name: "valkey volatile items", db: "db0", stats: "keys=17,expires=5,avg_ttl=2500,keys_with_volatile_items=3",
			metrics: []dbKeyspaceMetric{{name: "db_keys", value: 17}, {name: "db_keys_expiring", value: 5}, {name: "db_avg_ttl_seconds", value: 2.5}, {name: "db_keys_with_expiring_items", value: 3}}, ok: true,
		},
		{
			name: "cached keys compatibility", db: "db0", stats: "keys=1,expires=0,avg_ttl=0,cached_keys=4,extra=ignored",
			metrics: []dbKeyspaceMetric{{name: "db_keys", value: 1}, {name: "db_keys_expiring", value: 0}, {name: "db_avg_ttl_seconds", value: 0}, {name: "db_keys_cached", value: 4}}, ok: true,
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			metrics, ok := parseDBKeyspaceString(tst.db, tst.stats)
			if ok != tst.ok {
				t.Fatalf("parseDBKeyspaceString(%q, %q) ok = %t, want %t", tst.db, tst.stats, ok, tst.ok)
			}

			if ok && !reflect.DeepEqual(metrics, tst.metrics) {
				t.Errorf("parseDBKeyspaceString(%q, %q) metrics = %#v, want %#v", tst.db, tst.stats, metrics, tst.metrics)
			}
		})
	}
}

func TestExtractInfoKeyspaceMetrics(t *testing.T) {
	tests := []struct {
		name     string
		keyspace string
		want     []string
		dontWant []string
	}{
		{
			name: "redis", keyspace: "db0:keys=5,expires=3,avg_ttl=1000,subexpiry=2",
			want:     []string{"test_db_keys", "test_db_keys_expiring", "test_db_avg_ttl_seconds", "test_db_keys_with_expiring_items"},
			dontWant: []string{"test_db_keys_cached"},
		},
		{
			name: "valkey", keyspace: "db0:keys=5,expires=3,avg_ttl=1000,keys_with_volatile_items=2",
			want:     []string{"test_db_keys", "test_db_keys_expiring", "test_db_avg_ttl_seconds", "test_db_keys_with_expiring_items"},
			dontWant: []string{"test_db_keys_cached"},
		},
		{
			name: "cached keys compatibility", keyspace: "db0:keys=5,expires=3,avg_ttl=1000,cached_keys=2",
			want:     []string{"test_db_keys", "test_db_keys_expiring", "test_db_avg_ttl_seconds", "test_db_keys_cached"},
			dontWant: []string{"test_db_keys_with_expiring_items"},
		},
	}

	for _, tst := range tests {
		t.Run(tst.name, func(t *testing.T) {
			e, err := NewRedisExporter("unix:///tmp/doesnt.matter", Options{Namespace: "test"})
			if err != nil {
				t.Fatalf("NewRedisExporter() error = %v", err)
			}

			ch := make(chan prometheus.Metric)
			go func() {
				e.extractInfoMetrics(ch, "# Keyspace\n"+tst.keyspace+"\n", 0)
				close(ch)
			}()

			descriptions := ""
			for metric := range ch {
				descriptions += metric.Desc().String() + "\n"
			}
			for _, metric := range tst.want {
				if !strings.Contains(descriptions, `fqName: "`+metric+`"`) {
					t.Errorf("missing metric %s", metric)
				}
			}
			for _, metric := range tst.dontWant {
				if strings.Contains(descriptions, `fqName: "`+metric+`"`) {
					t.Errorf("unexpected metric %s", metric)
				}
			}
		})
	}
}

func TestLiveKeyspaceExpiringItemsMetric(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		sourceField string
	}{
		{name: "redis", env: "TEST_REDIS88_URI", sourceField: "subexpiry"},
		{name: "valkey", env: "TEST_VALKEY9_URI", sourceField: "keys_with_volatile_items"},
	}

	for _, tst := range tests {
		t.Run(tst.name, func(t *testing.T) {
			addr := os.Getenv(tst.env)
			if addr == "" {
				t.Skipf("%s not set - skipping", tst.env)
			}

			e, err := NewRedisExporter(addr, Options{Namespace: "test"})
			if err != nil {
				t.Fatalf("NewRedisExporter() error = %v", err)
			}
			c, err := e.connectToRedis()
			if err != nil {
				t.Fatalf("connectToRedis() error = %v", err)
			}
			defer c.Close()

			const db = "15"
			if _, err := c.Do("SELECT", db); err != nil {
				t.Fatalf("SELECT %s error = %v", db, err)
			}
			if _, err := c.Do("FLUSHDB"); err != nil {
				t.Fatalf("FLUSHDB error = %v", err)
			}
			defer c.Do("FLUSHDB")

			if _, err := c.Do("HSET", "expiring-items-test", "field", "value"); err != nil {
				t.Fatalf("HSET error = %v", err)
			}
			result, err := redis.Ints(c.Do("HEXPIRE", "expiring-items-test", 600, "FIELDS", 1, "field"))
			if err != nil {
				t.Fatalf("HEXPIRE error = %v", err)
			}
			if !reflect.DeepEqual(result, []int{1}) {
				t.Fatalf("HEXPIRE result = %v, want [1]", result)
			}

			info, err := redis.String(c.Do("INFO", "KEYSPACE"))
			if err != nil {
				t.Fatalf("INFO KEYSPACE error = %v", err)
			}
			if !strings.Contains(info, tst.sourceField+"=1") {
				t.Fatalf("INFO KEYSPACE missing %s=1:\n%s", tst.sourceField, info)
			}

			ts := httptest.NewServer(e)
			defer ts.Close()
			body := downloadURL(t, ts.URL+"/metrics")
			if want := `test_db_keys_with_expiring_items{db="db15"} 1`; !strings.Contains(body, want) {
				t.Errorf("missing metric %q:\n%s", want, body)
			}
			if strings.Contains(body, `test_db_keys_cached{db="db15"}`) {
				t.Errorf("unexpected cached-keys metric for db15:\n%s", body)
			}
		})
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
		t.Run(fmt.Sprintf("%s---%s", tst.k, tst.v), func(t *testing.T) {
			offset, ip, port, state, lag, ok := parseConnectedSlaveString(tst.k, tst.v)

			if ok != tst.ok {
				t.Errorf("failed for: db:%s stats:%s", tst.k, tst.v)
				return
			}
			if offset != tst.offset || ip != tst.ip || port != tst.port || state != tst.state || lag != tst.lag {
				t.Errorf("values not matching, string:%s %f %s %s %s %f", tst.v, offset, ip, port, state, lag)
			}
		})
	}
}

func TestCommandStats(t *testing.T) {
	defaultAddr := os.Getenv("TEST_REDIS_URI")
	e := getTestExporterWithAddr(defaultAddr)
	setupTestKeys(t, defaultAddr)

	want := map[string]bool{"test_commands_duration_seconds_total": false, "test_commands_total": false}
	commandStatsCheck(t, e, want)
	deleteTestKeys(t, defaultAddr)

	redisSixTwoAddr := os.Getenv("TEST_REDIS6_URI")
	if redisSixTwoAddr != "" {
		// Since Redis v6.2 we should expect extra failed calls and rejected calls
		e = getTestExporterWithAddr(redisSixTwoAddr)
		setupTestKeys(t, redisSixTwoAddr)

		want = map[string]bool{"test_commands_duration_seconds_total": false, "test_commands_total": false, "commands_failed_calls_total": false, "commands_rejected_calls_total": false, "errors_total": false}
		commandStatsCheck(t, e, want)
		deleteTestKeys(t, redisSixTwoAddr)
	}
}

func TestValkeyClusterInfoMetricsNotDuplicated(t *testing.T) {
	e, err := NewRedisExporter("unix:///tmp/doesnt.matter", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	clusterInfo := "cluster_state:ok\r\ncluster_slots_assigned:16384\r\n"
	infoAll := "# Cluster\r\ncluster_enabled:1\r\n# Cluster Info\r\n" + clusterInfo

	ch := make(chan prometheus.Metric)
	go func() {
		e.extractClusterInfoMetrics(ch, clusterInfo)
		e.extractInfoMetrics(ch, infoAll, 1)
		close(ch)
	}()

	counts := map[string]int{
		"test_cluster_state":          0,
		"test_cluster_slots_assigned": 0,
	}
	for metric := range ch {
		desc := metric.Desc().String()
		for name := range counts {
			if strings.Contains(desc, `fqName: "`+name+`"`) {
				counts[name]++
			}
		}
	}

	for name, count := range counts {
		if count != 1 {
			t.Errorf("%s emitted %d times, want 1", name, count)
		}
	}
}

func commandStatsCheck(t *testing.T, e *Exporter, want map[string]bool) {
	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

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

func TestInclMetricsForEmptyDatabases(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_URI")
	if addr == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	for _, inclMetrics := range []bool{true, false} {
		t.Run(fmt.Sprintf("inclMetrics:%t", inclMetrics), func(t *testing.T) {
			e, _ := NewRedisExporter(addr,
				Options{
					Namespace:                    "test",
					InclMetricsForEmptyDatabases: inclMetrics,
				})
			ts := httptest.NewServer(e)
			defer ts.Close()

			body := downloadURL(t, ts.URL+"/metrics")
			if inclMetrics {
				if !strings.Contains(body, `test_db_keys{db="db10"} 0`) {
					t.Errorf("Expected to find test_db_keys")
				}
			} else {
				if strings.Contains(body, `test_db_keys{db="db10"} 0`) {
					t.Errorf("Expected to not find test_db_keys")
				}
			}
		})
	}
}

func TestClusterMaster(t *testing.T) {
	if os.Getenv("TEST_VALKEY_CLUSTER_MASTER_URI") == "" {
		t.Skipf("TEST_VALKEY_CLUSTER_MASTER_URI not set - skipping")
	}

	addr := os.Getenv("TEST_VALKEY_CLUSTER_MASTER_URI")
	e, _ := NewRedisExporter(addr, Options{Namespace: "test"})
	ts := httptest.NewServer(e)
	defer ts.Close()

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

func TestClusterSkipCheckKeysIfMaster(t *testing.T) {
	uriMaster := os.Getenv("TEST_VALKEY_CLUSTER_MASTER_URI")
	uriSlave := os.Getenv("TEST_VALKEY_CLUSTER_SLAVE_URI")
	if uriMaster == "" || uriSlave == "" {
		t.Skipf("TEST_VALKEY_CLUSTER_MASTER_URI or slave not set - skipping")
	}

	setupTestKeysCluster(t, uriMaster)
	defer deleteTestKeysCluster(t, uriMaster)

	for _, uri := range []string{uriMaster, uriSlave} {
		for _, skip := range []bool{true, false} {
			e, _ := NewRedisExporter(
				uri,
				Options{Namespace: "test",
					CheckKeys:                  TestKeyNameHll,
					SkipCheckKeysForRoleMaster: skip,
					IsCluster:                  true,
				})
			ts := httptest.NewServer(e)

			body := downloadURL(t, ts.URL+"/metrics")

			expectedMetricPresent := true
			if skip && uri == uriMaster {
				expectedMetricPresent = false
			}
			t.Logf("skip: %#v  uri: %s    uri == uriMaster: %#v", skip, uri, uri == uriMaster)
			t.Logf("expectedMetricPresent: %#v", expectedMetricPresent)

			want := `test_key_size{db="db0",key="test-hll"} 3`

			if expectedMetricPresent {
				if !strings.Contains(body, want) {
					t.Fatalf("expectedMetricPresent but missing. metric: %s   body: %s\n", want, body)
				}
			} else {
				if strings.Contains(body, want) {
					t.Fatalf("should have skipped it but found it, body:\n%s", body)
				}
			}

			ts.Close()
		}
	}
}

func TestClusterSlave(t *testing.T) {
	if os.Getenv("TEST_VALKEY_CLUSTER_SLAVE_URI") == "" {
		t.Skipf("TEST_VALKEY_CLUSTER_SLAVE_URI not set - skipping")
	}

	addr := os.Getenv("TEST_VALKEY_CLUSTER_SLAVE_URI")
	e, _ := NewRedisExporter(addr, Options{Namespace: "test"})
	ts := httptest.NewServer(e)
	defer ts.Close()

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
	masterHost := hostReg.FindString(body)
	portReg, _ := regexp.Compile(`master_port="(\d+)"`)
	masterPort := portReg.FindString(body)
	for wantedKey, wantedVal := range map[string]int{
		masterHost: 5,
		masterPort: 5,
	} {
		if res := strings.Count(body, wantedKey); res != wantedVal {
			t.Errorf("Result: %s -> %d, Wanted: %d \nbody: %s", wantedKey, res, wantedVal, body)
		}
	}
}

func TestParseCommandStats(t *testing.T) {

	for _, tst := range []struct {
		fieldKey   string
		fieldValue string

		wantSuccess       bool
		wantExtraStats    bool
		wantCmd           string
		wantCalls         float64
		wantRejectedCalls float64
		wantFailedCalls   float64
		wantUsecTotal     float64
	}{
		{
			fieldKey:      "cmdstat_get",
			fieldValue:    "calls=21,usec=175,usec_per_call=8.33",
			wantSuccess:   true,
			wantCmd:       "get",
			wantCalls:     21,
			wantUsecTotal: 175,
		},
		{
			fieldKey:      "cmdstat_georadius_ro",
			fieldValue:    "calls=75,usec=1260,usec_per_call=16.80",
			wantSuccess:   true,
			wantCmd:       "georadius_ro",
			wantCalls:     75,
			wantUsecTotal: 1260,
		},
		{
			fieldKey:    "borked_stats",
			fieldValue:  "calls=75,usec=1260,usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "borked_values",
			wantSuccess: false,
		},

		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=ABC,usec=1260,usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=75,usec=DEF,usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:          "cmdstat_georadius_ro",
			fieldValue:        "calls=75,usec=1024,usec_per_call=16.80,rejected_calls=5,failed_calls=10",
			wantCmd:           "georadius_ro",
			wantCalls:         75,
			wantUsecTotal:     1024,
			wantSuccess:       true,
			wantExtraStats:    true,
			wantFailedCalls:   10,
			wantRejectedCalls: 5,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=75,usec=1024,usec_per_call=16.80,rejected_calls=ABC,failed_calls=10",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=75,usec=1024,usec_per_call=16.80,rejected_calls=5,failed_calls=ABC",
			wantSuccess: false,
		},
	} {
		t.Run(tst.fieldKey+tst.fieldValue, func(t *testing.T) {

			cmd, calls, rejectedCalls, failedCalls, usecTotal, _, err := parseMetricsCommandStats(tst.fieldKey, tst.fieldValue)

			if tst.wantSuccess && err != nil {
				t.Fatalf("err: %s", err)
				return
			}

			if !tst.wantSuccess && err == nil {
				t.Fatalf("expected err!")
				return
			}

			if !tst.wantSuccess {
				return
			}

			if cmd != tst.wantCmd {
				t.Fatalf("cmd not matching, got: %s, wanted: %s", cmd, tst.wantCmd)
			}

			if calls != tst.wantCalls {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", calls, tst.wantCalls)
			}
			if rejectedCalls != tst.wantRejectedCalls {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", rejectedCalls, tst.wantRejectedCalls)
			}
			if failedCalls != tst.wantFailedCalls {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", failedCalls, tst.wantFailedCalls)
			}
			if usecTotal != tst.wantUsecTotal {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", usecTotal, tst.wantUsecTotal)
			}
		})
	}

}

func TestParseErrorStats(t *testing.T) {

	for _, tst := range []struct {
		fieldKey   string
		fieldValue string

		wantSuccess     bool
		wantErrorPrefix string
		wantCount       float64
	}{
		{
			fieldKey:        "errorstat_ERR",
			fieldValue:      "count=4",
			wantSuccess:     true,
			wantErrorPrefix: "ERR",
			wantCount:       4,
		},
		{
			fieldKey:    "borked_stats",
			fieldValue:  "count=4",
			wantSuccess: false,
		},
		{
			fieldKey:    "errorstat_ERR",
			fieldValue:  "borked_values",
			wantSuccess: false,
		},

		{
			fieldKey:    "errorstat_ERR",
			fieldValue:  "count=ABC",
			wantSuccess: false,
		},
	} {
		t.Run(tst.fieldKey+tst.fieldValue, func(t *testing.T) {

			errorPrefix, count, err := parseMetricsErrorStats(tst.fieldKey, tst.fieldValue)

			if tst.wantSuccess && err != nil {
				t.Fatalf("err: %s", err)
				return
			}

			if !tst.wantSuccess && err == nil {
				t.Fatalf("expected err!")
				return
			}

			if !tst.wantSuccess {
				return
			}

			if errorPrefix != tst.wantErrorPrefix {
				t.Fatalf("cmd not matching, got: %s, wanted: %s", errorPrefix, tst.wantErrorPrefix)
			}

			if count != tst.wantCount {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", count, tst.wantCount)
			}
		})
	}

}

func Test_parseMetricsLatencyStats(t *testing.T) {
	type args struct {
		fieldKey   string
		fieldValue string
	}
	tests := []struct {
		name              string
		args              args
		wantCmd           string
		wantPercentileMap map[float64]float64
		wantErr           bool
	}{
		{
			name:              "simple",
			args:              args{fieldKey: "latency_percentiles_usec_ping", fieldValue: "p50=0.001,p99=1.003,p99.9=3.007"},
			wantCmd:           "ping",
			wantPercentileMap: map[float64]float64{50.0: 0.001, 99.0: 1.003, 99.9: 3.007},
			wantErr:           false,
		},
		{
			name:              "single-percentile",
			args:              args{fieldKey: "latency_percentiles_usec_ping", fieldValue: "p50=0.001"},
			wantCmd:           "ping",
			wantPercentileMap: map[float64]float64{50.0: 0.001},
			wantErr:           false,
		},
		{
			name:              "empty",
			args:              args{fieldKey: "latency_percentiles_usec_ping", fieldValue: ""},
			wantCmd:           "ping",
			wantPercentileMap: map[float64]float64{0: 0},
			wantErr:           false,
		},
		{
			name:              "invalid-percentile",
			args:              args{fieldKey: "latency_percentiles_usec_ping", fieldValue: "p50=a"},
			wantCmd:           "ping",
			wantPercentileMap: map[float64]float64{},
			wantErr:           true,
		},
		{
			name:              "invalid prefix",
			args:              args{fieldKey: "wrong_prefix_", fieldValue: "p50=0.001,p99=1.003,p99.9=3.007"},
			wantCmd:           "",
			wantPercentileMap: map[float64]float64{},
			wantErr:           true,
		},
		{
			name:              "empty-percentile-key",
			args:              args{fieldKey: "latency_percentiles_usec_ping", fieldValue: "=1.0,p99=2.0"},
			wantCmd:           "ping",
			wantPercentileMap: map[float64]float64{},
			wantErr:           true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotPercentileMap, err := parseMetricsLatencyStats(tt.args.fieldKey, tt.args.fieldValue)
			if (err != nil) != tt.wantErr {
				t.Errorf("test %s. parseMetricsLatencyStats() error = %v, wantErr %v", tt.name, err, tt.wantErr)
				return
			}
			if gotCmd != tt.wantCmd {
				t.Errorf("parseMetricsLatencyStats() gotCmd = %v, want %v", gotCmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(gotPercentileMap, tt.wantPercentileMap) {
				t.Errorf("parseMetricsLatencyStats() gotPercentileMap = %v, want %v", gotPercentileMap, tt.wantPercentileMap)
			}
		})
	}
}

// instance_info must survive a Valkey 8.0 -> 8.1 upgrade that adds the new
// valkey_release_stage INFO field between scrapes, without a restart.
func TestInstanceInfoLabelsChangeBetweenScrapes(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}
	// force the lazy init of metricDescriptionLabels on the first scrape
	e.metricDescriptionLabels = nil

	// Valkey 8.0 has no valkey_release_stage field
	infoBefore := "# Server\r\nredis_version:7.2.5\r\nredis_mode:standalone\r\nrun_id:abc\r\nvalkey_version:8.0.0\r\n"
	// Valkey 8.1 added valkey_release_stage
	infoAfter := "# Server\r\nredis_version:7.2.5\r\nredis_mode:standalone\r\nrun_id:abc\r\nvalkey_version:8.1.0\r\nvalkey_release_stage:ga\r\n"

	descs := make([]string, 2)
	for i, info := range []string{infoBefore, infoAfter} {
		ch := make(chan prometheus.Metric)
		go func() {
			e.extractInfoMetrics(ch, info, 0)
			close(ch)
		}()

		for m := range ch {
			if strings.Contains(m.Desc().String(), "test_instance_info") {
				descs[i] = m.Desc().String()
			}
		}
		if descs[i] == "" {
			t.Fatalf("scrape %d: test_instance_info metric missing after the instance_info label set changed", i)
		}
	}

	// the description must be rebuilt with the new label set, not the stale cached one
	if strings.Contains(descs[0], "valkey_release_stage") {
		t.Errorf("first scrape unexpectedly exposed valkey_release_stage label:\n%s", descs[0])
	}
	if !strings.Contains(descs[1], "valkey_release_stage") {
		t.Errorf("second scrape missing new valkey_release_stage label after the upgrade:\n%s", descs[1])
	}

	// a metric with no explicit labels must always return the cached description
	noLabel := e.createMetricDescription("uptime_in_seconds", nil)
	if noLabel != e.createMetricDescription("uptime_in_seconds", nil) {
		t.Errorf("expected cached description to be reused for a metric without labels")
	}
}
