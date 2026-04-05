package exporter

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractRdbFileSizeMetric(ch chan<- prometheus.Metric, config []interface{}) {
	if len(config) == 0 {
		log.Debugf("Config is empty, cannot extract RDB file size")
		return
	}

	// Validate config has even number of elements (key-value pairs)
	if len(config)%2 != 0 {
		log.Warnf("Invalid config format: odd number of elements (%d)", len(config))
		return
	}

	// Parse config to find dir and dbfilename
	configMap := make(map[string]string)
	for i := 0; i < len(config); i += 2 {
		key, err := redis.String(config[i], nil)
		if err != nil {
			log.Warnf("Failed to parse config key at index %d: %s", i, err)
			continue
		}
		value, err := redis.String(config[i+1], nil)
		if err != nil {
			log.Warnf("Failed to parse config value for key '%s': %s", key, err)
			continue
		}
		configMap[key] = value
	}

	dir, dirOk := configMap["dir"]
	dbfilename, dbfilenameOk := configMap["dbfilename"]

	if !dirOk || !dbfilenameOk {
		log.Warnf("Failed to find 'dir' or 'dbfilename' in config")
		return
	}

	// Basic path validation to prevent directory traversal
	if strings.Contains(dbfilename, "..") {
		log.Warnf("Invalid dbfilename contains '..': %s", dbfilename)
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

	fileSize := float64(fileInfo.Size())
	log.Debugf("RDB file size: %d bytes", fileInfo.Size())
	e.registerConstMetricGauge(ch, "rdb_current_size_bytes", fileSize)
}
