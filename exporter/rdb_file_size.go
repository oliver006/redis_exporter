package exporter

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// extractRDBFileSizeMetrics extracts the current RDB file size metric
// by combining the dir and dbfilename config values and stat'ing the file
func (e *Exporter) extractRDBFileSizeMetrics(ch chan<- prometheus.Metric, config map[string]string) {
	// Get the RDB file path components from config
	dir, hasDir := config["dir"]
	dbFilename, hasDbFilename := config["dbfilename"]

	if !hasDir || !hasDbFilename {
		log.Debugf("Cannot extract RDB file size: missing dir=%v or dbfilename=%v", hasDir, hasDbFilename)
		return
	}

	// Construct the full path to the RDB file
	// Handle the case where dir might be relative or absolute
	rdbPath := filepath.Join(dir, dbFilename)

	// Check if the file exists and get its size
	fileInfo, err := os.Stat(rdbPath)
	if err != nil {
		// File might not exist during initial startup or if RDB persistence is disabled
		log.Debugf("Could not stat RDB file at %s: %v", rdbPath, err)
		return
	}

	if fileInfo.IsDir() {
		log.Debugf("RDB path %s is a directory, not a file", rdbPath)
		return
	}

	// Register the RDB file size metric
	fileSize := float64(fileInfo.Size())
	e.registerConstMetricGauge(ch, "rdb_current_file_size_bytes", fileSize)
	log.Debugf("RDB file size metric: %s = %d bytes", rdbPath, fileInfo.Size())
}
