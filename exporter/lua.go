package exporter

import (
	"log/slog"
	"strconv"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func (e *Exporter) extractLuaScriptMetrics(ch chan<- prometheus.Metric, c redis.Conn, filename string, script []byte) error {
	slog.Debug("Evaluating e.options.LuaScript", "file", filename)
	kv, err := redis.StringMap(doRedisCmd(c, "EVAL", script, 0, 0))
	if err != nil {
		slog.Error("LuaScript error", "error", err)
		e.registerConstMetricGauge(ch, "script_result", 0, filename)
		return err
	}

	if len(kv) == 0 {
		slog.Debug("Lua script returned no results")
		e.registerConstMetricGauge(ch, "script_result", 2, filename)
		return nil
	}

	for key, stringVal := range kv {
		val, err := strconv.ParseFloat(stringVal, 64)
		if err != nil {
			slog.Error("Error parsing lua script results", "error", err)
			e.registerConstMetricGauge(ch, "script_result", 0, filename)
			return err
		}
		e.registerConstMetricGauge(ch, "script_values", val, key, filename)
	}
	e.registerConstMetricGauge(ch, "script_result", 1, filename)
	return nil
}
