package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestFalkorDB(t *testing.T) {
	if os.Getenv("TEST_FALKORDB_URI") == "" {
		t.Skipf("TEST_FALKORDB_URI not set - skipping")
	}

	tsts := []struct {
		addr                string
		isFalkorDB          bool
		wantFalkorDBMetrics bool
	}{
		{addr: os.Getenv("TEST_FALKORDB_URI"), isFalkorDB: true, wantFalkorDBMetrics: true},
		{addr: os.Getenv("TEST_FALKORDB_URI"), isFalkorDB: false, wantFalkorDBMetrics: false},
	}

	for _, tst := range tsts {
		e, _ := NewRedisExporter(tst.addr, Options{Namespace: "test", IsFalkorDB: tst.isFalkorDB})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"falkordb_total_graph_count": false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantFalkorDBMetrics {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in falkordb metrics but expected", want)
				}
			}
		} else if !tst.wantFalkorDBMetrics {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in falkordb metrics but *not* expected", want)
				}
			}
		}
	}
}
