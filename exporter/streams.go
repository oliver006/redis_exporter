package exporter

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

// All fields of the streamInfo struct must be exported
// because of redis.ScanStruct (reflect) limitations
type streamInfo struct {
	Length            int64  `redis:"length"`
	RadixTreeKeys     int64  `redis:"radix-tree-keys"`
	RadixTreeNodes    int64  `redis:"radix-tree-nodes"`
	LastGeneratedId   string `redis:"last-generated-id"`
	Groups            int64  `redis:"groups"`
	MaxDeletedEntryId string `redis:"max-deleted-entry-id"`
	FirstEntryId      string
	LastEntryId       string
	StreamGroupsInfo  []streamGroupsInfo
}

type streamGroupsInfo struct {
	Name                     string `redis:"name"`
	Consumers                int64  `redis:"consumers"`
	Pending                  int64  `redis:"pending"`
	LastDeliveredId          string `redis:"last-delivered-id"`
	EntriesRead              int64  `redis:"entries-read"`
	Lag                      int64  `redis:"lag"`
	StreamGroupConsumersInfo []streamGroupConsumersInfo
}

type streamGroupConsumersInfo struct {
	Name    string `redis:"name"`
	Pending int64  `redis:"pending"`
	Idle    int64  `redis:"idle"`
}

func getStreamInfo(c redis.Conn, key string) (*streamInfo, error) {
	values, err := redis.Values(doRedisCmd(c, "XINFO", "STREAM", key))
	if err != nil {
		return nil, err
	}

	// Scan slice to struct
	var stream streamInfo
	if err := redis.ScanStruct(values, &stream); err != nil {
		return nil, err
	}

	// Extract first and last id from slice
	for idx, v := range values {
		vbytes, ok := v.([]byte)
		if !ok {
			continue
		}
		if string(vbytes) == "first-entry" {
			stream.FirstEntryId = getStreamEntryId(values, idx+1)
		}
		if string(vbytes) == "last-entry" {
			stream.LastEntryId = getStreamEntryId(values, idx+1)
		}
	}

	stream.StreamGroupsInfo, err = scanStreamGroups(c, key)
	if err != nil {
		return nil, err
	}

	slog.Debug("Retrieved stream info", "stream", &stream)
	return &stream, nil
}

func getStreamEntryId(redisValue []interface{}, index int) string {
	if values, ok := redisValue[index].([]interface{}); !ok || len(values) < 2 {
		slog.Debug("Failed to parse StreamEntryId")
		return ""
	}

	if len(redisValue) < index || redisValue[index] == nil {
		slog.Debug("Failed to parse StreamEntryId")
		return ""
	}

	entryId, ok := redisValue[index].([]interface{})[0].([]byte)
	if !ok {
		slog.Debug("Failed to parse StreamEntryId")
		return ""
	}
	return string(entryId)
}

func scanStreamGroups(c redis.Conn, stream string) ([]streamGroupsInfo, error) {
	groups, err := redis.Values(doRedisCmd(c, "XINFO", "GROUPS", stream))
	if err != nil {
		return nil, err
	}

	var result []streamGroupsInfo
	for _, g := range groups {
		v, err := redis.Values(g, nil)
		if err != nil {
			slog.Error("Couldn't convert group values for stream", "stream", stream, "error", err)
			continue
		}
		slog.Debug("streamGroupsInfo value", "value", v)

		var group streamGroupsInfo
		if err := redis.ScanStruct(v, &group); err != nil {
			slog.Error("Couldn't scan group in stream", "stream", stream, "error", err)
			continue
		}

		group.StreamGroupConsumersInfo, err = scanStreamGroupConsumers(c, stream, group.Name)
		if err != nil {
			return nil, err
		}

		result = append(result, group)
	}

	slog.Debug("groups", "result", result)
	return result, nil
}

func scanStreamGroupConsumers(c redis.Conn, stream string, group string) ([]streamGroupConsumersInfo, error) {
	consumers, err := redis.Values(doRedisCmd(c, "XINFO", "CONSUMERS", stream, group))
	if err != nil {
		return nil, err
	}

	var result []streamGroupConsumersInfo
	for _, c := range consumers {

		v, err := redis.Values(c, nil)
		if err != nil {
			slog.Error("Couldn't convert consumer values for group in stream", "group", group, "stream", stream, "error", err)
			continue
		}
		slog.Debug("streamGroupConsumersInfo value", "value", v)

		var consumer streamGroupConsumersInfo
		if err := redis.ScanStruct(v, &consumer); err != nil {
			slog.Error("Couldn't scan consumers for group in stream", "group", group, "stream", stream, "error", err)
			continue
		}

		result = append(result, consumer)
	}

	slog.Debug("consumers", "result", result)
	return result, nil
}

func parseStreamItemId(id string) float64 {
	if strings.TrimSpace(id) == "" {
		return 0
	}
	frags := strings.Split(id, "-")
	if len(frags) == 0 {
		slog.Error("Couldn't parse StreamItemId", "id", id)
		return 0
	}
	parsedId, err := strconv.ParseFloat(strings.Split(id, "-")[0], 64)
	if err != nil {
		slog.Error("Couldn't parse given StreamItemId", "id", id, "error", err)
	}
	return parsedId
}

func (e *Exporter) extractStreamMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	streams, err := parseKeyArg(e.options.CheckStreams)
	if err != nil {
		slog.Error("Couldn't parse given stream keys", "error", err)
		return
	}

	singleStreams, err := parseKeyArg(e.options.CheckSingleStreams)
	if err != nil {
		slog.Error("Couldn't parse check-single-streams", "error", err)
		return
	}
	allStreams := append([]dbKeyPair{}, singleStreams...)

	scannedStreams, err := getKeysFromPatterns(c, streams, e.options.CheckKeysBatchSize)
	if err != nil {
		slog.Error("Error expanding key patterns", "error", err)
	} else {
		allStreams = append(allStreams, scannedStreams...)
	}

	slog.Debug("allStreams", "allStreams", allStreams)
	for _, k := range allStreams {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			slog.Debug("Couldn't select database when getting stream info", "db", k.db)
			continue
		}
		info, err := getStreamInfo(c, k.key)
		if err != nil {
			slog.Error("couldn't get info for stream", "stream", k.key, "error", err)
			continue
		}
		dbLabel := "db" + k.db

		e.registerConstMetricGauge(ch, "stream_length", float64(info.Length), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_radix_tree_keys", float64(info.RadixTreeKeys), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_radix_tree_nodes", float64(info.RadixTreeNodes), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_last_generated_id", parseStreamItemId(info.LastGeneratedId), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_groups", float64(info.Groups), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_max_deleted_entry_id", parseStreamItemId(info.MaxDeletedEntryId), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_first_entry_id", parseStreamItemId(info.FirstEntryId), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_last_entry_id", parseStreamItemId(info.LastEntryId), dbLabel, k.key)

		for _, g := range info.StreamGroupsInfo {
			e.registerConstMetricGauge(ch, "stream_group_consumers", float64(g.Consumers), dbLabel, k.key, g.Name)
			e.registerConstMetricGauge(ch, "stream_group_messages_pending", float64(g.Pending), dbLabel, k.key, g.Name)
			e.registerConstMetricGauge(ch, "stream_group_last_delivered_id", parseStreamItemId(g.LastDeliveredId), dbLabel, k.key, g.Name)
			e.registerConstMetricGauge(ch, "stream_group_entries_read", float64(g.EntriesRead), dbLabel, k.key, g.Name)
			e.registerConstMetricGauge(ch, "stream_group_lag", float64(g.Lag), dbLabel, k.key, g.Name)
			if !e.options.StreamsExcludeConsumerMetrics {
				for _, c := range g.StreamGroupConsumersInfo {
					e.registerConstMetricGauge(ch, "stream_group_consumer_messages_pending", float64(c.Pending), dbLabel, k.key, g.Name, c.Name)
					e.registerConstMetricGauge(ch, "stream_group_consumer_idle_seconds", float64(c.Idle)/1e3, dbLabel, k.key, g.Name, c.Name)
				}
			}
		}
	}
}
