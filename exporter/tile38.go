package exporter

import (
	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractTile38Metrics(ch chan<- prometheus.Metric, c redis.Conn) {
	info, err := redis.Strings(doRedisCmd(c, "SERVER"))
	if err != nil {
		log.Errorf("extractTile38Metrics() err: %s", err)
		return
	}

	for i := 0; i < len(info); i += 2 {
		fieldKey := "tile38_" + info[i]
		fieldValue := info[i+1]
		log.Debugf("tile38   key:%s   val:%s", fieldKey, fieldValue)

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}
}
