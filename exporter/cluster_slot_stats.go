package exporter

import (
	"strconv"
	"strings"
	"sync"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var logSlotStatsErrOnce sync.Once

const clusterSlotCount = 16384

// extractClusterSlotStatsMetrics exports per-slot usage statistics for the slots
// owned by this node, see https://valkey.io/commands/cluster-slot-stats/
func (e *Exporter) extractClusterSlotStatsMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	reply, err := redis.Values(doRedisCmd(c, "CLUSTER", "SLOT-STATS", "SLOTSRANGE", "0", strconv.Itoa(clusterSlotCount-1)))
	if err != nil {
		// unknown subcommand is expected on Redis and Valkey < 8.0
		if !strings.HasPrefix(err.Error(), "ERR unknown") && !strings.Contains(err.Error(), "unknown subcommand") {
			logSlotStatsErrOnce.Do(func() {
				log.Errorf("WARNING, LOGGED ONCE ONLY: cmd CLUSTER SLOT-STATS, err: %s", err)
			})
		}
		log.Debugf("cmd CLUSTER SLOT-STATS, err: %s", err)
		return
	}

	e.registerClusterSlotStats(ch, reply)
}

func (e *Exporter) registerClusterSlotStats(ch chan<- prometheus.Metric, reply []interface{}) {
	for _, item := range reply {
		entry, err := redis.Values(item, nil)
		if err != nil || len(entry) < 2 {
			continue
		}
		slot, err := redis.Int(entry[0], nil)
		if err != nil {
			continue
		}
		stats, err := redis.Values(entry[1], nil)
		if err != nil {
			continue
		}

		slotLabel := strconv.Itoa(slot)
		// stats is a flat list: name, value, name, value, ...
		for i := 0; i+1 < len(stats); i += 2 {
			name, err := redis.String(stats[i], nil)
			if err != nil {
				continue
			}
			intVal, err := redis.Int64(stats[i+1], nil)
			if err != nil {
				continue
			}
			val := float64(intVal)

			switch name {
			case "key-count":
				e.registerConstMetricGauge(ch, "cluster_slot_key_count", val, slotLabel)
			case "cpu-usec":
				e.registerConstMetric(ch, "cluster_slot_cpu_usec_total", val, prometheus.CounterValue, slotLabel)
			case "network-bytes-in":
				e.registerConstMetric(ch, "cluster_slot_network_bytes_in_total", val, prometheus.CounterValue, slotLabel)
			case "network-bytes-out":
				e.registerConstMetric(ch, "cluster_slot_network_bytes_out_total", val, prometheus.CounterValue, slotLabel)
			}
		}
	}
}
