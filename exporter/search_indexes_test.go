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

	// create search index, based on https://redis.io/docs/latest/develop/get-started/document-database/
	if _, err := doRedisCmd(c, "FT.CREATE", "idx:bicycle", "ON", "JSON", "PREFIX", "1", "bicycle:", "SCORE", "1.0", "SCHEMA", "$.brand", "AS", "brand", "TEXT", "WEIGHT", "1.0", "$.model", "AS", "model", "TEXT", "WEIGHT", "1.0", "$.description", "AS", "description", "TEXT", "WEIGHT", "1.0", "$.price", "AS", "price", "NUMERIC", "$.condition", "AS", "condition", "TAG", "SEPARATOR", ","); err != nil {
		log.Printf("setupSearchIndex() - couldn't create search index, err: %s ", err)
		return err
	}
	return nil
}

func TestExtractSearchIndexesMetrics(t *testing.T) {
	if os.Getenv("TEST_REDIS8_URI") == "" {
		t.Skipf("TEST_REDIS8_URI not set - skipping")
	}
	if err := setupSearchIndex(t, os.Getenv("TEST_REDIS8_URI")); err != nil {
		t.Fatalf("couldn't create search index, err: %s ", err)
	}

	tsts := []struct {
		addr                     string
		inclSearchIndexesMetrics bool
		wantSearchIndexesMetrics bool
	}{
		{addr: os.Getenv("TEST_REDIS8_URI"), inclSearchIndexesMetrics: true, wantSearchIndexesMetrics: true},
		{addr: os.Getenv("TEST_REDIS8_URI"), inclSearchIndexesMetrics: false, wantSearchIndexesMetrics: false},
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
