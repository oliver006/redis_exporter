package exporter

import (
	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func (e *Exporter) extractCommandLogMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	commandLogTypes := []struct {
		logType string
		metric  string
	}{
		{"slow", "commandlog_slow_length"},
		{"large-request", "commandlog_large_request_length"},
		{"large-reply", "commandlog_large_reply_length"},
	}

	for _, t := range commandLogTypes {
		if reply, err := redis.Int64(doRedisCmd(c, "COMMANDLOG", "LEN", t.logType)); err == nil {
			e.registerConstMetricGauge(ch, t.metric, float64(reply))
		}
	}
}
