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

	lines := strings.SplitSeq(info, "\r\n")
	for line := range lines {
		log.Debugf("info: %s", line)

		split := strings.Split(line, ":")
		if len(split) != 2 {
			continue
		}

		if split[0] == "module" {
			// module format: 'module:name=<module-name>,ver=21005,api=1,filters=0,usedby=[],using=[],options=[]'
			module := strings.Split(split[1], ",")
			if len(module) != 7 {
				continue
			}
			extractModuleVal := func(s string) string {
				parts := strings.SplitN(s, "=", 2)
				if len(parts) != 2 {
					return ""
				}
				return parts[1]
			}
			e.registerConstMetricGauge(ch, "module_info", 1,
				extractModuleVal(module[0]),
				extractModuleVal(module[1]),
				extractModuleVal(module[2]),
				extractModuleVal(module[3]),
				extractModuleVal(module[4]),
				extractModuleVal(module[5]),
			)
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
