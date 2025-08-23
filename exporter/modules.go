package exporter

import (
	"log/slog"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func (e *Exporter) extractModulesMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	info, err := redis.String(doRedisCmd(c, "INFO", "MODULES"))
	if err != nil {
		slog.Error("Failed to extract modules metrics", "error", err)
		return
	}

	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		slog.Debug("info", "line", line)

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
			e.registerConstMetricGauge(ch, "module_info", 1,
				strings.Split(module[0], "=")[1],
				strings.Split(module[1], "=")[1],
				strings.Split(module[2], "=")[1],
				strings.Split(module[3], "=")[1],
				strings.Split(module[4], "=")[1],
				strings.Split(module[5], "=")[1],
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
