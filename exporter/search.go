package exporter

import (
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractSearchMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	for _, section := range [5]string{"search_version", "search_index", "search_memory", "search_cursors", "search_gc"} {
		info, err := redis.String(doRedisCmd(c, "INFO", section))
		if err != nil {
			log.Errorf("extractSearchMetrics() err: %s", err)
			return
		}
		e.registerSearchMetrics(ch, info)
	}

}

func (e *Exporter) registerSearchMetrics(ch chan<- prometheus.Metric, info string) {
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
