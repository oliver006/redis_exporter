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
		log.Debugf("Lua script returned no results")
		return nil
	}

	for key, stringVal := range kv {
		val, err := strconv.ParseFloat(stringVal, 64)
		if err != nil {
			log.Errorf("Error parsing lua script results, err: %s", err)
			return err
		}
		e.registerConstMetricGauge(ch, "script_values", val, key)
	}
	return nil
}
