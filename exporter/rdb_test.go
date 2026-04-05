package exporter

import (
	"os"
	"path/filepath"
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

	ch := make(chan prometheus.Metric, 100)
	go func() {
		e.Collect(ch)
		close(ch)
	}()

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

	ch := make(chan prometheus.Metric, 100)
	go func() {
		e.Collect(ch)
		close(ch)
	}()

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when CONFIG command is disabled")
		}
	}
}

func TestExtractRdbFileSizeMetricEmptyConfig(t *testing.T) {
	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, []interface{}{})
	close(ch)

	// Should not produce any metrics for empty config
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("Expected 0 metrics for empty config, got %d", count)
	}
}

func TestExtractRdbFileSizeMetricOddConfig(t *testing.T) {
	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	// Odd number of elements (invalid config)
	e.extractRdbFileSizeMetric(ch, []interface{}{"dir", "/tmp", "dbfilename"})
	close(ch)

	// Should not produce any metrics for invalid config
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("Expected 0 metrics for odd config, got %d", count)
	}
}

func TestExtractRdbFileSizeMetricMissingKeys(t *testing.T) {
	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	// Config without dir or dbfilename
	e.extractRdbFileSizeMetric(ch, []interface{}{"maxmemory", "1000000", "timeout", "300"})
	close(ch)

	// Should not produce any metrics when required keys are missing
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("Expected 0 metrics for config without dir/dbfilename, got %d", count)
	}
}

func TestExtractRdbFileSizeMetricNonExistentFile(t *testing.T) {
	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	// Point to a non-existent file
	e.extractRdbFileSizeMetric(ch, []interface{}{
		"dir", "/tmp/nonexistent_redis_dir_12345",
		"dbfilename", "dump.rdb",
	})
	close(ch)

	// Should produce a metric with value 0 for non-existent file
	found := false
	for m := range ch {
		found = true
		d := &dto.Metric{}
		if err := m.Write(d); err == nil && d.GetGauge() != nil {
			metricValue := d.GetGauge().GetValue()
			if metricValue != 0 {
				t.Errorf("Expected metric value 0 for non-existent file, got %f", metricValue)
			}
		}
	}
	if !found {
		t.Error("Expected metric to be produced for non-existent file")
	}
}

func TestExtractRdbFileSizeMetricValidFile(t *testing.T) {
	// Create a temporary RDB file
	tmpDir := t.TempDir()
	rdbFile := filepath.Join(tmpDir, "dump.rdb")
	testData := []byte("test rdb data content")
	if err := os.WriteFile(rdbFile, testData, 0644); err != nil {
		t.Fatalf("Failed to create test RDB file: %s", err)
	}

	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, []interface{}{
		"dir", tmpDir,
		"dbfilename", "dump.rdb",
	})
	close(ch)

	// Should produce a metric with the actual file size
	found := false
	for m := range ch {
		found = true
		d := &dto.Metric{}
		if err := m.Write(d); err == nil && d.GetGauge() != nil {
			metricValue := d.GetGauge().GetValue()
			expectedSize := float64(len(testData))
			if metricValue != expectedSize {
				t.Errorf("Expected metric value %f, got %f", expectedSize, metricValue)
			}
		}
	}
	if !found {
		t.Error("Expected metric to be produced for valid file")
	}
}

func TestExtractRdbFileSizeMetricPathTraversal(t *testing.T) {
	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	// Try path traversal in dbfilename
	e.extractRdbFileSizeMetric(ch, []interface{}{
		"dir", "/tmp",
		"dbfilename", "../../../etc/passwd",
	})
	close(ch)

	// Should not produce any metrics for path traversal attempt
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("Expected 0 metrics for path traversal attempt, got %d", count)
	}
}

func TestExtractRdbFileSizeMetricDirectory(t *testing.T) {
	// Create a temporary directory (not a file)
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}

	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, []interface{}{
		"dir", tmpDir,
		"dbfilename", "subdir",
	})
	close(ch)

	// Should not produce any metrics for a directory
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("Expected 0 metrics for directory, got %d", count)
	}
}

func TestExtractRdbFileSizeMetricLargeFile(t *testing.T) {
	// Create a temporary large file (simulate >2GB scenario with smaller size for testing)
	tmpDir := t.TempDir()
	rdbFile := filepath.Join(tmpDir, "dump.rdb")

	// Create a 10MB file to test large file handling
	largeSize := int64(10 * 1024 * 1024) // 10MB
	f, err := os.Create(rdbFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %s", err)
	}
	if err := f.Truncate(largeSize); err != nil {
		f.Close()
		t.Fatalf("Failed to truncate file: %s", err)
	}
	f.Close()

	e := &Exporter{
		options: Options{
			Namespace: "test",
		},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, []interface{}{
		"dir", tmpDir,
		"dbfilename", "dump.rdb",
	})
	close(ch)

	// Should produce a metric with the correct large file size
	found := false
	for m := range ch {
		found = true
		d := &dto.Metric{}
		if err := m.Write(d); err == nil && d.GetGauge() != nil {
			metricValue := d.GetGauge().GetValue()
			expectedSize := float64(largeSize)
			if metricValue != expectedSize {
				t.Errorf("Expected metric value %f, got %f", expectedSize, metricValue)
			}
		}
	}
	if !found {
		t.Error("Expected metric to be produced for large file")
	}
}

// Made with Bob
