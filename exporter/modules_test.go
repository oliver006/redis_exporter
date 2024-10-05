package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestModules(t *testing.T) {
	if os.Getenv("TEST_REDIS_MODULES_URI") == "" {
		t.Skipf("TEST_REDIS_MODULES_URI not set - skipping")
	}

	tsts := []struct {
		addr               string
		inclModulesMetrics bool
		wantModulesMetrics bool
	}{
		{addr: os.Getenv("TEST_REDIS_MODULES_URI"), inclModulesMetrics: true, wantModulesMetrics: true},
		{addr: os.Getenv("TEST_REDIS_MODULES_URI"), inclModulesMetrics: false, wantModulesMetrics: false},
		{addr: os.Getenv("TEST_REDIS_URI"), inclModulesMetrics: true, wantModulesMetrics: false},
		{addr: os.Getenv("TEST_REDIS_URI"), inclModulesMetrics: false, wantModulesMetrics: false},
	}

	for _, tst := range tsts {
		e, _ := NewRedisExporter(tst.addr, Options{Namespace: "test", InclModulesMetrics: tst.inclModulesMetrics})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"module_info":                      false,
			"search_number_of_indexes":         false,
			"search_used_memory_indexes_bytes": false,
			"search_total_indexing_time_ms":    false,
			"search_global_idle":               false,
			"search_global_total":              false,
			"search_collected_bytes":           false,
			"search_total_cycles":              false,
			"search_total_run_ms":              false,
			"search_dialect_1":                 false,
			"search_dialect_2":                 false,
			"search_dialect_3":                 false,
			"search_dialect_4":                 false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantModulesMetrics {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in Redis Modules metrics but expected", want)
				}
			}
		} else if !tst.wantModulesMetrics {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in Redis Modules metrics but *not* expected", want)
				}
			}
		}
	}
}
