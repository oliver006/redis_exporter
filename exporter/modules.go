package exporter

import (
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractModuleMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	info, err := redis.String(doRedisCmd(c, "INFO", "MODULES"))
	if err != nil {
		log.Errorf("extractSearchMetrics() err: %s", err)
		return
	}

	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		log.Debugf("info: %s", line)

		split := strings.Split(line, ":")
		if len(split) != 2 {
			continue
		}
		fieldKey := split[0]
		fieldValue := split[1]

		if !e.includeMetric(fieldKey) {
			continue
		}
		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}
}
