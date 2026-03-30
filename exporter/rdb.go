package exporter

import (
	"os"
	"path/filepath"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractRdbFileSizeMetric(ch chan<- prometheus.Metric, config []interface{}) {
	if len(config) == 0 {
		log.Debugf("Config is empty, cannot extract RDB file size")
		return
	}

	// Parse config to find dir and dbfilename
	configMap := make(map[string]string)
	for i := 0; i < len(config); i += 2 {
		if i+1 >= len(config) {
			break
		}
		key, err := redis.String(config[i], nil)
		if err != nil {
			continue
		}
		value, err := redis.String(config[i+1], nil)
		if err != nil {
			continue
		}
		configMap[key] = value
	}

	dir, dirOk := configMap["dir"]
	dbfilename, dbfilenameOk := configMap["dbfilename"]

	if !dirOk || !dbfilenameOk {
		log.Debugf("Failed to find 'dir' or 'dbfilename' in config")
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
