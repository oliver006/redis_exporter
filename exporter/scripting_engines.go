package exporter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type scriptingEngineInfo struct {
	name           string
	module         string
	abiVersion     string
	usedMemory     float64
	memoryOverhead float64
}

func parseScriptingEngineInfo(value string) (scriptingEngineInfo, error) {
	fields := make(map[string]string, 5)
	for field := range strings.SplitSeq(value, ",") {
		name, value, ok := strings.Cut(field, "=")
		if !ok {
			return scriptingEngineInfo{}, fmt.Errorf("invalid scripting engine field %q", field)
		}
		fields[name] = value
	}

	for _, name := range []string{"name", "module", "abi_version", "used_memory", "memory_overhead"} {
		if _, ok := fields[name]; !ok {
			return scriptingEngineInfo{}, fmt.Errorf("missing scripting engine field %q", name)
		}
	}

	usedMemory, err := strconv.ParseUint(fields["used_memory"], 10, 64)
	if err != nil {
		return scriptingEngineInfo{}, fmt.Errorf("invalid scripting engine used_memory: %w", err)
	}
	memoryOverhead, err := strconv.ParseUint(fields["memory_overhead"], 10, 64)
	if err != nil {
		return scriptingEngineInfo{}, fmt.Errorf("invalid scripting engine memory_overhead: %w", err)
	}

	return scriptingEngineInfo{
		name:           fields["name"],
		module:         fields["module"],
		abiVersion:     fields["abi_version"],
		usedMemory:     float64(usedMemory),
		memoryOverhead: float64(memoryOverhead),
	}, nil
}

func (e *Exporter) handleMetricsScriptingEngines(ch chan<- prometheus.Metric, fieldKey, fieldValue string) bool {
	if !strings.HasPrefix(fieldKey, "engine_") {
		return false
	}

	engine, err := parseScriptingEngineInfo(fieldValue)
	if err != nil {
		log.Debugf("couldn't parse %s, err: %s", fieldKey, err)
		return true
	}

	labels := []string{engine.name, engine.module, engine.abiVersion}
	e.registerConstMetricGauge(ch, "scripting_engine_info", 1, labels...)
	e.registerConstMetricGauge(ch, "scripting_engine_memory_used_bytes", engine.usedMemory, labels...)
	e.registerConstMetricGauge(ch, "scripting_engine_memory_overhead_bytes", engine.memoryOverhead, labels...)
	return true
}
