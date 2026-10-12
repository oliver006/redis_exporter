package exporter

import (
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var slotStatsMetricNames = []string{
	"cluster_slot_key_count",
	"cluster_slot_cpu_usec_total",
	"cluster_slot_network_bytes_in_total",
	"cluster_slot_network_bytes_out_total",
}

func newSlotStatsTestExporter() *Exporter {
	e := &Exporter{
		options:            Options{Namespace: "test"},
		metricDescriptions: map[string]*prometheus.Desc{},
	}
	for _, m := range slotStatsMetricNames {
		e.metricDescriptions[m] = newMetricDescr("test", m, m, []string{"slot"})
	}
	return e
}

var fqNameRE = regexp.MustCompile(`fqName: "test_([^"]+)"`)

// collectSlotStats drains ch and returns "<metric>{slot}" -> value, plus whether each metric was a counter.
func collectSlotStats(t *testing.T, ch chan prometheus.Metric) (values map[string]float64, counters map[string]bool) {
	t.Helper()
	close(ch)
	values = map[string]float64{}
	counters = map[string]bool{}
	for m := range ch {
		d := &dto.Metric{}
		if err := m.Write(d); err != nil {
			t.Fatalf("metric.Write: %v", err)
		}
		sub := fqNameRE.FindStringSubmatch(m.Desc().String())
		if sub == nil {
			t.Fatalf("cannot read the metric name from %s", m.Desc())
		}
		if len(d.GetLabel()) != 1 || d.GetLabel()[0].GetName() != "slot" {
			t.Fatalf("%s: want exactly one label, slot; got %v", sub[1], d.GetLabel())
		}
		key := sub[1] + "{" + d.GetLabel()[0].GetValue() + "}"
		counters[key] = d.GetCounter() != nil
		values[key] = d.GetGauge().GetValue() + d.GetCounter().GetValue()
	}
	return values, counters
}

func TestRegisterClusterSlotStats(t *testing.T) {
	e := newSlotStatsTestExporter()

	reply := []interface{}{
		[]interface{}{int64(5), []interface{}{
			[]byte("key-count"), int64(3),
			[]byte("cpu-usec"), int64(100),
			[]byte("network-bytes-in"), int64(200),
			[]byte("network-bytes-out"), int64(300),
		}},
	}

	ch := make(chan prometheus.Metric, 10)
	e.registerClusterSlotStats(ch, reply)
	got, counters := collectSlotStats(t, ch)

	want := map[string]float64{
		"cluster_slot_key_count{5}":               3,
		"cluster_slot_cpu_usec_total{5}":          100,
		"cluster_slot_network_bytes_in_total{5}":  200,
		"cluster_slot_network_bytes_out_total{5}": 300,
	}
	if len(got) != len(want) {
		t.Errorf("expected %d metrics, got %d: %v", len(want), len(got), got)
	}
	for k, w := range want {
		if v, ok := got[k]; !ok || v != w {
			t.Errorf("%s: got %v (present: %v) want %v", k, v, ok, w)
		}
		// the key count can go down, the others only ever go up
		if isCounter := counters[k]; isCounter != (k != "cluster_slot_key_count{5}") {
			t.Errorf("%s: counter=%v", k, isCounter)
		}
	}
}

// Malformed entries must be skipped without stopping the scrape: every case is followed by a
// good entry (slot 9) that has to come through.
func TestRegisterClusterSlotStatsSkipsMalformedReplies(t *testing.T) {
	good := []interface{}{int64(9), []interface{}{[]byte("key-count"), int64(1)}}

	for name, bad := range map[string]interface{}{
		"entry is not a list":         int64(7),
		"entry without stats":         []interface{}{int64(1)},
		"slot is not a number":        []interface{}{[]byte("x"), []interface{}{[]byte("key-count"), int64(1)}},
		"stats is not a list":         []interface{}{int64(2), int64(1)},
		"stat name is not a string":   []interface{}{int64(3), []interface{}{int64(5), int64(1)}},
		"stat value is not a number":  []interface{}{int64(4), []interface{}{[]byte("key-count"), []byte("abc")}},
		"unknown stat is ignored":     []interface{}{int64(6), []interface{}{[]byte("some-future-stat"), int64(1)}},
		"trailing name without value": []interface{}{int64(8), []interface{}{[]byte("key-count")}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newSlotStatsTestExporter()
			ch := make(chan prometheus.Metric, 10)
			e.registerClusterSlotStats(ch, []interface{}{bad, good})
			got, _ := collectSlotStats(t, ch)

			if len(got) != 1 || got["cluster_slot_key_count{9}"] != 1 {
				t.Errorf("want only the good entry (slot 9), got %v", got)
			}
		})
	}
}

// Servers that cannot answer CLUSTER SLOT-STATS must give no metrics and no failure:
// Redis and Valkey < 8 do not know the subcommand, a Valkey 8+ without cluster mode refuses it.
func TestExtractClusterSlotStatsMetricsUnsupportedServer(t *testing.T) {
	for _, tc := range []struct{ env, why string }{
		{"TEST_REDIS7_URI", "unknown subcommand"},
		{"TEST_VALKEY9_URI", "cluster support disabled"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			uri := os.Getenv(tc.env)
			if uri == "" {
				t.Skipf("%s not set - skipping", tc.env)
			}
			c, err := redis.DialURL(strings.Replace(uri, "valkey://", "redis://", 1))
			if err != nil {
				t.Fatalf("dial %s: %v", uri, err)
			}
			defer c.Close()

			e := newSlotStatsTestExporter()
			ch := make(chan prometheus.Metric, 10)
			e.extractClusterSlotStatsMetrics(ch, c)
			if got, _ := collectSlotStats(t, ch); len(got) != 0 {
				t.Errorf("expected no metrics from %s, got %v", tc.env, got)
			}
		})
	}
}

func TestClusterSlotStatsScrape(t *testing.T) {
	uri := os.Getenv("TEST_VALKEY_CLUSTER_MASTER_URI")
	if uri == "" {
		t.Skipf("TEST_VALKEY_CLUSTER_MASTER_URI not set - skipping")
	}
	c, err := redis.DialURL(uri)
	if err != nil {
		t.Fatalf("dial %s: %v", uri, err)
	}
	defer c.Close()

	// leave the server setting the way we found it
	cfg, err := redis.Strings(c.Do("CONFIG", "GET", "cluster-slot-stats-enabled"))
	if err != nil || len(cfg) != 2 {
		t.Skipf("server does not have cluster-slot-stats-enabled (%v, %v) - skipping", cfg, err)
	}
	defer c.Do("CONFIG", "SET", "cluster-slot-stats-enabled", cfg[1])

	// a key that lives in a slot this node owns, so that the key count can be checked against the server
	owned := map[int]bool{}
	stats, err := redis.Values(c.Do("CLUSTER", "SLOT-STATS", "SLOTSRANGE", "0", "16383"))
	if err != nil {
		t.Fatalf("CLUSTER SLOT-STATS: %v", err)
	}
	for _, s := range stats {
		entry, _ := redis.Values(s, nil)
		if slot, err := redis.Int(entry[0], nil); err == nil {
			owned[slot] = true
		}
	}
	if len(owned) == 0 || len(owned) == 16384 {
		t.Fatalf("expected a node that owns some of the slots, owns %d", len(owned))
	}
	var key string
	var slot int
	for i := 0; ; i++ {
		key = fmt.Sprintf("test-slot-stats-%d", i)
		if slot, err = redis.Int(c.Do("CLUSTER", "KEYSLOT", key)); err != nil {
			t.Fatalf("CLUSTER KEYSLOT: %v", err)
		}
		if owned[slot] {
			break
		}
	}
	if _, err := c.Do("SET", key, "1"); err != nil {
		t.Fatalf("SET %s: %v", key, err)
	}
	defer c.Do("DEL", key)
	wantKeys, err := redis.Int(c.Do("CLUSTER", "COUNTKEYSINSLOT", slot))
	if err != nil || wantKeys < 1 {
		t.Fatalf("COUNTKEYSINSLOT %d: %d, %v", slot, wantKeys, err)
	}
	slotLabel := strconv.Itoa(slot)
	keyCountLine := fmt.Sprintf(`test_cluster_slot_key_count{slot="%s"} %d`, slotLabel, wantKeys)

	scrape := func(incl bool) string {
		e, _ := NewRedisExporter(uri, Options{Namespace: "test", InclClusterSlotStats: incl})
		ts := httptest.NewServer(e)
		defer ts.Close()
		return downloadURL(t, ts.URL+"/metrics")
	}

	t.Run("off by default", func(t *testing.T) {
		if body := scrape(false); strings.Contains(body, "test_cluster_slot_") {
			t.Errorf("per-slot metrics exported without the option")
		}
	})

	t.Run("key count only while the server does not collect slot stats", func(t *testing.T) {
		if _, err := c.Do("CONFIG", "SET", "cluster-slot-stats-enabled", "no"); err != nil {
			t.Fatal(err)
		}
		body := scrape(true)
		if !strings.Contains(body, keyCountLine) {
			t.Errorf("missing %s", keyCountLine)
		}
		for _, absent := range []string{"cluster_slot_cpu_usec_total", "cluster_slot_network_bytes_in_total", "cluster_slot_network_bytes_out_total"} {
			if strings.Contains(body, "test_"+absent) {
				t.Errorf("%s exported although the server does not report it", absent)
			}
		}
		if n := strings.Count(body, "\ntest_cluster_slot_key_count{"); n != len(owned) {
			t.Errorf("expected one key count per owned slot (%d), got %d", len(owned), n)
		}
	})

	t.Run("all four with cluster-slot-stats-enabled", func(t *testing.T) {
		if _, err := c.Do("CONFIG", "SET", "cluster-slot-stats-enabled", "yes"); err != nil {
			t.Fatal(err)
		}
		body := scrape(true)
		for _, want := range []string{
			keyCountLine,
			fmt.Sprintf(`test_cluster_slot_cpu_usec_total{slot="%s"} `, slotLabel),
			fmt.Sprintf(`test_cluster_slot_network_bytes_in_total{slot="%s"} `, slotLabel),
			fmt.Sprintf(`test_cluster_slot_network_bytes_out_total{slot="%s"} `, slotLabel),
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %s", want)
			}
		}
	})
}
