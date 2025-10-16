package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestModulesv74(t *testing.T) {
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
			"search_indexing_time_ms_total":    false,
			"search_global_idle":               false,
			"search_global_total":              false,
			"search_collected_bytes":           false,
			"search_cycles_total":              false,
			"search_run_ms_total":              false,
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

func TestModulesv80(t *testing.T) {
	if os.Getenv("TEST_REDIS8_URI") == "" || os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS8_URI or TEST_REDIS_URI aren't set - skipping")
	}

	tsts := []struct {
		addr               string
		inclModulesMetrics bool
		wantModulesMetrics bool
	}{
		{addr: os.Getenv("TEST_REDIS8_URI"), inclModulesMetrics: true, wantModulesMetrics: true},
		{addr: os.Getenv("TEST_REDIS8_URI"), inclModulesMetrics: false, wantModulesMetrics: false},
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
			"module_info":                                     false,
			"search_number_of_indexes":                        false,
			"search_used_memory_indexes_bytes":                false,
			"search_indexing_time_ms_total":                   false,
			"search_dialect_1":                                false,
			"search_dialect_2":                                false,
			"search_dialect_3":                                false,
			"search_dialect_4":                                false,
			"search_number_of_active_indexes":                 false,
			"search_number_of_active_indexes_running_queries": false,
			"search_number_of_active_indexes_indexing":        false,
			"search_total_active_write_threads":               false,
			"search_smallest_memory_index_bytes":              false,
			"search_largest_memory_index_bytes":               false,
			"search_used_memory_vector_index_bytes":           false,
			"search_global_idle_user":                         false,
			"search_global_idle_internal":                     false,
			"search_global_total_user":                        false,
			"search_global_total_internal":                    false,
			"search_gc_collected_bytes":                       false,
			"search_gc_total_docs_not_collected":              false,
			"search_gc_marked_deleted_vectors":                false,
			"search_errors_indexing_failures":                 false,
			"search_gc_cycles_total":                          false,
			"search_gc_run_ms_total":                          false,
			"search_queries_processed_total":                  false,
			"search_query_commands_total":                     false,
			"search_query_execution_time_ms_total":            false,
			"search_active_queries_total":                     false,
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
