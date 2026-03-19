package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestExtractRdbFileSizeMetric(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_URI")
	e, err := NewRedisExporter(addr, Options{
		Namespace:             "test",
		InclRdbFileSizeMetric: true,
		ConfigCommandName:     "CONFIG",
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() failed: %s", err)
	}

	c, err := e.connectToRedis()
	if err != nil {
		t.Fatalf("connectToRedis() failed: %s", err)
	}
	defer c.Close()

	ch := make(chan prometheus.Metric, 100)
	e.extractRdbFileSizeMetric(ch, c)
	close(ch)

	found := false
	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			found = true
			d := &dto.Metric{}
			if err := m.Write(d); err == nil && d.GetGauge() != nil {
				metricValue := d.GetGauge().GetValue()
				if metricValue < 0 {
					t.Errorf("rdb_current_size_bytes should not be negative, got %f", metricValue)
				}
			}
			break
		}
	}

	if !found {
		t.Error("rdb_current_size_bytes metric should be present")
	}
}

func TestExtractRdbFileSizeMetricDisabled(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_URI")

	e, err := NewRedisExporter(addr, Options{
		Namespace:             "test",
		InclRdbFileSizeMetric: false,
		ConfigCommandName:     "CONFIG",
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() failed: %s", err)
	}

	ch := make(chan prometheus.Metric, 100)
	go func() {
		e.Collect(ch)
		close(ch)
	}()

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when disabled")
		}
	}
}

func TestExtractRdbFileSizeMetricConfigDisabled(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_URI")

	e, err := NewRedisExporter(addr, Options{
		Namespace:             "test",
		InclRdbFileSizeMetric: true,
		ConfigCommandName:     "-",
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() failed: %s", err)
	}

	c, err := e.connectToRedis()
	if err != nil {
		t.Fatalf("connectToRedis() failed: %s", err)
	}
	defer c.Close()

	ch := make(chan prometheus.Metric, 100)
	e.extractRdbFileSizeMetric(ch, c)
	close(ch)

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when CONFIG command is disabled")
		}
	}
}
