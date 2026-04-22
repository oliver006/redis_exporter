package exporter

import (
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractRdbFileSizeMetric(ch chan<- prometheus.Metric, configMap map[string]string) {
	if len(configMap) == 0 {
		log.Debugf("Config is empty, cannot extract RDB file size")
		return
	}

	dir := configMap["dir"]
	dbfilename := configMap["dbfilename"]
	if dir == "" || dbfilename == "" {
		log.Debugf("Failed to find 'dir' or 'dbfilename' in config")
		return
	}

	if dbfilename != filepath.Base(dbfilename) || dbfilename == "." || dbfilename == ".." {
		log.Warnf("dbfilename %q is not a bare filename, skipping RDB size metric", dbfilename)
		return
	}

	rdbPath := filepath.Join(dir, dbfilename)
	log.Debugf("RDB file path: %s", rdbPath)

	fileInfo, err := os.Stat(rdbPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("RDB file does not exist: %s", rdbPath)

			// File doesn't exist, report 0
			e.registerConstMetricGauge(ch, "rdb_current_size_bytes", 0)
			return
		}
		if os.IsPermission(err) {
			log.Warnf("Permission denied accessing RDB file: %s", rdbPath)
			return
		}
		log.Warnf("Failed to stat RDB file %s: %s", rdbPath, err)
		return
	}

	// Verify it's a regular file
	if !fileInfo.Mode().IsRegular() {
		log.Warnf("RDB path is not a regular file: %s (mode: %s)", rdbPath, fileInfo.Mode())
		return
	}

	e.registerConstMetricGauge(ch, "rdb_current_size_bytes", float64(fileInfo.Size()))

	log.Debugf("RDB file size: %d bytes", fileInfo.Size())
}
