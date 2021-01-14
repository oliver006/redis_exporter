package exporter

import (
	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func (e *Exporter) extractSlowLogMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	if reply, err := redis.Int64(doRedisCmd(c, "SLOWLOG", "LEN")); err == nil {
		e.registerConstMetricGauge(ch, "slowlog_length", float64(reply))
	}

	values, err := redis.Values(doRedisCmd(c, "SLOWLOG", "GET", "1"))
	if err != nil {
		return
	}

	var slowlogLastID int64
	var lastSlowExecutionDurationSeconds float64

	if len(values) > 0 {
		if values, err = redis.Values(values[0], err); err == nil && len(values) > 0 {
			slowlogLastID = values[0].(int64)
			if len(values) > 2 {
				lastSlowExecutionDurationSeconds = float64(values[2].(int64)) / 1e6
			}
		}
	}

	e.registerConstMetricGauge(ch, "slowlog_last_id", float64(slowlogLastID))
	e.registerConstMetricGauge(ch, "last_slow_execution_duration_seconds", lastSlowExecutionDurationSeconds)
}
