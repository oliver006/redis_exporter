package exporter

import (
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func TestKeyspaceStringParser(t *testing.T) {
	tsts := []struct {
		db                                    string
		stats                                 string
		keysTotal, keysEx, keysCached, avgTTL float64
		ok                                    bool
	}{
		{db: "xxx", stats: "", ok: false},
		{db: "xxx", stats: "keys=1,expires=0,avg_ttl=0", ok: false},
		{db: "db0", stats: "xxx", ok: false},
		{db: "db1", stats: "keys=abcd,expires=0,avg_ttl=0", ok: false},
		{db: "db2", stats: "keys=1234=1234,expires=0,avg_ttl=0", ok: false},

		{db: "db3", stats: "keys=abcde,expires=0", ok: false},
		{db: "db3", stats: "keys=213,expires=xxx", ok: false},
		{db: "db3", stats: "keys=123,expires=0,avg_ttl=zzz", ok: false},
		{db: "db3", stats: "keys=1,expires=0,avg_ttl=zzz,cached_keys=0", ok: false},
		{db: "db3", stats: "keys=1,expires=0,avg_ttl=0,cached_keys=zzz", ok: false},
		{db: "db3", stats: "keys=1,expires=0,avg_ttl=0,cached_keys=0,extra=0", ok: false},

		{db: "db0", stats: "keys=1,expires=0,avg_ttl=0", keysTotal: 1, keysEx: 0, avgTTL: 0, keysCached: -1, ok: true},
		{db: "db0", stats: "keys=1,expires=0,avg_ttl=0,cached_keys=0", keysTotal: 1, keysEx: 0, avgTTL: 0, keysCached: 0, ok: true},
	}

	for _, tst := range tsts {
		if kt, kx, ttl, kc, ok := parseDBKeyspaceString(tst.db, tst.stats); true {

			if ok != tst.ok {
				t.Errorf("failed for: db:%s stats:%s", tst.db, tst.stats)
				continue
			}

			if ok && (kt != tst.keysTotal || kx != tst.keysEx || kc != tst.keysCached || ttl != tst.avgTTL) {
				t.Errorf("values not matching, db:%s stats:%s   %f %f %f %f", tst.db, tst.stats, kt, kx, kc, ttl)
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
	redisSixTwoAddr := os.Getenv("TEST_REDIS6_URI")
	e := getTestExporterWithAddr(defaultAddr)
	setupDBKeys(t, defaultAddr)

	want := map[string]bool{"test_commands_duration_seconds_total": false, "test_commands_total": false}
	commandStatsCheck(t, e, want)
	deleteKeysFromDB(t, defaultAddr)

	// Since Redis v6.2 we should expect extra failed calls and rejected calls
	e = getTestExporterWithAddr(redisSixTwoAddr)
	setupDBKeys(t, redisSixTwoAddr)

	want = map[string]bool{"test_commands_duration_seconds_total": false, "test_commands_total": false, "commands_failed_calls_total": false, "commands_rejected_calls_total": false, "errors_total": false}
	commandStatsCheck(t, e, want)
	deleteKeysFromDB(t, redisSixTwoAddr)
}

func TestLatencyStats(t *testing.T) {
	redisSevenAddr := os.Getenv("TEST_REDIS7_URI")

	// Since Redis v7 we should have extended latency stats (summary of command latencies)
	e := getTestExporterWithAddr(redisSevenAddr)
	setupDBKeys(t, redisSevenAddr)

	want := map[string]bool{"latency_percentiles_usec": false}
	commandStatsCheck(t, e, want)
	deleteKeysFromDB(t, redisSevenAddr)
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
