package exporter

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var metricNameRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func sanitizeMetricName(n string) string {
	return metricNameRE.ReplaceAllString(n, "_")
}

func newMetricDescr(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName(namespace, "", metricName), docString, labels, nil)
}

func (e *Exporter) includeMetric(s string) bool {
	if strings.HasPrefix(s, "db") || strings.HasPrefix(s, "cmdstat_") || strings.HasPrefix(s, "cluster_") {
		return true
	}
	if _, ok := e.metricMapGauges[s]; ok {
		return true
	}

	_, ok := e.metricMapCounters[s]
	return ok
}

func (e *Exporter) parseAndRegisterConstMetric(ch chan<- prometheus.Metric, fieldKey, fieldValue string) {
	orgMetricName := sanitizeMetricName(fieldKey)
	metricName := orgMetricName
	if newName, ok := e.metricMapGauges[metricName]; ok {
		metricName = newName
	} else {
		if newName, ok := e.metricMapCounters[metricName]; ok {
			metricName = newName
		}
	}

	var err error
	var val float64

	switch fieldValue {

	case "ok", "true":
		val = 1

	case "err", "fail", "false":
		val = 0

	default:
		val, err = strconv.ParseFloat(fieldValue, 64)

	}
	if err != nil {
		log.Debugf("couldn't parse %s, err: %s", fieldValue, err)
	}

	t := prometheus.GaugeValue
	if e.metricMapCounters[orgMetricName] != "" {
		t = prometheus.CounterValue
	}

	switch metricName {
	case "latest_fork_usec":
		metricName = "latest_fork_seconds"
		val = val / 1e6
	}

	e.registerConstMetric(ch, metricName, val, t)
}

func (e *Exporter) registerConstMetricGauge(ch chan<- prometheus.Metric, metric string, val float64, labels ...string) {
	e.registerConstMetric(ch, metric, val, prometheus.GaugeValue, labels...)
}

func (e *Exporter) registerConstMetric(ch chan<- prometheus.Metric, metric string, val float64, valType prometheus.ValueType, labelValues ...string) {
	description := e.findOrCreateMetricDescription(metric, labelValues)

	if m, err := prometheus.NewConstMetric(description, valType, val, labelValues...); err == nil {
		ch <- m
	}
}

func (e *Exporter) registerConstSummary(ch chan<- prometheus.Metric, metric string, labelValues []string, count uint64, sum float64, latencyMap map[float64]float64, cmd string) {
	description := e.findOrCreateMetricDescription(metric, labelValues)

	// Create a constant summary from values we got from a 3rd party telemetry system.
	summary := prometheus.MustNewConstSummary(
		description,
		count, sum,
		latencyMap,
		cmd,
	)
	ch <- summary
}

func (e *Exporter) registerConstHistogram(ch chan<- prometheus.Metric, metric string, labelValues []string, count uint64, sum float64, buckets map[float64]uint64, cmd string) {
	description := e.findOrCreateMetricDescription(metric, labelValues)

	histogram := prometheus.MustNewConstHistogram(
		description,
		count, sum,
		buckets,
		cmd,
	)
	ch <- histogram
}

func (e *Exporter) findOrCreateMetricDescription(metricName string, labels []string) *prometheus.Desc {
	description, found := e.metricDescriptions[metricName]

	if !found {
		description = newMetricDescr(e.options.Namespace, metricName, metricName+" metric", labels)
		e.metricDescriptions[metricName] = description
	}

	return description
}
