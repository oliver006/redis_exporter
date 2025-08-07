package exporter

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
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
		slog.Debug("couldn't parse", "fieldValue", fieldValue, "error", err)
		return
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
	var desc *prometheus.Desc
	if len(labelValues) == 0 {
		desc = e.createMetricDescription(metric, nil)
	} else {
		desc = e.mustFindMetricDescription(metric)
	}

	m, err := prometheus.NewConstMetric(desc, valType, val, labelValues...)
	if err != nil {
		slog.Debug("registerConstMetric err", "metric", metric, "value", val, "error", err)
		return
	}

	ch <- m
}

func (e *Exporter) registerConstSummary(ch chan<- prometheus.Metric, metric string, count uint64, sum float64, latencyMap map[float64]float64, labelValues ...string) {
	// Create a constant summary from values we got from a 3rd party telemetry system.
	summary := prometheus.MustNewConstSummary(
		e.mustFindMetricDescription(metric),
		count, sum,
		latencyMap,
		labelValues...,
	)
	ch <- summary
}

func (e *Exporter) registerConstHistogram(ch chan<- prometheus.Metric, metric string, count uint64, sum float64, buckets map[float64]uint64, labelValues ...string) {
	histogram := prometheus.MustNewConstHistogram(
		e.mustFindMetricDescription(metric),
		count, sum,
		buckets,
		labelValues...,
	)
	ch <- histogram
}

func (e *Exporter) mustFindMetricDescription(metricName string) *prometheus.Desc {
	description, found := e.metricDescriptions[metricName]
	if !found {
		panic(fmt.Sprintf("couldn't find metric description for %s", metricName))
	}
	return description
}

func (e *Exporter) createMetricDescription(metricName string, labels []string) *prometheus.Desc {
	if desc, found := e.metricDescriptions[metricName]; found {
		return desc
	}
	d := newMetricDescr(e.options.Namespace, metricName, metricName+" metric", labels)
	e.metricDescriptions[metricName] = d
	return d
}
