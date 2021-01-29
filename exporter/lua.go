package exporter

import (
	"strconv"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractLuaScriptMetrics(ch chan<- prometheus.Metric, c redis.Conn) error {
	log.Debug("Evaluating e.options.LuaScript")
	kv, err := redis.StringMap(doRedisCmd(c, "EVAL", e.options.LuaScript, 0, 0))
	if err != nil {
		log.Errorf("LuaScript error: %v", err)
		return err
	}

	if len(kv) == 0 {
		return nil
	}

	for key, stringVal := range kv {
		if val, err := strconv.ParseFloat(stringVal, 64); err == nil {
			e.registerConstMetricGauge(ch, "script_values", val, key)
		}
	}
	return nil
}
