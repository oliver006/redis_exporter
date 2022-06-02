package exporter

import (
	"sync"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var logErrOnce sync.Once

func (e *Exporter) extractLatencyMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	reply, err := redis.Values(doRedisCmd(c, "LATENCY", "LATEST"))
	if err != nil {
		/*
			this can be a little too verbose, see e.g. https://github.com/oliver006/redis_exporter/issues/495
			we're logging this only once as an Error and always as Debugf()
		*/
		logErrOnce.Do(func() {
			log.Errorf("WARNING, LOGGED ONCE ONLY: cmd LATENCY LATEST, err: %s", err)
		})
		log.Debugf("cmd LATENCY LATEST, err: %s", err)
		return
	}

	for _, l := range reply {
		if latencyResult, err := redis.Values(l, nil); err == nil {
			var eventName string
			var spikeLast, spikeDuration, max int64
			if _, err := redis.Scan(latencyResult, &eventName, &spikeLast, &spikeDuration, &max); err == nil {
				spikeDurationSeconds := float64(spikeDuration) / 1e3
				e.registerConstMetricGauge(ch, "latency_spike_last", float64(spikeLast), eventName)
				e.registerConstMetricGauge(ch, "latency_spike_duration_seconds", spikeDurationSeconds, eventName)
			}
		}
	}
}
