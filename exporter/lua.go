package exporter

import (
	"strconv"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) extractLuaScriptMetrics(ch chan<- prometheus.Metric, c redis.Conn, filename string, script []byte) error {
	log.Debugf("Evaluating e.options.LuaScript: %s", filename)
	kv, err := redis.StringMap(doRedisCmd(c, "EVAL", script, 0, 0))
	if err != nil {
		log.Errorf("LuaScript error: %v", err)
		e.registerConstMetricGauge(ch, "script_result", 0, filename)
		return err
	}

	if len(kv) == 0 {
		log.Debugf("Lua script returned no results")
		e.registerConstMetricGauge(ch, "script_result", 2, filename)
		return nil
	}

	for key, stringVal := range kv {
		val, err := strconv.ParseFloat(stringVal, 64)
		if err != nil {
			log.Errorf("Error parsing lua script results, err: %s", err)
			e.registerConstMetricGauge(ch, "script_result", 0, filename)
			return err
		}
		e.registerConstMetricGauge(ch, "script_values", val, key, filename)
	}
	e.registerConstMetricGauge(ch, "script_result", 1, filename)
	return nil
}
