package exporter

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestScriptingEngineMetrics(t *testing.T) {
	addr := os.Getenv("TEST_VALKEY9_URI")
	if addr == "" {
		t.Skip("TEST_VALKEY9_URI not set - skipping")
	}

	e := getTestExporterWithAddr(addr)
	ts := httptest.NewServer(e)
	defer ts.Close()

	body := downloadURL(t, ts.URL+"/metrics")
	for _, metric := range []string{
		"test_scripting_engines_count",
		"test_scripting_engines_memory_used_bytes",
		"test_scripting_engines_memory_overhead_bytes",
		"test_scripting_engine_info",
		"test_scripting_engine_memory_used_bytes",
		"test_scripting_engine_memory_overhead_bytes",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing metric %s", metric)
		}
	}
}

func TestParseScriptingEngineInfoErrors(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "invalid field", value: "name=LUA,module", wantErr: "invalid scripting engine field"},
		{name: "missing name", value: "module=lua,abi_version=4,used_memory=1,memory_overhead=2", wantErr: `missing scripting engine field "name"`},
		{name: "invalid used memory", value: "name=LUA,module=lua,abi_version=4,used_memory=-1,memory_overhead=2", wantErr: "invalid scripting engine used_memory"},
		{name: "invalid memory overhead", value: "name=LUA,module=lua,abi_version=4,used_memory=1,memory_overhead=-2", wantErr: "invalid scripting engine memory_overhead"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseScriptingEngineInfo(test.value)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("parseScriptingEngineInfo() err = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestHandleMetricsScriptingEnginesNonMetricPaths(t *testing.T) {
	e := &Exporter{}
	if e.handleMetricsScriptingEngines(nil, "engines_count", "1") {
		t.Errorf("aggregate engine field unexpectedly handled as a per-engine metric")
	}
	if !e.handleMetricsScriptingEngines(nil, "engine_0", "invalid") {
		t.Errorf("malformed per-engine field was not handled")
	}
}
