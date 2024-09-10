package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestSearch(t *testing.T) {
	if os.Getenv("TEST_REDIS_SEARCH_URI") == "" {
		t.Skipf("TEST_REDIS_SEARCH_URI not set - skipping")
	}

	tsts := []struct {
		addr              string
		isSearch          bool
		wantSearchMetrics bool
	}{
		{addr: os.Getenv("TEST_REDIS_SEARCH_URI"), isSearch: true, wantSearchMetrics: true},
		{addr: os.Getenv("TEST_REDIS_SEARCH_URI"), isSearch: false, wantSearchMetrics: false},
		{addr: os.Getenv("TEST_REDIS_SEARCH_URI"), isSearch: true, wantSearchMetrics: false},
		{addr: os.Getenv("TEST_REDIS_SEARCH_URI"), isSearch: false, wantSearchMetrics: false},
	}

	for _, tst := range tsts {
		e, _ := NewRedisExporter(tst.addr, Options{Namespace: "test", IsTile38: tst.isSearch})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"search_number_of_indexes":   false,
			"search_used_memory_indexes": false,
			"search_total_indexing_time": false,
			"search_global_idle":         false,
			"search_global_total":        false,
			"search_bytes_collected":     false,
			"search_total_cycles":        false,
			"search_total_ms_run":        false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantSearchMetrics {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in RediSearch metrics but expected", want)
				}
			}
		} else if !tst.wantSearchMetrics {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in RediSearch metrics but *not* expected", want)
				}
			}
		}
	}
}
