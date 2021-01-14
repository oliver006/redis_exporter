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

		found := false
		want := "tile38_threads_total"
		for m := range chM {
			if strings.Contains(m.Desc().String(), want) {
				found = true
			}
		}

		if tst.wantTile38Metrics && !found {
			t.Errorf("%s was *not* found in tile38 metrics but expected", want)
		} else if !tst.wantTile38Metrics && found {
			t.Errorf("%s was *found* in tile38 metrics but *not* expected", want)
		}
	}
}
