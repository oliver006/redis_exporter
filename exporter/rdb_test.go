package exporter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func newRdbTestExporter() *Exporter {
	return &Exporter{
		options: Options{Namespace: "test"},
		metricDescriptions: map[string]*prometheus.Desc{
			"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "test metric", nil),
		},
	}
}

func collectRdbSizeMetric(t *testing.T, e *Exporter, config map[string]string) (found bool, value float64) {
	t.Helper()
	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, config)
	close(ch)

	for m := range ch {
		if !strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			continue
		}
		found = true
		d := &dto.Metric{}
		if err := m.Write(d); err == nil && d.GetGauge() != nil {
			value = d.GetGauge().GetValue()
		}
	}
	return found, value
}

func TestExtractRdbFileSizeMetricNoOutput(t *testing.T) {
	tsts := []struct {
		name   string
		config map[string]string
	}{
		{"empty config", map[string]string{}},
		{"missing dir and dbfilename", map[string]string{"maxmemory": "1000000", "timeout": "300"}},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			if found, _ := collectRdbSizeMetric(t, newRdbTestExporter(), tst.config); found {
				t.Errorf("expected no metric, got one")
			}
		})
	}
}

func TestExtractRdbFileSizeMetricNonExistentFile(t *testing.T) {
	found, val := collectRdbSizeMetric(t, newRdbTestExporter(), map[string]string{
		"dir":        "/tmp/nonexistent_redis_dir_12345",
		"dbfilename": "dump.rdb",
	})
	if !found {
		t.Fatal("expected metric to be produced for non-existent file")
	}
	if val != 0 {
		t.Errorf("expected value 0 for non-existent file, got %f", val)
	}
}

func TestExtractRdbFileSizeMetricRegularFile(t *testing.T) {
	tsts := []struct {
		name string
		size int64
	}{
		{"small file", 21},
		{"large file", 10 * 1024 * 1024},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			f, err := os.Create(filepath.Join(tmpDir, "dump.rdb"))
			if err != nil {
				t.Fatalf("failed to create test file: %s", err)
			}
			if err := f.Truncate(tst.size); err != nil {
				f.Close()
				t.Fatalf("failed to truncate file: %s", err)
			}
			f.Close()

			found, val := collectRdbSizeMetric(t, newRdbTestExporter(), map[string]string{
				"dir":        tmpDir,
				"dbfilename": "dump.rdb",
			})
			if !found {
				t.Fatal("expected metric to be produced")
			}
			if val != float64(tst.size) {
				t.Errorf("expected value %d, got %f", tst.size, val)
			}
		})
	}
}

func TestExtractRdbFileSizeMetricUnsafeDbfilename(t *testing.T) {
	tsts := []struct {
		name       string
		dbfilename string
	}{
		{"parent traversal", "../../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
		{"subdir path", "sub/dump.rdb"},
		{"dot", "."},
		{"dotdot", ".."},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			if found, _ := collectRdbSizeMetric(t, newRdbTestExporter(), map[string]string{
				"dir":        "/tmp",
				"dbfilename": tst.dbfilename,
			}); found {
				t.Errorf("expected no metric for unsafe dbfilename %q, got one", tst.dbfilename)
			}
		})
	}
}

func TestExtractRdbFileSizeMetricDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create test directory: %s", err)
	}

	if found, _ := collectRdbSizeMetric(t, newRdbTestExporter(), map[string]string{
		"dir":        tmpDir,
		"dbfilename": "subdir",
	}); found {
		t.Error("expected no metric for a directory, got one")
	}
}

func TestExtractRdbFileSizeMetricIntegration(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	tsts := []struct {
		name       string
		opt        Options
		wantMetric bool
	}{
		{"enabled", Options{Namespace: "test", InclRdbFileSizeMetric: true, ConfigCommandName: "CONFIG"}, true},
		{"disabled", Options{Namespace: "test", InclRdbFileSizeMetric: false, ConfigCommandName: "CONFIG"}, false},
		{"config command disabled", Options{Namespace: "test", InclRdbFileSizeMetric: true, ConfigCommandName: "-"}, false},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			e, err := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), tst.opt)
			if err != nil {
				t.Fatalf("NewRedisExporter() failed: %s", err)
			}

			ch := make(chan prometheus.Metric, 100)
			go func() {
				e.Collect(ch)
				close(ch)
			}()

			found := false
			var value float64
			for m := range ch {
				if !strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
					continue
				}
				found = true
				d := &dto.Metric{}
				if err := m.Write(d); err == nil && d.GetGauge() != nil {
					value = d.GetGauge().GetValue()
				}
			}

			if found != tst.wantMetric {
				t.Errorf("metric present = %v, want %v", found, tst.wantMetric)
			}
			if found && value < 0 {
				t.Errorf("rdb_current_size_bytes should not be negative, got %f", value)
			}
		})
	}
}
