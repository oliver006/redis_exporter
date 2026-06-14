package exporter

import (
	"strconv"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

var commandLogConfigMetrics = []struct {
	configKey  string
	metricName string
}{
	{"commandlog-execution-slower-than", "commandlog_execution_slower_than"},
	{"commandlog-slow-execution-max-len", "commandlog_slow_execution_max_len"},
	{"commandlog-request-larger-than", "commandlog_request_larger_than"},
	{"commandlog-large-request-max-len", "commandlog_large_request_max_len"},
	{"commandlog-reply-larger-than", "commandlog_reply_larger_than"},
	{"commandlog-large-reply-max-len", "commandlog_large_reply_max_len"},
}

func (e *Exporter) extractCommandLogMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	commandLogTypes := []struct {
		logType string
		metric  string
	}{
		{"slow", "commandlog_slow_length"},
		{"large-request", "commandlog_large_request_length"},
		{"large-reply", "commandlog_large_reply_length"},
	}

	supported := false
	for _, t := range commandLogTypes {
		if reply, err := redis.Int64(doRedisCmd(c, "COMMANDLOG", "LEN", t.logType)); err == nil {
			supported = true
			e.registerConstMetricGauge(ch, t.metric, float64(reply))
		}
	}

	if !supported {
		return
	}

	for _, cfg := range commandLogConfigMetrics {
		reply, err := redis.Strings(doRedisCmd(c, "CONFIG", "GET", cfg.configKey))
		if err != nil || len(reply) != 2 {
			continue
		}
		if val, err := strconv.ParseInt(reply[1], 10, 64); err == nil {
			e.registerConstMetricGauge(ch, cfg.metricName, float64(val))
		}
	}
}
