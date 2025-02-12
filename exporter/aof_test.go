package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAof(t *testing.T) {
	if os.Getenv("TEST_AOF_FILE_SIZE_URL") == "" {
		t.Skipf("TEST_AOF_FILE_SIZE_URL not set - skipping")
	}

	tsts := []struct {
		addr              string
		enableAofFileSize bool
		wantAofFileSize   bool
	}{
		{addr: os.Getenv("TEST_AOF_FILE_SIZE_URL"), enableAofFileSize: true, wantAofFileSize: true},
		{addr: os.Getenv("TEST_AOF_FILE_SIZE_URL"), enableAofFileSize: false, wantAofFileSize: false},
	}

	for _, tst := range tsts {
		e, _ := NewRedisExporter(tst.addr, Options{InclAofFileSize: tst.enableAofFileSize, OverrideAofFilePath: "../data/appendonlydir"})

		chM := make(chan prometheus.Metric)

		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"aof_file_size_bytes": false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantAofFileSize {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in AOF metrics but expected", want)
				}
			}
		} else if !tst.wantAofFileSize {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in AOF metrics but *not* expected", want)
				}
			}
		}
	}
}
