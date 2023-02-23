package exporter

import (
	"sync"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var logLatestErrOnce, logHistogramErrOnce sync.Once

func (e *Exporter) extractLatencyMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	extractLatencyLatestMetrics(e, ch, c)
	extractLatencyHistogramMetrics(e, ch, c)
}

func extractLatencyLatestMetrics(exporter *Exporter, outChan chan<- prometheus.Metric, redisConn redis.Conn) {
	reply, err := redis.Values(doRedisCmd(redisConn, "LATENCY", "LATEST"))
	if err != nil {
		/*
			this can be a little too verbose, see e.g. https://github.com/oliver006/redis_exporter/issues/495
			we're logging this only once as an Error and always as Debugf()
		*/
		logLatestErrOnce.Do(func() {
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
				exporter.registerConstMetricGauge(outChan, "latency_spike_last", float64(spikeLast), eventName)
				exporter.registerConstMetricGauge(outChan, "latency_spike_duration_seconds", spikeDurationSeconds, eventName)
			}
		}
	}
}

func extractLatencyHistogramMetrics(exporter *Exporter, outChan chan<- prometheus.Metric, redisConn redis.Conn) {
	reply, err := redis.Values(doRedisCmd(redisConn, "LATENCY", "HISTOGRAM"))
	if err != nil {
		logHistogramErrOnce.Do(func() {
			log.Errorf("WARNING, LOGGED ONCE ONLY: cmd LATENCY HISTOGRAM, err: %s", err)
		})
		log.Debugf("cmd LATENCY HISTOGRAM, err: %s", err)
		return
	}

	type cmdDetails struct {
		cmd     string
		details []interface{}
	}

	cmdCount := len(reply) / 2
	details := make([]cmdDetails, cmdCount)

	for i := 0; i < cmdCount; i++ {
		cmd := string(reply[i*2].([]byte))
		det, _ := redis.Values(reply[i*2+1], nil)

		details[i].cmd = cmd
		details[i].details = det
	}

	for _, detail := range details {
		var totalCalls uint64
		var bucketInfo []uint64

		buckets := make(map[float64]uint64)

		if _, err := redis.Scan(detail.details, nil, &totalCalls, nil, &bucketInfo); err != nil {
			break
		}

		for j := 0; j < len(bucketInfo); j += 2 {
			usec := float64(bucketInfo[j])
			count := bucketInfo[j+1]
			buckets[usec] = count
		}

		sumEstimated := estimateSum(buckets)

		labelValues := []string{"cmd"}
		exporter.registerConstHistogram(outChan, "latency_usec", labelValues, totalCalls, sumEstimated, buckets, detail.cmd)
	}
}

func estimateSum(buckets map[float64]uint64) float64 {
	// This should ideally be the total time as reported by info commandstats.
	// Estimated here instead of querying redis again.

	sum := .0
	counted := uint64(0)

	for usec, count := range buckets {
		bucketCount := count - counted
		bucketAverage := usec * .75

		sum += float64(bucketCount) * bucketAverage

		counted = count
	}

	return sum
}
