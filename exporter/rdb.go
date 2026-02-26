package exporter

import (
	"os"
	"path/filepath"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractRdbFileSizeMetric(ch chan<- prometheus.Metric, c redis.Conn) {
	// Get RDB directory - CONFIG GET returns an array [key, value]
	dirResult, err := redis.Values(doRedisCmd(c, e.options.ConfigCommandName, "GET", "dir"))
	if err != nil {
		log.Debugf("Failed to get RDB directory from CONFIG GET dir: %s", err)
		return
	}
	if len(dirResult) < 2 {
		log.Debugf("CONFIG GET dir returned unexpected result: %v", dirResult)
		return
	}
	dir, err := redis.String(dirResult[1], nil)
	if err != nil {
		log.Debugf("Failed to parse RDB directory: %s", err)
		return
	}

	// Get RDB filename - CONFIG GET returns an array [key, value]
	dbfilenameResult, err := redis.Values(doRedisCmd(c, e.options.ConfigCommandName, "GET", "dbfilename"))
	if err != nil {
		log.Debugf("Failed to get RDB filename from CONFIG GET dbfilename: %s", err)
		return
	}
	if len(dbfilenameResult) < 2 {
		log.Debugf("CONFIG GET dbfilename returned unexpected result: %v", dbfilenameResult)
		return
	}
	dbfilename, err := redis.String(dbfilenameResult[1], nil)
	if err != nil {
		log.Debugf("Failed to parse RDB filename: %s", err)
		return
	}

	// Construct full path
	rdbPath := filepath.Join(dir, dbfilename)
	log.Debugf("RDB file path: %s", rdbPath)

	// Get file size
	fileInfo, err := os.Stat(rdbPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("RDB file does not exist: %s", rdbPath)
			// File doesn't exist, report 0
			e.registerConstMetricGauge(ch, "rdb_current_size_bytes", 0)
			return
		}
		log.Debugf("Failed to stat RDB file %s: %s", rdbPath, err)
		return
	}

	fileSize := float64(fileInfo.Size())
	log.Debugf("RDB file size: %d bytes", fileInfo.Size())
	e.registerConstMetricGauge(ch, "rdb_current_size_bytes", fileSize)
}

// Made with Bob
