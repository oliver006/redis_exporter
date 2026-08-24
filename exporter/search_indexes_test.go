package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func setupSearchIndex(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("setupSearchIndex() - couldn't setup redis for uri %s, err: %s ", addr, err)
		return err
	}
	defer c.Close()

	// create search index, based on https://redis.io/docs/latest/commands/ft.create and https://valkey.io/commands/ft.create
	if _, err := doRedisCmd(c, "FT.CREATE", "test_index", "SCHEMA", "my_hash_field_key", "VECTOR", "HNSW", "10", "TYPE", "FLOAT32", "DIM", "20", "DISTANCE_METRIC", "COSINE", "M", "4", "EF_CONSTRUCTION", "100"); err != nil {
		log.Printf("setupSearchIndex() - couldn't create search index, err: %s ", err)
		return err
	}
	return nil
}

func TestExtractSearchIndexesMetrics(t *testing.T) {
	testRedis8URI := os.Getenv("TEST_REDIS8_URI")
	testValkey9BundleURI := os.Getenv("TEST_VALKEY9_BUNDLE_URI")
	if testRedis8URI == "" || testValkey9BundleURI == "" {
		t.Skipf("TEST_REDIS8_URI or TEST_VALKEY9_BUNDLE_URI aren't set - skipping")
	}
	if err := setupSearchIndex(t, testRedis8URI); err != nil {
		t.Fatalf("couldn't create search index in TEST_REDIS8_URI (%s), err: %s ", testRedis8URI, err)
	}
	if err := setupSearchIndex(t, testValkey9BundleURI); err != nil {
		t.Fatalf("couldn't create search index in TEST_VALKEY9_BUNDLE_URI (%s), err: %s ", testValkey9BundleURI, err)
	}

	tsts := []struct {
		addr                     string
		inclSearchIndexesMetrics bool
		wantSearchIndexesMetrics bool
	}{
		{addr: testRedis8URI, inclSearchIndexesMetrics: true, wantSearchIndexesMetrics: true},
		{addr: testRedis8URI, inclSearchIndexesMetrics: false, wantSearchIndexesMetrics: false},
		{addr: testValkey9BundleURI, inclSearchIndexesMetrics: true, wantSearchIndexesMetrics: true},
		{addr: testValkey9BundleURI, inclSearchIndexesMetrics: false, wantSearchIndexesMetrics: false},
	}

	for _, tst := range tsts {
		e, _ := NewRedisExporter(tst.addr, Options{Namespace: "test", InclSearchIndexesMetrics: tst.inclSearchIndexesMetrics})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"search_index_num_docs":                      false,
			"search_index_max_doc_id":                    false,
			"search_index_num_terms":                     false,
			"search_index_num_records":                   false,
			"search_index_inverted_size_bytes":           false,
			"search_index_total_inverted_index_blocks":   false,
			"search_index_vector_index_size_bytes":       false,
			"search_index_offset_vectors_size_bytes":     false,
			"search_index_doc_table_size_bytes":          false,
			"search_index_sortable_values_size_bytes":    false,
			"search_index_key_table_size_bytes":          false,
			"search_index_tag_overhead_size_bytes":       false,
			"search_index_text_overhead_size_bytes":      false,
			"search_index_total_index_memory_size_bytes": false,
			"search_index_geoshapes_size_bytes":          false,
			"search_index_avg_per_doc_records":           false,
			"search_index_avg_per_record_bytes":          false,
			"search_index_avg_per_term_offsets":          false,
			"search_index_avg_per_record_offset_bits":    false,
			"search_index_indexing":                      false,
			"search_index_percent_indexed":               false,
			"search_index_hash_indexing_failures":        false,
			"search_index_number_of_uses_total":          false,
			"search_index_cleaning":                      false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantSearchIndexesMetrics {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in Redis Search indexes metrics but expected", want)
				}
			}
		} else if !tst.wantSearchIndexesMetrics {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in Redis Search indexes metrics but *not* expected", want)
				}
			}
		}
	}
}

func TestNewRedisExporterSearchIndexRegex(t *testing.T) {
	tests := []struct {
		name    string
		regex   string
		wantErr bool
	}{
		{name: "valid", regex: "^idx:", wantErr: false},
		{name: "invalid", regex: "[", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRedisExporter("", Options{
				Namespace:                "test",
				InclSearchIndexesMetrics: true,
				CheckSearchIndexes:       tt.regex,
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRedisExporter() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
