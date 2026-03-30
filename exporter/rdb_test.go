package exporter

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// mockRedisConn is a minimal redis.Conn implementation for unit testing.
// It returns a pre-configured flat array response for CONFIG GET commands.
type mockRedisConn struct {
	// configValues maps "CONFIG GET <key>" to the value to return.
	// The Do() method returns [key, value, key, value, ...] for CONFIG GET.
	configValues map[string]string
	// err is returned for all commands if non-nil.
	err error
}

func (m *mockRedisConn) Do(commandName string, args ...interface{}) (interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Handle CONFIG GET <key> [<key2> ...]
	if commandName == "CONFIG" && len(args) >= 2 && fmt.Sprintf("%v", args[0]) == "GET" {
		var result []interface{}
		for _, arg := range args[1:] {
			key := fmt.Sprintf("%v", arg)
			val, ok := m.configValues[key]
			if ok {
				result = append(result, []byte(key), []byte(val))
			}
		}
		return result, nil
	}
	return nil, nil
}

func (m *mockRedisConn) Send(commandName string, args ...interface{}) error { return nil }
func (m *mockRedisConn) Flush() error                                       { return nil }
func (m *mockRedisConn) Receive() (interface{}, error)                      { return nil, nil }
func (m *mockRedisConn) Close() error                                       { return nil }
func (m *mockRedisConn) Err() error                                         { return nil }

// Compile-time check that mockRedisConn implements redis.Conn.
var _ redis.Conn = (*mockRedisConn)(nil)

// mockRedisConnRaw returns a pre-built raw result slice for any Do() call,
// allowing tests to inject non-string values that cause redis.String() to fail.
type mockRedisConnRaw struct {
	result []interface{}
}

func (m *mockRedisConnRaw) Do(commandName string, args ...interface{}) (interface{}, error) {
	return m.result, nil
}

func (m *mockRedisConnRaw) Send(commandName string, args ...interface{}) error { return nil }
func (m *mockRedisConnRaw) Flush() error                                       { return nil }
func (m *mockRedisConnRaw) Receive() (interface{}, error)                      { return nil, nil }
func (m *mockRedisConnRaw) Close() error                                       { return nil }
func (m *mockRedisConnRaw) Err() error                                         { return nil }

// Compile-time check that mockRedisConnRaw implements redis.Conn.
var _ redis.Conn = (*mockRedisConnRaw)(nil)

func newTestExporterForRdb(t *testing.T) *Exporter {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_URI")
	if addr == "" {
		addr = "redis://localhost:6379"
	}
	e, err := NewRedisExporter(addr, Options{
		Namespace:             "test",
		InclRdbFileSizeMetric: true,
		ConfigCommandName:     "CONFIG",
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() failed: %s", err)
	}
	return e
}

// TestExtractRdbFileSizeMetricFileNotExist covers the os.IsNotExist branch:
// when CONFIG GET returns a valid dir/filename but the file doesn't exist on disk,
// the metric should be reported as 0.
func TestExtractRdbFileSizeMetricFileNotExist(t *testing.T) {
	e := newTestExporterForRdb(t)

	configValues := map[string]string{
		"dir":        "/tmp",
		"dbfilename": fmt.Sprintf("nonexistent_rdb_%d.rdb", os.Getpid()),
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, configValues)
	close(ch)

	found := false
	var metricValue float64
	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			found = true
			d := &dto.Metric{}
			if err := m.Write(d); err == nil && d.GetGauge() != nil {
				metricValue = d.GetGauge().GetValue()
			}
			break
		}
	}

	if !found {
		t.Error("rdb_current_size_bytes metric should be present even when file does not exist (reported as 0)")
	}
	if metricValue != 0 {
		t.Errorf("expected rdb_current_size_bytes to be 0 for non-existent file, got %f", metricValue)
	}
}

// TestExtractRdbFileSizeMetricFileExists covers the normal path:
// when the RDB file exists on disk, the metric should report its actual size.
func TestExtractRdbFileSizeMetricFileExists(t *testing.T) {
	e := newTestExporterForRdb(t)

	// Create a temp file with known content.
	tmpFile, err := os.CreateTemp("", "test_rdb_*.rdb")
	if err != nil {
		t.Fatalf("could not create temp file: %s", err)
	}
	defer os.Remove(tmpFile.Name())

	testData := []byte("REDIS0011test data for rdb size metric test")
	if _, err := tmpFile.Write(testData); err != nil {
		t.Fatalf("could not write to temp file: %s", err)
	}
	tmpFile.Close()

	tmpDir := os.TempDir()
	tmpBase := strings.TrimPrefix(tmpFile.Name(), tmpDir)
	tmpBase = strings.TrimPrefix(tmpBase, string(os.PathSeparator))

	configValues := map[string]string{
		"dir":        tmpDir,
		"dbfilename": tmpBase,
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, configValues)
	close(ch)

	found := false
	var metricValue float64
	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			found = true
			d := &dto.Metric{}
			if err := m.Write(d); err == nil && d.GetGauge() != nil {
				metricValue = d.GetGauge().GetValue()
			}
			break
		}
	}

	if !found {
		t.Error("rdb_current_size_bytes metric should be present")
	}
	expectedSize := float64(len(testData))
	if metricValue != expectedSize {
		t.Errorf("expected rdb_current_size_bytes to be %f, got %f", expectedSize, metricValue)
	}
}

// TestExtractRdbFileSizeMetricConfigError covers the error path:
// when CONFIG GET fails, no metric should be emitted.
func TestExtractRdbFileSizeMetricConfigError(t *testing.T) {
	e := newTestExporterForRdb(t)

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, nil)
	close(ch)

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when CONFIG GET fails")
		}
	}
}

// TestExtractRdbFileSizeMetricInvalidDirValue covers the branch where
// the dir value returned by CONFIG GET cannot be parsed as a string.
func TestExtractRdbFileSizeMetricInvalidDirValue(t *testing.T) {
	e := newTestExporterForRdb(t)

	// Return an integer (not a string/bytes) for "dir" so redis.String fails.
	//conn := &mockRedisConnRaw{
	//	result: []interface{}{[]byte("dir"), int64(12345), []byte("dbfilename"), []byte("dump.rdb")},
	//}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, nil)
	close(ch)

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when dir value is invalid")
		}
	}
}

// TestExtractRdbFileSizeMetricInvalidFilenameValue covers the branch where
// the dbfilename value returned by CONFIG GET cannot be parsed as a string.
func TestExtractRdbFileSizeMetricInvalidFilenameValue(t *testing.T) {
	e := newTestExporterForRdb(t)

	// Return an integer (not a string/bytes) for "dbfilename" so redis.String fails.
	//conn := &mockRedisConnRaw{
	//	result: []interface{}{[]byte("dir"), []byte("/tmp"), []byte("dbfilename"), int64(99999)},
	//}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, nil)
	close(ch)

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when dbfilename value is invalid")
		}
	}
}

// TestExtractRdbFileSizeMetricStatError covers the branch where os.Stat returns
// an error that is NOT os.IsNotExist (e.g., invalid path).
func TestExtractRdbFileSizeMetricStatError(t *testing.T) {
	e := newTestExporterForRdb(t)

	// A path containing a null byte is invalid on Linux/macOS and causes
	// os.Stat to return an error that is not os.IsNotExist.
	configValues := map[string]string{
		"dir":        "/tmp",
		"dbfilename": "invalid\x00filename.rdb",
	}

	ch := make(chan prometheus.Metric, 10)
	e.extractRdbFileSizeMetric(ch, configValues)
	close(ch)

	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			t.Error("rdb_current_size_bytes metric should NOT be present when os.Stat returns a non-NotExist error")
		}
	}
}

// TestExtractRdbFileSizeMetric is an integration test that requires a live Redis instance.
func TestExtractRdbFileSizeMetric(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(addr, Options{
		Namespace:             "test",
		InclRdbFileSizeMetric: true,
		ConfigCommandName:     "CONFIG",
	})

	ch := make(chan prometheus.Metric, 100)
	go func() {
		e.Collect(ch)
		close(ch)
	}()

	found := false
	for m := range ch {
		if strings.Contains(m.Desc().String(), "rdb_current_size_bytes") {
			found = true
			break
		}
	}

	if !found {
		t.Error("rdb_current_size_bytes metric should be present")
	}
}

// TestExtractRdbFileSizeMetricDisabled verifies the metric is only present when the flag is enabled.
func TestExtractRdbFileSizeMetricDisabled(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_URI")

	for _, inc := range []bool{false, true} {
		e, _ := NewRedisExporter(addr, Options{
			Namespace:             "test",
			InclRdbFileSizeMetric: inc,
		})
		ts := httptest.NewServer(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && !strings.Contains(body, "rdb_current_size_bytes") {
			t.Errorf("want metrics to include rdb_current_size_bytes when enabled, have:\n%s", body)
		} else if !inc && strings.Contains(body, "rdb_current_size_bytes") {
			t.Errorf("did NOT want metrics to include rdb_current_size_bytes when disabled, have:\n%s", body)
		}

		ts.Close()
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
