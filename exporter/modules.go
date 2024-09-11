package exporter

import (
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractModulesMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	info, err := redis.String(doRedisCmd(c, "INFO", "MODULES"))
	if err != nil {
		log.Errorf("extractSearchMetrics() err: %s", err)
		return
	}

	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		log.Debugf("info: %s", line)

		split := strings.Split(line, ":")
		switch {
		case split[0] == "module":
			module := strings.Split(split[1], ",")
			e.registerConstMetricGauge(ch, "module_info", 1,
				// response format: 'module:name=search,ver=21005,api=1,filters=0,usedby=[],using=[ReJSON],options=[handle-io-errors]'
				strings.Split(module[0], "=")[1],
				strings.Split(module[1], "=")[1],
				strings.Split(module[2], "=")[1],
				strings.Split(module[3], "=")[1],
				strings.Split(module[4], "=")[1],
				strings.Split(module[5], "=")[1],
			)
		case split[0] == "search_version":
			e.registerConstMetricGauge(ch, "search_version", 1, split[1])
		case len(split) != 2:
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
