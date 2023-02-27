package exporter

import (
	"regexp"
	"strconv"
	"strings"

	"sync"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var logLatestErrOnce, logHistogramErrOnce sync.Once

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
				e.registerConstMetricGauge(outChan, "latency_spike_last", float64(spikeLast), eventName)
				e.registerConstMetricGauge(outChan, "latency_spike_duration_seconds", spikeDurationSeconds, eventName)
			}
		}
	}
}

func (e *Exporter) extractLatencyHistogramMetrics(outChan chan<- prometheus.Metric, infoAll string, redisConn redis.Conn) {
	reply, err := redis.Values(doRedisCmd(redisConn, "LATENCY", "HISTOGRAM"))
	if err != nil {
		logHistogramErrOnce.Do(func() {
			log.Errorf("WARNING, LOGGED ONCE ONLY: cmd LATENCY HISTOGRAM, err: %s", err)
		})
		log.Debugf("cmd LATENCY HISTOGRAM, err: %s", err)
		return
	}

	for i := 0; i < len(reply); i += 2 {

		cmd := string(reply[i].([]byte))
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

		labelValues := []string{"cmd"}
		e.registerConstHistogram(outChan, "latency_usec", labelValues, totalCalls, float64(totalUsecs), buckets, cmd)
	}
}

func extractTotalUsecForCommand(infoAll string, cmd string) uint64 {
	sanitizedCmd := strings.ReplaceAll(cmd, "|", "\\|")
	usecRegexp := regexp.MustCompile(`(?m)^cmdstat_` + sanitizedCmd + `(?:|.*)?:.*usec=([0-9]+).*$`)

	matches := usecRegexp.FindAllStringSubmatch(infoAll, -1)

	if len(matches) == 0 {
		log.Errorf("Unable to extract total latency for cmd=%s", cmd)
		return 0
	}

	total := uint64(0)

	for _, match := range matches {
		if len(match) < 2 {
			log.Warnf("Unable to match usec for cmd=%s", cmd)
			continue
		}

		usecs, err := strconv.ParseUint(match[1], 10, 0)
		if err != nil {
			log.Warnf("Unable to parse uint from string \"%s\": %v", match[1], err)
		}

		total += usecs
	}

	return total
}
