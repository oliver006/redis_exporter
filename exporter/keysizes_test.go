package exporter

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestKeysizesMetrics(t *testing.T) {
	addr := os.Getenv("TEST_REDIS8_URI")
	if addr == "" {
		t.Skip("TEST_REDIS8_URI not set - skipping")
	}

	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	e := getTestExporterWithAddrAndOptions(addr, Options{Namespace: "test", InclKeysizesMetrics: true})
	ts := httptest.NewServer(e)
	defer ts.Close()

	body := downloadURL(t, ts.URL+"/metrics")
	for _, metric := range []string{
		`test_keysizes_key_count{bucket=`,
		`measure="sizes"`,
		`type="strings"`,
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing %s in scrape body", metric)
		}
	}
}

func TestKeysizesMetricsDisabledByDefault(t *testing.T) {
	addr := os.Getenv("TEST_REDIS8_URI")
	if addr == "" {
		t.Skip("TEST_REDIS8_URI not set - skipping")
	}

	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	e := getTestExporterWithAddr(addr)
	ts := httptest.NewServer(e)
	defer ts.Close()

	body := downloadURL(t, ts.URL+"/metrics")
	if strings.Contains(body, "test_keysizes_key_count") {
		t.Errorf("keysizes metrics exported without --include-keysizes-metrics")
	}
}

func TestParseKeysizesBucket(t *testing.T) {
	tests := []struct {
		label string
		want  uint64
	}{
		{"0", 0},
		{"1", 1},
		{"64", 64},
		{"512", 512},
		{"1K", 1024},
		{"2M", 2 * 1024 * 1024},
		{"4G", 4 * 1024 * 1024 * 1024},
		{"16M", 16 * 1024 * 1024},
		{"256P", 256 << 50}, // highest label Redis can emit (bin 59)
		{"4E", 4 << 60},     // last defined label, beyond the 60-bin cutoff
		{"1024", 1024},      // plain numeric labels parse too
	}
	for _, test := range tests {
		got, err := parseKeysizesBucket(test.label)
		if err != nil {
			t.Errorf("parseKeysizesBucket(%q) err = %v", test.label, err)
			continue
		}
		if got != test.want {
			t.Errorf("parseKeysizesBucket(%q) = %d, want %d", test.label, got, test.want)
		}
	}

	// "32E" parses as a number but 32*2^60 overflows uint64 — the guard must reject it
	for _, label := range []string{"", "K", "abc", "1X", "32E"} {
		if _, err := parseKeysizesBucket(label); err == nil {
			t.Errorf("parseKeysizesBucket(%q) expected error, got nil", label)
		}
	}
}

func TestHandleMetricsKeysizes(t *testing.T) {
	e := getTestExporterWithAddr("redis://localhost:6379")
	ch := make(chan prometheus.Metric, 10)

	// non-keysizes fields and malformed entries register nothing and don't panic
	for _, field := range []struct{ key, value string }{
		{"not_a_keysizes_field", "1=2"},
		{"db0_distrib_strings_bytes", "1=2"}, // unknown measure
		{"db0_distrib_strings_sizes", ""},
		{"db0_distrib_strings_sizes", "garbage"},
		{"db0_distrib_strings_sizes", "1X=5,=3,4="},
	} {
		e.handleMetricsKeysizes(ch, field.key, field.value)
	}
	if n := len(ch); n != 0 {
		t.Errorf("expected no metrics registered, got %d", n)
	}

	// malformed entries don't take down valid ones on the same line
	e.handleMetricsKeysizes(ch, "db0_distrib_strings_sizes", "garbage,1K=5")
	if n := len(ch); n != 1 {
		t.Fatalf("expected 1 metric registered, got %d", n)
	}

	d := &dto.Metric{}
	if err := (<-ch).Write(d); err != nil {
		t.Fatalf("metric.Write: %v", err)
	}
	// the "1K" label must be normalized to its numeric value
	want := map[string]string{"db": "db0", "type": "strings", "measure": "sizes", "bucket": "1024"}
	for _, l := range d.GetLabel() {
		if w, ok := want[l.GetName()]; !ok || w != l.GetValue() {
			t.Errorf("label %s = %q, want %q", l.GetName(), l.GetValue(), w)
		}
		delete(want, l.GetName())
	}
	if len(want) != 0 {
		t.Errorf("missing labels: %v", want)
	}
	if v := d.GetGauge().GetValue(); v != 5 {
		t.Errorf("value = %v, want 5", v)
	}
}
