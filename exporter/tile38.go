package exporter

import (
	"log/slog"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func (e *Exporter) extractTile38Metrics(ch chan<- prometheus.Metric, c redis.Conn) {
	info, err := redis.Strings(doRedisCmd(c, "SERVER", "EXT"))
	if err != nil {
		slog.Error("Failed to extract Tile38 metrics", "error", err)
		return
	}

	for i := 0; i < len(info); i += 2 {
		fieldKey := info[i]
		if !strings.HasPrefix(fieldKey, "tile38_") {
			fieldKey = "tile38_" + fieldKey
		}

		fieldValue := info[i+1]
		slog.Debug("tile38", "key", fieldKey, "val", fieldValue)

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}
}
