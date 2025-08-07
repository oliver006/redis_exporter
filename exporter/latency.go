package exporter

import (
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"sync"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	logLatestErrOnce, logHistogramErrOnce sync.Once

	extractUsecRegexp = regexp.MustCompile(`(?m)^cmdstat_([a-zA-Z0-9\|]+):.*usec=([0-9]+).*$`)
)

func (e *Exporter) extractLatencyMetrics(ch chan<- prometheus.Metric, infoAll string, c redis.Conn) {
	e.extractLatencyLatestMetrics(ch, c)
	e.extractLatencyHistogramMetrics(ch, infoAll, c)
}

func (e *Exporter) extractLatencyLatestMetrics(outChan chan<- prometheus.Metric, redisConn redis.Conn) {
	reply, err := redis.Values(doRedisCmd(redisConn, "LATENCY", "LATEST"))
	if err != nil {
		/*
			this can be a little too verbose, see e.g. https://github.com/oliver006/redis_exporter/issues/495
			we're logging this only once as an Error and always as Debugf()
		*/
		logLatestErrOnce.Do(func() {
			slog.Error("WARNING, LOGGED ONCE ONLY: cmd LATENCY LATEST", "error", err)
		})
		slog.Debug("cmd LATENCY LATEST", "error", err)
		return
	}

	for _, l := range reply {
		if latencyResult, err := redis.Values(l, nil); err == nil {
			var eventName string
			var spikeLast, spikeDuration, maxLatency int64
			if _, err := redis.Scan(latencyResult, &eventName, &spikeLast, &spikeDuration, &maxLatency); err == nil {
				spikeDurationSeconds := float64(spikeDuration) / 1e3
				e.registerConstMetricGauge(outChan, "latency_spike_last", float64(spikeLast), eventName)
				e.registerConstMetricGauge(outChan, "latency_spike_duration_seconds", spikeDurationSeconds, eventName)
			}
		}
	}
}

/*
https://redis.io/docs/latest/commands/latency-histogram/
*/
func (e *Exporter) extractLatencyHistogramMetrics(outChan chan<- prometheus.Metric, infoAll string, redisConn redis.Conn) {
	reply, err := redis.Values(doRedisCmd(redisConn, "LATENCY", "HISTOGRAM"))
	if err != nil {
		logHistogramErrOnce.Do(func() {
			slog.Error("WARNING, LOGGED ONCE ONLY: cmd LATENCY HISTOGRAM", "error", err)
		})
		slog.Debug("cmd LATENCY HISTOGRAM", "error", err)
		return
	}

	for i := 0; i < len(reply); i += 2 {
		cmd, _ := redis.String(reply[i], nil)
		details, _ := redis.Values(reply[i+1], nil)

		var totalCalls uint64
		var bucketInfo []uint64

		if _, err := redis.Scan(details, nil, &totalCalls, nil, &bucketInfo); err != nil {
			break
		}

		buckets := map[float64]uint64{}

		for j := 0; j < len(bucketInfo); j += 2 {
			usec := float64(bucketInfo[j])
			count := bucketInfo[j+1]
			buckets[usec] = count
		}

		totalUsecs := extractTotalUsecForCommand(infoAll, cmd)

		e.createMetricDescription("commands_latencies_usec", []string{"cmd"})
		e.registerConstHistogram(outChan, "commands_latencies_usec", totalCalls, float64(totalUsecs), buckets, cmd)
	}
}

func extractTotalUsecForCommand(infoAll string, cmd string) uint64 {
	total := uint64(0)

	matches := extractUsecRegexp.FindAllStringSubmatch(infoAll, -1)
	for _, match := range matches {
		if !strings.HasPrefix(match[1], cmd) {
			continue
		}

		usecs, err := strconv.ParseUint(match[2], 10, 0)
		if err != nil {
			slog.Warn("Unable to parse uint from string", "string", match[2], "error", err)
			continue
		}

		total += usecs
	}

	return total
}
