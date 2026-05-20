package exporter

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func testModuleMetrics(t *testing.T, addr string, wantedMetrics []string) {
	t.Helper()

	for _, inclModules := range []bool{true, false} {
		t.Run(fmt.Sprintf("inclModulesMetrics:%t", inclModules), func(t *testing.T) {
			e, _ := NewRedisExporter(addr, Options{Namespace: "test", InclModulesMetrics: inclModules})

			chM := make(chan prometheus.Metric)
			go func() {
				e.Collect(chM)
				close(chM)
			}()

			found := map[string]bool{}
			for m := range chM {
				desc := m.Desc().String()
				for _, want := range wantedMetrics {
					if strings.Contains(desc, want) {
						found[want] = true
					}
				}
			}

			for _, want := range wantedMetrics {
				if inclModules && !found[want] {
					t.Errorf("%s was *not* found in Redis Modules metrics but expected", want)
				}
				if !inclModules && found[want] {
					t.Errorf("%s was *found* in Redis Modules metrics but *not* expected", want)
				}
			}
		})
	}
}

func TestModulesv80(t *testing.T) {
	if os.Getenv("TEST_REDIS8_URI") == "" {
		t.Skipf("TEST_REDIS8_URI not set - skipping")
	}

	testModuleMetrics(t, os.Getenv("TEST_REDIS8_URI"), []string{
		"module_info",
		"search_number_of_indexes",
		"search_number_of_active_indexes",
		"search_number_of_active_indexes_running_queries",
		"search_number_of_active_indexes_indexing",
		"search_total_active_write_threads",
		"search_indexing_time_ms_total",
		"search_total_num_docs_in_indexes",
	})
}

func TestModulesValkey(t *testing.T) {
	if os.Getenv("TEST_VALKEY8_BUNDLE_URI") == "" {
		t.Skipf("TEST_VALKEY8_BUNDLE_URI not set - skipping")
	}

	testModuleMetrics(t, os.Getenv("TEST_VALKEY8_BUNDLE_URI"), []string{
		"module_info",
		"search_number_of_indexes",
		"bf_bloom_total_memory_bytes",
		"bf_bloom_num_objects",
		"bf_bloom_num_filters_across_objects",
		"bf_bloom_num_items_across_objects",
		"bf_bloom_capacity_across_objects",
		"json_total_memory_bytes",
		"json_num_documents",
		"search_used_memory_bytes",
		"search_number_of_attributes",
		"search_total_indexed_documents",
		"search_query_queue_size",
		"search_writer_queue_size",
		"search_string_interning_store_size",
		"search_vector_externing_hash_extern_errors",
		"search_vector_externing_num_lru_entries",
		"bf_bloom_defrag_hits_total",
		"bf_bloom_defrag_misses_total",
		"search_worker_pool_suspend_count",
		"search_writer_resumed_count",
		"search_reader_resumed_count",
		"search_writer_suspension_expired_count",
		"search_rdb_load_success_count",
		"search_rdb_load_failure_count",
		"search_rdb_save_success_count",
		"search_rdb_save_failure_count",
		"search_successful_requests_count",
		"search_failure_requests_count",
		"search_hybrid_requests_count",
		"search_inline_filtering_requests_count",
		"search_hnsw_add_exceptions_count",
		"search_hnsw_remove_exceptions_count",
		"search_hnsw_modify_exceptions_count",
		"search_hnsw_search_exceptions_count",
		"search_hnsw_create_exceptions_count",
		"search_vector_externing_entry_count",
		"search_vector_externing_generated_value_count",
		"search_vector_externing_lru_promote_count",
		"search_vector_externing_deferred_entry_count",
	})
}
