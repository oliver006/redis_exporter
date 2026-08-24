package exporter

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const valkeyInfo = `# Memory
mem_replicas_repl_buffer:41
# Stats
expired_fields:42
expired_keys_with_volatile_items_stale_perc:12.5
acl_access_denied_tls_cert:43
acl_access_denied_db:44
# Replication
replicas_repl_buffer_size:45
replicas_repl_buffer_peak:46
replicas_waiting_psync:2
# TLS
tls_server_cert_serial:01A2
tls_server_cert_expires_in_seconds:47
tls_client_cert_serial:02B3
tls_client_cert_expires_in_seconds:48
tls_ca_cert_serial:03C4
tls_ca_cert_expires_in_seconds:49
# Scripting Engines
engines_count:2
engines_total_used_memory:50
engines_total_memory_overhead:51
engine_0:name=LUA,module=built-in,abi_version=1,used_memory=20,memory_overhead=21
engine_1:memory_overhead=31,used_memory=30,abi_version=2,module=example,name=JS
`

func collectValkeyInfoMetrics(t *testing.T, names ...string) map[string][]*dto.Metric {
	t.Helper()
	exp := getTestExporterWithOptions(t, Options{Namespace: "test"})

	ch := make(chan prometheus.Metric)
	go func() {
		exp.extractInfoMetrics(ch, valkeyInfo, 0)
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

func TestValkeyInfoScalarMetrics(t *testing.T) {
	expected := map[string]struct {
		value   float64
		counter bool
	}{
		"mem_replicas_repl_buffer_bytes":                    {value: 41},
		"expired_fields_total":                              {value: 42, counter: true},
		"expired_keys_with_volatile_items_stale_percentage": {value: 12.5},
		"acl_access_denied_tls_cert_total":                  {value: 43, counter: true},
		"acl_access_denied_db_total":                        {value: 44, counter: true},
		"replicas_repl_buffer_size_bytes":                   {value: 45},
		"replicas_repl_buffer_peak_bytes":                   {value: 46},
		"replicas_waiting_psync":                            {value: 2},
		"tls_server_cert_expires_in_seconds":                {value: 47},
		"tls_client_cert_expires_in_seconds":                {value: 48},
		"tls_ca_cert_expires_in_seconds":                    {value: 49},
		"scripting_engines_count":                           {value: 2},
		"scripting_engines_memory_used_bytes":               {value: 50},
		"scripting_engines_memory_overhead_bytes":           {value: 51},
	}

	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	metrics := collectValkeyInfoMetrics(t, names...)

	for name, want := range expected {
		got := metrics[name]
		if len(got) != 1 {
			t.Errorf("%s metric count = %d, want 1", name, len(got))
			continue
		}

		if want.counter {
			if got[0].Counter == nil || got[0].GetCounter().GetValue() != want.value {
				t.Errorf("%s counter = %v, want %v", name, got[0].Counter, want.value)
			}
			continue
		}
		if got[0].Gauge == nil || got[0].GetGauge().GetValue() != want.value {
			t.Errorf("%s gauge = %v, want %v", name, got[0].Gauge, want.value)
		}
	}
}

func TestValkeyTLSCertificateInfo(t *testing.T) {
	metrics := collectValkeyInfoMetrics(t, "tls_certificate_info")["tls_certificate_info"]
	if len(metrics) != 3 {
		t.Fatalf("tls_certificate_info metric count = %d, want 3", len(metrics))
	}

	serials := map[string]string{}
	for _, metric := range metrics {
		labels := metricLabels(metric)
		serials[labels["certificate"]] = labels["serial"]
	}
	for certificate, serial := range map[string]string{"server": "01A2", "client": "02B3", "ca": "03C4"} {
		if serials[certificate] != serial {
			t.Errorf("tls_certificate_info certificate %q serial = %q, want %q", certificate, serials[certificate], serial)
		}
	}
}

func TestValkeyScriptingEngineMetrics(t *testing.T) {
	names := []string{
		"scripting_engine_info",
		"scripting_engine_memory_used_bytes",
		"scripting_engine_memory_overhead_bytes",
	}
	metrics := collectValkeyInfoMetrics(t, names...)

	for _, name := range names {
		if len(metrics[name]) != 2 {
			t.Errorf("%s metric count = %d, want 2", name, len(metrics[name]))
		}
	}

	values := map[string]map[string]float64{}
	for _, name := range names {
		values[name] = map[string]float64{}
		for _, metric := range metrics[name] {
			labels := metricLabels(metric)
			key := labels["engine"] + "/" + labels["module"] + "/" + labels["abi_version"]
			values[name][key] = metric.GetGauge().GetValue()
		}
	}

	for name, expected := range map[string]map[string]float64{
		"scripting_engine_info":                  {"LUA/built-in/1": 1, "JS/example/2": 1},
		"scripting_engine_memory_used_bytes":     {"LUA/built-in/1": 20, "JS/example/2": 30},
		"scripting_engine_memory_overhead_bytes": {"LUA/built-in/1": 21, "JS/example/2": 31},
	} {
		for engine, want := range expected {
			if values[name][engine] != want {
				t.Errorf("%s{%s} = %v, want %v", name, engine, values[name][engine], want)
			}
		}
	}
}

func TestParseScriptingEngineInfoRejectsInvalidMemory(t *testing.T) {
	for _, value := range []string{
		"name=LUA,module=built-in,abi_version=1,used_memory=-1,memory_overhead=2",
		"name=LUA,module=built-in,abi_version=1,used_memory=1",
	} {
		if _, err := parseScriptingEngineInfo(value); err == nil {
			t.Errorf("parseScriptingEngineInfo(%q) unexpectedly succeeded", value)
		}
	}
}

func metricLabels(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.Label))
	for _, label := range metric.Label {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}
