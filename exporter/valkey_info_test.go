package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const valkeyInfoTestKey = "redis_exporter_valkey_info_test"

func TestValkeyInfoMetrics(t *testing.T) {
	e := newValkeyInfoTestExporter(t, "TEST_VALKEY9_URI")

	before := collectValkeyInfoMetrics(t, e, "expired_fields_total")
	beforeExpired := requireValkeyInfoCounter(t, before, "expired_fields_total")
	expireValkeyHashField(t, e)

	names := []string{
		"up",
		"instance_info",
		"mem_replicas_repl_buffer_bytes",
		"expired_fields_total",
		"expired_keys_with_volatile_items_stale_percentage",
		"acl_access_denied_tls_cert_total",
		"acl_access_denied_db_total",
		"replicas_waiting_psync",
	}
	metrics := collectValkeyInfoMetrics(t, e, names...)

	requireValkey9Instance(t, metrics, "master")
	requireValkeyInfoGauge(t, metrics, "up", 1)

	counters := map[string]bool{
		"expired_fields_total":             true,
		"acl_access_denied_tls_cert_total": true,
		"acl_access_denied_db_total":       true,
	}
	for _, name := range []string{
		"mem_replicas_repl_buffer_bytes",
		"expired_fields_total",
		"expired_keys_with_volatile_items_stale_percentage",
		"acl_access_denied_tls_cert_total",
		"acl_access_denied_db_total",
		"replicas_waiting_psync",
	} {
		metric := requireValkeyInfoMetric(t, metrics, name)
		if counters[name] {
			if metric.Counter == nil {
				t.Errorf("%s is not a counter", name)
			}
		} else if metric.Gauge == nil {
			t.Errorf("%s is not a gauge", name)
		}
	}

	afterExpired := requireValkeyInfoCounter(t, metrics, "expired_fields_total")
	if afterExpired < beforeExpired+1 {
		t.Errorf("expired_fields_total = %v, want at least %v", afterExpired, beforeExpired+1)
	}
}

func TestValkeyTLSInfoMetrics(t *testing.T) {
	plain := newValkeyInfoTestExporter(t, "TEST_VALKEY9_URI")
	tlsExporter := newValkeyInfoTestExporterWithOptions(t, "TEST_VALKEY9_TLS_URI", Options{
		Namespace:           "test",
		SkipTLSVerification: true,
		ClientCertFile:      "../contrib/tls/redis.crt",
		ClientKeyFile:       "../contrib/tls/redis.key",
	})

	names := []string{
		"tls_server_cert_expires_in_seconds",
		"tls_client_cert_expires_in_seconds",
		"tls_ca_cert_expires_in_seconds",
		"tls_certificate_info",
	}
	plainMetrics := collectValkeyInfoMetrics(t, plain, names...)
	tlsMetrics := collectValkeyInfoMetrics(t, tlsExporter, names...)

	for _, name := range names[:3] {
		requireValkeyInfoGauge(t, plainMetrics, name, 0)
		requireValkeyInfoGauge(t, tlsMetrics, name, -1)
	}
	for _, name := range []string{"tls_server_cert_expires_in_seconds", "tls_ca_cert_expires_in_seconds"} {
		if value := requireValkeyInfoGauge(t, tlsMetrics, name, -1); value <= 0 {
			t.Errorf("TLS Valkey %s = %v, want greater than 0", name, value)
		}
	}

	plainSerials := valkeyTLSCertificateSerials(t, plainMetrics)
	tlsSerials := valkeyTLSCertificateSerials(t, tlsMetrics)
	for _, certificate := range []string{"server", "ca"} {
		if plainSerials[certificate] != "none" {
			t.Errorf("plain Valkey %s certificate serial = %q, want none", certificate, plainSerials[certificate])
		}
		if tlsSerials[certificate] == "none" || tlsSerials[certificate] == plainSerials[certificate] {
			t.Errorf("TLS Valkey %s certificate serial = %q, want a real serial different from plain Valkey", certificate, tlsSerials[certificate])
		}
	}
}

func TestValkeyReplicaInfoMetrics(t *testing.T) {
	e := newValkeyInfoTestExporter(t, "TEST_VALKEY9_REPLICA_URI")
	metrics := collectValkeyInfoMetrics(t, e,
		"up",
		"instance_info",
		"replicas_repl_buffer_size_bytes",
		"replicas_repl_buffer_peak_bytes",
	)

	requireValkey9Instance(t, metrics, "slave")
	requireValkeyInfoGauge(t, metrics, "up", 1)
	size := requireValkeyInfoGauge(t, metrics, "replicas_repl_buffer_size_bytes", -1)
	peak := requireValkeyInfoGauge(t, metrics, "replicas_repl_buffer_peak_bytes", -1)
	if peak < size {
		t.Errorf("replicas_repl_buffer_peak_bytes = %v, smaller than current size %v", peak, size)
	}
}

func newValkeyInfoTestExporter(t *testing.T, envName string) *Exporter {
	return newValkeyInfoTestExporterWithOptions(t, envName, Options{Namespace: "test"})
}

func newValkeyInfoTestExporterWithOptions(t *testing.T, envName string, options Options) *Exporter {
	t.Helper()
	addr := os.Getenv(envName)
	if addr == "" {
		t.Skipf("%s not set - skipping", envName)
	}

	e, err := NewRedisExporter(addr, options)
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}
	return e
}

func expireValkeyHashField(t *testing.T, e *Exporter) {
	t.Helper()
	c, err := e.connectToRedis()
	if err != nil {
		t.Fatalf("connectToRedis() err: %s", err)
	}
	defer c.Close()
	defer doRedisCmd(c, "DEL", valkeyInfoTestKey)

	if _, err := doRedisCmd(c, "HSET", valkeyInfoTestKey, "field", "value"); err != nil {
		t.Fatalf("HSET err: %s", err)
	}
	if _, err := doRedisCmd(c, "HEXPIRE", valkeyInfoTestKey, 0, "FIELDS", 1, "field"); err != nil {
		t.Fatalf("HEXPIRE err: %s", err)
	}
}

func collectValkeyInfoMetrics(t *testing.T, e *Exporter, names ...string) map[string][]*dto.Metric {
	t.Helper()
	ch := make(chan prometheus.Metric)
	go func() {
		e.Collect(ch)
		close(ch)
	}()

	metrics := make(map[string][]*dto.Metric, len(names))
	for metric := range ch {
		desc := metric.Desc().String()
		for _, name := range names {
			if !strings.Contains(desc, `fqName: "test_`+name+`"`) {
				continue
			}

			got := &dto.Metric{}
			if err := metric.Write(got); err != nil {
				t.Fatalf("metric.Write() err: %s", err)
			}
			metrics[name] = append(metrics[name], got)
		}
	}
	return metrics
}

func requireValkeyInfoMetric(t *testing.T, metrics map[string][]*dto.Metric, name string) *dto.Metric {
	t.Helper()
	if len(metrics[name]) != 1 {
		t.Fatalf("%s metric count = %d, want 1", name, len(metrics[name]))
	}
	return metrics[name][0]
}

func requireValkeyInfoGauge(t *testing.T, metrics map[string][]*dto.Metric, name string, want float64) float64 {
	t.Helper()
	metric := requireValkeyInfoMetric(t, metrics, name)
	if metric.Gauge == nil {
		t.Fatalf("%s is not a gauge", name)
	}
	value := metric.GetGauge().GetValue()
	if want >= 0 && value != want {
		t.Errorf("%s = %v, want %v", name, value, want)
	}
	return value
}

func requireValkeyInfoCounter(t *testing.T, metrics map[string][]*dto.Metric, name string) float64 {
	t.Helper()
	metric := requireValkeyInfoMetric(t, metrics, name)
	if metric.Counter == nil {
		t.Fatalf("%s is not a counter", name)
	}
	return metric.GetCounter().GetValue()
}

func requireValkey9Instance(t *testing.T, metrics map[string][]*dto.Metric, role string) {
	t.Helper()
	labels := valkeyInfoMetricLabels(requireValkeyInfoMetric(t, metrics, "instance_info"))
	if !strings.HasPrefix(labels["valkey_version"], "9.") {
		t.Fatalf("valkey_version = %q, want Valkey 9", labels["valkey_version"])
	}
	if labels["role"] != role {
		t.Fatalf("role = %q, want %q", labels["role"], role)
	}
}

func valkeyTLSCertificateSerials(t *testing.T, metrics map[string][]*dto.Metric) map[string]string {
	t.Helper()
	certificates := metrics["tls_certificate_info"]
	if len(certificates) != 3 {
		t.Fatalf("tls_certificate_info metric count = %d, want 3", len(certificates))
	}

	serials := map[string]string{}
	for _, metric := range certificates {
		labels := valkeyInfoMetricLabels(metric)
		if metric.GetGauge().GetValue() != 1 {
			t.Errorf("tls_certificate_info%v = %v, want 1", labels, metric.GetGauge().GetValue())
		}
		serials[labels["certificate"]] = labels["serial"]
	}
	for _, certificate := range []string{"server", "client", "ca"} {
		if _, ok := serials[certificate]; !ok {
			t.Errorf("tls_certificate_info missing certificate %q", certificate)
		}
	}
	return serials
}

func valkeyInfoMetricLabels(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.Label))
	for _, label := range metric.Label {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}
