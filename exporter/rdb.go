package exporter

import (
	"os"
	"path/filepath"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractRdbFileSizeMetric(ch chan<- prometheus.Metric, c redis.Conn) {
	result, err := redis.Values(doRedisCmd(c, e.options.ConfigCommandName, "GET", "dir", "dbfilename"))
	if err != nil || len(result) < 4 {
		log.Debugf("Failed to get RDB config from CONFIG GET dir dbfilename: %s", err)
		return
	}

	dir, err := redis.String(result[1], nil)
	if err != nil {
		log.Debugf("Failed to parse RDB directory: %s", err)
		return
	}
	dbfilename, err := redis.String(result[3], nil)
	if err != nil {
		log.Debugf("Failed to parse RDB filename: %s", err)
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
		log.Debugf("Failed to stat RDB file %s: %s", rdbPath, err)
		return
	}

	fileSize := float64(fileInfo.Size())
	log.Debugf("RDB file size: %d bytes", fileInfo.Size())
	e.registerConstMetricGauge(ch, "rdb_current_size_bytes", fileSize)
}
