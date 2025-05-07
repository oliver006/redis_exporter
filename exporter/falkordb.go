package exporter

import (
	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractFalkorDBMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	graphList, err := redis.Values(doRedisCmd(c, "GRAPH.LIST"))
	if err != nil {
		log.Errorf("extractFalkorDBMetrics() err: %s", err)
		return
	}

	graphCount := len(graphList)

	e.registerConstMetricGauge(ch, "falkordb_total_graph_count", float64(graphCount))
}
