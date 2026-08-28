package exporter

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
)

type sentinelConfigConn struct {
	reply any
}

func (c *sentinelConfigConn) Close() error { return nil }
func (c *sentinelConfigConn) Err() error   { return nil }
func (c *sentinelConfigConn) Do(string, ...any) (any, error) {
	return c.reply, nil
}
func (c *sentinelConfigConn) Send(string, ...any) error { return nil }
func (c *sentinelConfigConn) Flush() error              { return nil }
func (c *sentinelConfigConn) Receive() (any, error)     { return nil, nil }

func sentinelConfigReply() []any {
	return []any{
		[]byte("sentinel-pass"), []byte("application-secret-canary"),
		[]byte("resolve-hostnames"), []byte("yes"),
	}
}

func collectSentinelConfigKeyValues(t *testing.T, redact bool) []*dto.Metric {
	t.Helper()

	e, err := NewRedisExporter("", Options{
		Namespace:           "test",
		InclConfigMetrics:   true,
		RedactConfigMetrics: redact,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter: %v", err)
	}

	ch := make(chan prometheus.Metric, 8)
	e.extractSentinelConfig(ch, &sentinelConfigConn{reply: sentinelConfigReply()})
	close(ch)

	var metrics []*dto.Metric
	for metric := range ch {
		if !strings.Contains(metric.Desc().String(), "sentinel_config_key_value") {
			continue
		}
		got := &dto.Metric{}
		if err := metric.Write(got); err != nil {
			t.Fatalf("metric.Write: %v", err)
		}
		metrics = append(metrics, got)
	}
	return metrics
}

func hasSentinelConfigKeyValue(metrics []*dto.Metric, key, value string) bool {
	for _, metric := range metrics {
		labels := make(map[string]string, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["key"] == key && labels["value"] == value {
			return true
		}
	}
	return false
}

func TestSensitiveConfigKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{key: "masterauth", want: true},
		{key: "requirepass", want: true},
		{key: "tls-key-file-pass", want: true},
		{key: "tls-client-key-file-pass", want: true},
		{key: "SENTINEL-PASS", want: true},
		{key: "service-password-file", want: true},
		{key: "resolve-hostnames", want: false},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			if got := isSensitiveConfigKey(test.key); got != test.want {
				t.Fatalf("isSensitiveConfigKey(%q) = %t, want %t", test.key, got, test.want)
			}
		})
	}
}

func TestSentinelConfigRedaction(t *testing.T) {
	for _, test := range []struct {
		name       string
		redact     bool
		wantSecret bool
	}{
		{name: "enabled", redact: true, wantSecret: false},
		{name: "disabled", redact: false, wantSecret: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			metrics := collectSentinelConfigKeyValues(t, test.redact)
			gotSecret := hasSentinelConfigKeyValue(metrics, "sentinel-pass", "application-secret-canary")
			if gotSecret != test.wantSecret {
				t.Fatalf("sentinel-pass exported = %t, want %t", gotSecret, test.wantSecret)
			}
			if !hasSentinelConfigKeyValue(metrics, "resolve-hostnames", "yes") {
				t.Fatal("non-sensitive Sentinel config was not exported")
			}
		})
	}
}

func TestSentinelConfigDebugLogDoesNotContainValues(t *testing.T) {
	e, err := NewRedisExporter("", Options{
		Namespace:           "test",
		InclConfigMetrics:   true,
		RedactConfigMetrics: false,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter: %v", err)
	}

	logger := log.StandardLogger()
	originalOutput := logger.Out
	originalLevel := logger.GetLevel()
	t.Cleanup(func() {
		logger.SetOutput(originalOutput)
		logger.SetLevel(originalLevel)
	})

	var logs bytes.Buffer
	logger.SetOutput(&logs)
	logger.SetLevel(log.DebugLevel)

	ch := make(chan prometheus.Metric, 8)
	e.extractSentinelConfig(ch, &sentinelConfigConn{reply: sentinelConfigReply()})
	close(ch)

	for _, value := range []string{"application-secret-canary", "yes"} {
		if strings.Contains(logs.String(), value) {
			t.Fatalf("Sentinel config value %q leaked to debug logs: %s", value, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "Sentinel config contains 2 entries") {
		t.Fatalf("sanitized Sentinel config log missing: %s", logs.String())
	}
}

func TestSentinelExtractInfoMetrics(t *testing.T) {
	if os.Getenv("TEST_VALKEY_SENTINEL_URI") == "" {
		t.Skipf("TEST_VALKEY_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_VALKEY_SENTINEL_URI")
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

func TestSentinelParseSentinelMasterString(t *testing.T) {
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

func TestSentinelExtractSentinelMetricsForRedis(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

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

func TestSentinelExtractSentinelMetricsForSentinel(t *testing.T) {
	if os.Getenv("TEST_VALKEY_SENTINEL_URI") == "" {
		t.Skipf("TEST_VALKEY_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_VALKEY_SENTINEL_URI")
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
		"sentinel_master_ok_sentinels":                    false,
		"sentinel_master_ok_slaves":                       false,
		"sentinel_master_ckquorum_status":                 false,
		"sentinel_master_setting_ckquorum":                false,
		"sentinel_master_setting_failover_timeout":        false,
		"sentinel_master_setting_parallel_syncs":          false,
		"sentinel_master_setting_down_after_milliseconds": false,
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
	name                  string
	sentinelDetails       []any
	labels                []string
	expectedMetricValue   map[string]int
	expectedPeerInfoCount int
}

func TestSentinelProcessSentinels(t *testing.T) {
	if os.Getenv("TEST_VALKEY_SENTINEL_URI") == "" {
		t.Skipf("TEST_VALKEY_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_VALKEY_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test", InclSentinelPeerInfo: true},
	)

	oneOkSentinelExpectedMetricValue := map[string]int{
		"sentinel_master_ok_sentinels": 1,
	}
	twoOkSentinelExpectedMetricValue := map[string]int{
		"sentinel_master_ok_sentinels": 2,
	}
	tsts := []sentinelSentinelsData{
		{"1/1 okay sentinel", []any{[]any{[]byte("")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSentinelExpectedMetricValue, 0},
		{"1/3 okay sentinel", []any{[]any{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("o_down,s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []any{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), []byte("s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSentinelExpectedMetricValue, 2},
		{"2/3 okay sentinel(string is not byte slice)", []any{[]any{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []any{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), "sentinel", []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue, 1},
		{"2/3 okay sentinel", []any{[]any{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []any{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), []byte("s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue, 2},
		{"2/3 okay sentinel(missing flags)", []any{[]any{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []any{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue, 2},
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
				"sentinel_peer_info":           false,
			}
			peerInfoCount := 0

			for m := range chM {
				descStr := m.Desc().String()
				if strings.Contains(descStr, "sentinel_peer_info") {
					peerInfoCount++
					want["sentinel_peer_info"] = true
				}
				for k := range want {
					if k == "sentinel_peer_info" {
						continue
					}
					if strings.Contains(descStr, k) {
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
			if tst.expectedPeerInfoCount > 0 {
				if peerInfoCount != tst.expectedPeerInfoCount {
					t.Errorf("sentinel_peer_info: expected count %d, got %d", tst.expectedPeerInfoCount, peerInfoCount)
				}
			}
			for k, found := range want {
				if k == "sentinel_peer_info" && tst.expectedPeerInfoCount == 0 {
					continue
				}
				if !found {
					t.Errorf("didn't find metric %s", k)
				}
			}
		})
	}
}

type sentinelSlavesData struct {
	name                string
	slaveDetails        []any
	labels              []string
	expectedMetricValue map[string]int
}

// TestSentinelPeerInfoMetric verifies sentinel_peer_info is emitted with correct labels (no live Sentinel required).
func TestSentinelPeerInfoMetric(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:26379", Options{Namespace: "test", InclSentinelPeerInfo: true})
	if err != nil {
		t.Fatalf("NewRedisExporter: %v", err)
	}
	// One peer with all labels; one peer with missing "flags" (tests safe map access).
	sentinelDetails := []any{
		[]any{
			[]byte("name"), []byte("runid-peer1"),
			[]byte("ip"), []byte("10.0.0.1"),
			[]byte("port"), []byte("26379"),
			[]byte("runid"), []byte("runid-peer1"),
			[]byte("flags"), []byte("sentinel"),
		},
		[]any{
			[]byte("name"), []byte("runid-peer2"),
			[]byte("ip"), []byte("10.0.0.2"),
			[]byte("port"), []byte("26380"),
			[]byte("runid"), []byte("runid-peer2"),
			// no "flags" key - must not panic, label should be empty
		},
	}
	labels := []string{"mymaster", "127.0.0.1:6379"}

	chM := make(chan prometheus.Metric, 16)
	go func() {
		e.processSentinelSentinels(chM, sentinelDetails, labels...)
		close(chM)
	}()

	var peerInfoMetrics []*dto.Metric
	for m := range chM {
		if strings.Contains(m.Desc().String(), "sentinel_peer_info") {
			got := &dto.Metric{}
			_ = m.Write(got)
			peerInfoMetrics = append(peerInfoMetrics, got)
		}
	}

	if len(peerInfoMetrics) != 2 {
		t.Fatalf("expected 2 sentinel_peer_info metrics, got %d", len(peerInfoMetrics))
	}

	labelMap := func(metric *dto.Metric) map[string]string {
		out := make(map[string]string)
		for _, lp := range metric.GetLabel() {
			out[lp.GetName()] = lp.GetValue()
		}
		return out
	}

	// First peer: all labels set
	l0 := labelMap(peerInfoMetrics[0])
	if l0["master_name"] != "mymaster" || l0["master_address"] != "127.0.0.1:6379" {
		t.Errorf("first peer: wrong master labels: master_name=%q master_address=%q", l0["master_name"], l0["master_address"])
	}
	if l0["name"] != "runid-peer1" || l0["ip"] != "10.0.0.1" || l0["port"] != "26379" || l0["runid"] != "runid-peer1" || l0["flags"] != "sentinel" {
		t.Errorf("first peer: wrong peer labels: name=%q ip=%q port=%q runid=%q flags=%q", l0["name"], l0["ip"], l0["port"], l0["runid"], l0["flags"])
	}

	// Second peer: flags missing in input -> must be empty string (no panic)
	l1 := labelMap(peerInfoMetrics[1])
	if l1["name"] != "runid-peer2" || l1["ip"] != "10.0.0.2" || l1["port"] != "26380" || l1["runid"] != "runid-peer2" {
		t.Errorf("second peer: wrong labels: name=%q ip=%q port=%q runid=%q", l1["name"], l1["ip"], l1["port"], l1["runid"])
	}
	if l1["flags"] != "" {
		t.Errorf("second peer: expected empty flags when key missing, got %q", l1["flags"])
	}
}

func TestInclSentinelPeerInfoDisabled(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:26379", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter: %v", err)
	}
	sentinelDetails := []any{
		[]any{
			[]byte("name"), []byte("peer-a"),
			[]byte("ip"), []byte("10.0.0.1"),
			[]byte("port"), []byte("26379"),
			[]byte("runid"), []byte("rid-a"),
			[]byte("flags"), []byte("sentinel"),
		},
	}
	chM := make(chan prometheus.Metric, 8)
	go func() {
		e.processSentinelSentinels(chM, sentinelDetails, "mymaster", "127.0.0.1:6379")
		close(chM)
	}()
	var peerInfo, okSentinels int
	for m := range chM {
		ds := m.Desc().String()
		if strings.Contains(ds, "sentinel_peer_info") {
			peerInfo++
		}
		if strings.Contains(ds, "sentinel_master_ok_sentinels") {
			okSentinels++
		}
	}
	if peerInfo != 0 {
		t.Errorf("expected no sentinel_peer_info when InclSentinelPeerInfo is false, got %d", peerInfo)
	}
	if okSentinels != 1 {
		t.Errorf("expected sentinel_master_ok_sentinels, got count %d", okSentinels)
	}
}

func TestSentinelProcessSentinelsWithoutLabels(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:26379", Options{Namespace: "test", InclSentinelPeerInfo: true})
	if err != nil {
		t.Fatalf("NewRedisExporter: %v", err)
	}

	sentinelDetails := []any{
		[]any{
			[]byte("name"), []byte("peer-a"),
			[]byte("ip"), []byte("10.0.0.1"),
			[]byte("port"), []byte("26379"),
			[]byte("runid"), []byte("rid-a"),
			[]byte("flags"), []byte("sentinel"),
		},
	}

	chM := make(chan prometheus.Metric, 8)
	go func() {
		e.processSentinelSentinels(chM, sentinelDetails)
		close(chM)
	}()

	gotAny := false
	for range chM {
		gotAny = true
	}

	if gotAny {
		t.Errorf("expected no sentinel metrics when labels are missing")
	}
}

func TestSentinelProcessSlavesWithoutLabels(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:26379", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter: %v", err)
	}

	slaveDetails := []any{
		[]any{
			[]byte("name"), []byte("172.17.0.3:6379"),
			[]byte("ip"), []byte("172.17.0.3"),
			[]byte("port"), []byte("6379"),
			[]byte("runid"), []byte("rid-a"),
			[]byte("flags"), []byte("slave"),
		},
	}

	chM := make(chan prometheus.Metric, 8)
	go func() {
		e.processSentinelSlaves(chM, slaveDetails)
		close(chM)
	}()

	gotAny := false
	for range chM {
		gotAny = true
	}

	if gotAny {
		t.Errorf("expected no slave metrics when labels are missing")
	}
}

func TestSentinelProcessSlaves(t *testing.T) {
	if os.Getenv("TEST_VALKEY_SENTINEL_URI") == "" {
		t.Skipf("TEST_VALKEY_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_VALKEY_SENTINEL_URI")
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
		{"0/1 okay slave(string is not byte slice)", []any{[]any{[]string{"name"}, []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("490"), []byte("last-ping-reply"), []byte("490"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("2636"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("48279581"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("765829")}}, []string{"mymaster", "172.17.0.7:26379"}, zeroOkSlaveExpectedMetricValue},
		{"1/1 okay slave", []any{[]any{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("490"), []byte("last-ping-reply"), []byte("490"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("2636"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("48279581"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("765829")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSlaveExpectedMetricValue},
		{"1/3 okay slave", []any{[]any{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []any{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("s_down,slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []any{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("flags"), []byte("o_down,s_down,slave"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSlaveExpectedMetricValue},
		{"2/3 okay slave", []any{[]any{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []any{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []any{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("flags"), []byte("s_down,slave"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSlaveExpectedMetricValue},
		{"2/3 okay slave(missing flags)", []any{[]any{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []any{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []any{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSlaveExpectedMetricValue},
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

func TestSentinelScrapeRedisHostSentinelPath(t *testing.T) {
	if os.Getenv("TEST_VALKEY_SENTINEL_URI") == "" {
		t.Skipf("TEST_VALKEY_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_VALKEY_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)

	chM := make(chan prometheus.Metric, 1000)
	go func() {
		e.scrapeRedisHost(chM)
		close(chM)
	}()

	found := false
	for m := range chM {
		if strings.Contains(m.Desc().String(), "sentinel") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find sentinel metrics when scraping sentinel host via scrapeRedisHost()")
	}
}

func TestSentinelScrapeAllConfig(t *testing.T) {
	if os.Getenv("TEST_VALKEY_SENTINEL_URI") == "" {
		t.Skipf("TEST_VALKEY_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_VALKEY_SENTINEL_URI")
	for _, inc := range []bool{false, true} {
		e, _ := NewRedisExporter(
			addr,
			Options{Namespace: "test",
				InclConfigMetrics: inc,
			},
		)

		ts := httptest.NewServer(e)
		defer ts.Close()

		body := downloadURL(t, ts.URL+"/metrics")
		for _, want := range []string{
			"sentinel_config_key_value",
			"sentinel_config_value",
		} {
			if inc && !strings.Contains(body, want) {
				t.Fatalf("didn't find metrics with sentinel_config, want: %s, body: %s", want, body)
				return
			} else if !inc && strings.Contains(body, want) {
				t.Errorf("did NOT want metrics to include sentinel_config, have:\n%s", body)
			}
		}
	}
}
