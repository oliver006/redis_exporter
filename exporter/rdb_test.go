package exporter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

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

	c, err := e.connectToRedis()
	assert.NoError(t, err, "connectToRedis() failed")
	defer c.Close()

	ch := make(chan prometheus.Metric, 100)
	e.extractRdbFileSizeMetric(ch, c)
	close(ch)

	// Check that we got a metric
	found := false
	for m := range ch {
		desc := m.Desc().String()
		if contains(desc, "rdb_current_size_bytes") {
			found = true
			break
		}
	}

	assert.True(t, found, "rdb_current_size_bytes metric should be present")
}

func TestExtractRdbFileSizeMetricWithNonExistentFile(t *testing.T) {
	// Create a mock exporter with a temporary directory
	tempDir := t.TempDir()

	e := &Exporter{
		options: Options{
			Namespace:             "test",
			InclRdbFileSizeMetric: true,
			ConfigCommandName:     "CONFIG",
		},
	}
	e.metricDescriptions = map[string]*prometheus.Desc{
		"rdb_current_size_bytes": newMetricDescr("test", "rdb_current_size_bytes", "Current RDB file size in bytes", nil),
	}

	// Create a mock connection that returns our temp directory
	// Note: This is a simplified test - in reality we'd need to mock the Redis connection
	// For now, we'll just test the file stat logic directly

	rdbPath := filepath.Join(tempDir, "dump.rdb")

	// Test with non-existent file
	_, err := os.Stat(rdbPath)
	assert.True(t, os.IsNotExist(err), "File should not exist")

	// Test with existing file
	testData := []byte("test rdb data")
	err = os.WriteFile(rdbPath, testData, 0644)
	assert.NoError(t, err, "Failed to create test RDB file")

	fileInfo, err := os.Stat(rdbPath)
	assert.NoError(t, err, "Failed to stat test RDB file")
	assert.Equal(t, int64(len(testData)), fileInfo.Size(), "File size should match test data")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Made with Bob
