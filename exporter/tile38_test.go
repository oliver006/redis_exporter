package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestTile38(t *testing.T) {
	if os.Getenv("TEST_TILE38_URI") == "" {
		t.Skipf("TEST_TILE38_URI not set - skipping")
	}

	tsts := []struct {
		addr              string
		isTile38          bool
		wantTile38Metrics bool
	}{
		{addr: os.Getenv("TEST_TILE38_URI"), isTile38: true, wantTile38Metrics: true},
		{addr: os.Getenv("TEST_TILE38_URI"), isTile38: false, wantTile38Metrics: false},
		{addr: os.Getenv("TEST_REDIS_URI"), isTile38: true, wantTile38Metrics: false},
		{addr: os.Getenv("TEST_REDIS_URI"), isTile38: false, wantTile38Metrics: false},
	}

	for _, tst := range tsts {
		e, _ := NewRedisExporter(tst.addr, Options{Namespace: "test", IsTile38: tst.isTile38})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"tile38_threads_total":       false,
			"tile38_cpus_total":          false,
			"tile38_go_goroutines_total": false,
			"tile38_avg_item_size_bytes": false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantTile38Metrics {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in tile38 metrics but expected", want)
				}
			}
		} else if !tst.wantTile38Metrics {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in tile38 metrics but *not* expected", want)
				}
			}
		}
	}
}
