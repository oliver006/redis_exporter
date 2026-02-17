package exporter

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestSanitizeMetricName(t *testing.T) {
	tsts := map[string]string{
		"cluster_stats_messages_auth-req_received": "cluster_stats_messages_auth_req_received",
		"cluster_stats_messages_auth_req_received": "cluster_stats_messages_auth_req_received",
	}

	for m, want := range tsts {
		if got := sanitizeMetricName(m); got != want {
			t.Errorf("sanitizeMetricName( %s ) error, want: %s, got: %s", m, want, got)
		}
	}
}

func TestRegisterConstHistogram(t *testing.T) {
	metricName := "foo"
	for _, inc := range []bool{false, true} {
		exp := getTestExporter()
		exp.options.AppendInstanceRoleLabel = inc
		ch := make(chan prometheus.Metric)
		go func() {
			exp.createMetricDescription(metricName, []string{"test"})
			exp.registerConstHistogram(ch, metricName, 12, .24, map[float64]uint64{}, "test")
			close(ch)
		}()

		for m := range ch {
			if strings.Contains(m.Desc().String(), metricName) {
				if inc && !strings.Contains(m.Desc().String(), "instance_role") {
					t.Errorf("want metrics to include instance_role label, have:\n%s", m.Desc().String())
				}
				if !inc && strings.Contains(m.Desc().String(), "instance_role") {
					t.Errorf("did NOT want metrics to include instance_role label, have:\n%s", m.Desc().String())
				}
				continue
			}
			t.Errorf("Histogram was not registered")
		}
	}
}

func TestFindOrCreateMetricsDescriptionFindExisting(t *testing.T) {
	exp := getTestExporter()
	exp.metricDescriptions = map[string]*prometheus.Desc{}

	metricName := "foo"
	labels := []string{"1", "2"}

	ret := exp.createMetricDescription(metricName, labels)
	ret2 := exp.createMetricDescription(metricName, labels)

	if ret == nil || ret2 == nil || ret != ret2 {
		t.Errorf("Unexpected return values: (%v, %v)", ret, ret2)
	}

	if len(exp.metricDescriptions) != 1 {
		t.Errorf("Unexpected metricDescriptions entry count.")
	}
}

func TestFindOrCreateMetricsDescriptionCreateNew(t *testing.T) {
	exp := getTestExporter()
	exp.metricDescriptions = map[string]*prometheus.Desc{}

	metricName := "foo"
	labels := []string{"1", "2"}

	ret := exp.createMetricDescription(metricName, labels)

	if ret == nil {
		t.Errorf("Unexpected return value: %s", ret)
	}
}
