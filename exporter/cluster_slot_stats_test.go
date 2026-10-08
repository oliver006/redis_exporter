package exporter

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestRegisterClusterSlotStats(t *testing.T) {
	e := &Exporter{
		options:            Options{Namespace: "test"},
		metricDescriptions: map[string]*prometheus.Desc{},
	}
	e.metricDescriptions = map[string]*prometheus.Desc{}
	for _, m := range []string{"cluster_slot_key_count", "cluster_slot_cpu_usec_total", "cluster_slot_network_bytes_in_total", "cluster_slot_network_bytes_out_total"} {
		e.metricDescriptions[m] = newMetricDescr("test", m, m, []string{"slot"})
	}

	reply := []interface{}{
		[]interface{}{int64(5), []interface{}{
			[]byte("key-count"), int64(3),
			[]byte("cpu-usec"), int64(100),
			[]byte("network-bytes-in"), int64(200),
			[]byte("network-bytes-out"), int64(300),
		}},
	}

	ch := make(chan prometheus.Metric, 10)
	e.registerClusterSlotStats(ch, reply)
	close(ch)

	want := map[string]float64{"key_count": 3, "cpu_usec_total": 100, "network_bytes_in_total": 200, "network_bytes_out_total": 300}
	got := 0
	for m := range ch {
		d := &dto.Metric{}
		_ = m.Write(d)
		v := d.GetGauge().GetValue() + d.GetCounter().GetValue()
		for k, w := range want {
			if strings.Contains(m.Desc().String(), "cluster_slot_"+k+"\"") {
				got++
				if v != w {
					t.Errorf("%s: got %v want %v", k, v, w)
				}
				if d.GetLabel()[0].GetValue() != "5" {
					t.Errorf("bad slot label")
				}
			}
		}
	}
	if got != 4 {
		t.Errorf("expected 4 metrics, got %d", got)
	}
}
