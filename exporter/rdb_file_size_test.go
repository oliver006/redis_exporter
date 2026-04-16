package exporter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestExtractRDBFileSizeMetrics(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a mock RDB file
	rdbFile := filepath.Join(tempDir, "dump.rdb")
	expectedSize := int64(1024)
	if err := os.WriteFile(rdbFile, make([]byte, expectedSize), 0644); err != nil {
		t.Fatalf("Failed to create mock RDB file: %v", err)
	}

	tests := []struct {
		name       string
		config     map[string]string
		expectSize bool
	}{
		{
			name: "valid config with RDB file",
			config: map[string]string{
				"dir":        tempDir,
				"dbfilename": "dump.rdb",
			},
			expectSize: true,
		},
		{
			name: "missing dir config",
			config: map[string]string{
				"dbfilename": "dump.rdb",
			},
			expectSize: false,
		},
		{
			name: "missing dbfilename config",
			config: map[string]string{
				"dir": tempDir,
			},
			expectSize: false,
		},
		{
			name: "non-existent RDB file",
			config: map[string]string{
				"dir":        tempDir,
				"dbfilename": "nonexistent.rdb",
			},
			expectSize: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Exporter{}
			ch := make(chan prometheus.Metric, 1)

			e.extractRDBFileSizeMetrics(ch, tt.config)

			if tt.expectSize {
				select {
				case metric := <-ch:
					desc := metric.Desc().String()
					if !containsSubstring(desc, "rdb_current_file_size_bytes") {
						t.Errorf("Expected rdb_current_file_size_bytes metric, got: %s", desc)
					}
				default:
					t.Error("Expected a metric but got none")
				}
			} else {
				select {
				case <-ch:
					t.Error("Expected no metric but got one")
				default:
					// This is expected - no metric should be produced
				}
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
