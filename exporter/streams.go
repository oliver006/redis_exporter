package exporter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
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
	EntriesAdded      int64  `redis:"entries-added"`
	IDMPDuration      int64  `redis:"idmp-duration"`
	IDMPMaxSize       int64  `redis:"idmp-maxsize"`
	PIDsTracked       int64  `redis:"pids-tracked"`
	IIDsTracked       int64  `redis:"iids-tracked"`
	IIDsAdded         int64  `redis:"iids-added"`
	IIDsDuplicates    int64  `redis:"iids-duplicates"`
	IDMPAvailable     bool
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
	NackedCount              int64
	NackedCountAvailable     bool
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

	stream, err := parseStreamInfo(values)
	if err != nil {
		return nil, err
	}

	stream.StreamGroupsInfo, err = scanStreamGroups(c, key)
	if err != nil {
		return nil, err
	}
	if len(stream.StreamGroupsInfo) > 0 {
		nackedCounts, err := scanStreamGroupNackedCounts(c, key)
		if err != nil {
			log.Debugf("Couldn't get XNACK state for stream '%s': %s", key, err)
		} else {
			for idx := range stream.StreamGroupsInfo {
				group := &stream.StreamGroupsInfo[idx]
				if count, ok := nackedCounts[group.Name]; ok {
					group.NackedCount = count
					group.NackedCountAvailable = true
				}
			}
		}
	}

	log.Debugf("getStreamInfo() stream: %#v", stream)
	return stream, nil
}

func parseStreamInfo(values []any) (*streamInfo, error) {
	var stream streamInfo
	if err := redis.ScanStruct(values, &stream); err != nil {
		return nil, err
	}

	for idx := 0; idx+1 < len(values); idx += 2 {
		key, err := redis.String(values[idx], nil)
		if err != nil {
			continue
		}
		switch key {
		case "first-entry":
			stream.FirstEntryId = getStreamEntryId(values, idx+1)
		case "last-entry":
			stream.LastEntryId = getStreamEntryId(values, idx+1)
		case "idmp-duration":
			stream.IDMPAvailable = true
		}
	}

	return &stream, nil
}

func getStreamEntryId(redisValue []any, index int) string {
	if index >= len(redisValue) || redisValue[index] == nil {
		log.Debugf("Failed to parse StreamEntryId")
		return ""
	}

	values, ok := redisValue[index].([]any)
	if !ok || len(values) < 1 {
		log.Debugf("Failed to parse StreamEntryId")
		return ""
	}

	entryId, ok := values[0].([]byte)
	if !ok {
		log.Debugf("Failed to parse StreamEntryId")
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
			log.Errorf("Couldn't convert group values for stream '%s': %s", stream, err)
			continue
		}
		log.Debugf("streamGroupsInfo value: %#v", v)

		var group streamGroupsInfo
		if err := redis.ScanStruct(v, &group); err != nil {
			log.Errorf("Couldn't scan group in stream '%s': %s", stream, err)
			continue
		}

		group.StreamGroupConsumersInfo, err = scanStreamGroupConsumers(c, stream, group.Name)
		if err != nil {
			return nil, err
		}

		result = append(result, group)
	}

	log.Debugf("groups: %v", result)
	return result, nil
}

func scanStreamGroupNackedCounts(c redis.Conn, stream string) (map[string]int64, error) {
	values, err := redis.Values(doRedisCmd(c, "XINFO", "STREAM", stream, "FULL", "COUNT", 1))
	if err != nil {
		return nil, err
	}
	return parseStreamGroupNackedCounts(values)
}

func parseStreamGroupNackedCounts(values []any) (map[string]int64, error) {
	result := map[string]int64{}
	var groups []any
	for idx := 0; idx+1 < len(values); idx += 2 {
		key, err := redis.String(values[idx], nil)
		if err != nil || key != "groups" {
			continue
		}
		groups, err = redis.Values(values[idx+1], nil)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse stream groups: %w", err)
		}
		break
	}

	for _, rawGroup := range groups {
		fields, err := redis.Values(rawGroup, nil)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse stream group: %w", err)
		}
		var name string
		var nackedCount int64
		var nackedCountAvailable bool
		for idx := 0; idx+1 < len(fields); idx += 2 {
			key, err := redis.String(fields[idx], nil)
			if err != nil {
				continue
			}
			switch key {
			case "name":
				name, err = redis.String(fields[idx+1], nil)
			case "nacked-count":
				nackedCount, err = redis.Int64(fields[idx+1], nil)
				nackedCountAvailable = err == nil
			}
			if err != nil {
				return nil, fmt.Errorf("couldn't parse stream group field %q: %w", key, err)
			}
		}
		if name != "" && nackedCountAvailable {
			result[name] = nackedCount
		}
	}

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
			log.Errorf("Couldn't convert consumer values for group '%s' in stream '%s': %s", group, stream, err)
			continue
		}
		log.Debugf("streamGroupConsumersInfo value: %#v", v)

		var consumer streamGroupConsumersInfo
		if err := redis.ScanStruct(v, &consumer); err != nil {
			log.Errorf("Couldn't scan consumers for  group '%s' in stream '%s': %s", group, stream, err)
			continue
		}

		result = append(result, consumer)
	}

	log.Debugf("consumers: %v", result)
	return result, nil
}

func parseStreamItemId(id string) float64 {
	if strings.TrimSpace(id) == "" {
		return 0
	}
	frags := strings.Split(id, "-")
	if len(frags) == 0 {
		log.Errorf("Couldn't parse StreamItemId: %s", id)
		return 0
	}
	parsedId, err := strconv.ParseFloat(strings.Split(id, "-")[0], 64)
	if err != nil {
		log.Errorf("Couldn't parse given StreamItemId: [%s]   err: %s", id, err)
	}
	return parsedId
}

func (e *Exporter) registerStreamMetrics(ch chan<- prometheus.Metric, dbLabel string, key string, info *streamInfo) {
	e.registerConstMetricGauge(ch, "stream_length", float64(info.Length), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_radix_tree_keys", float64(info.RadixTreeKeys), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_radix_tree_nodes", float64(info.RadixTreeNodes), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_last_generated_id", parseStreamItemId(info.LastGeneratedId), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_groups", float64(info.Groups), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_max_deleted_entry_id", parseStreamItemId(info.MaxDeletedEntryId), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_first_entry_id", parseStreamItemId(info.FirstEntryId), dbLabel, key)
	e.registerConstMetricGauge(ch, "stream_last_entry_id", parseStreamItemId(info.LastEntryId), dbLabel, key)
	e.registerConstMetric(ch, "stream_entries_added_total", float64(info.EntriesAdded), prometheus.CounterValue, dbLabel, key)

	if info.IDMPAvailable {
		e.registerConstMetricGauge(ch, "stream_idmp_duration_seconds", float64(info.IDMPDuration), dbLabel, key)
		e.registerConstMetricGauge(ch, "stream_idmp_max_size", float64(info.IDMPMaxSize), dbLabel, key)
		e.registerConstMetricGauge(ch, "stream_idmp_producer_ids_tracked", float64(info.PIDsTracked), dbLabel, key)
		e.registerConstMetricGauge(ch, "stream_idmp_idempotent_ids_tracked", float64(info.IIDsTracked), dbLabel, key)
		e.registerConstMetric(ch, "stream_idmp_entries_added_total", float64(info.IIDsAdded), prometheus.CounterValue, dbLabel, key)
		e.registerConstMetric(ch, "stream_idmp_duplicates_total", float64(info.IIDsDuplicates), prometheus.CounterValue, dbLabel, key)
	}

	for _, group := range info.StreamGroupsInfo {
		e.registerConstMetricGauge(ch, "stream_group_consumers", float64(group.Consumers), dbLabel, key, group.Name)
		e.registerConstMetricGauge(ch, "stream_group_messages_pending", float64(group.Pending), dbLabel, key, group.Name)
		e.registerConstMetricGauge(ch, "stream_group_last_delivered_id", parseStreamItemId(group.LastDeliveredId), dbLabel, key, group.Name)
		e.registerConstMetricGauge(ch, "stream_group_entries_read", float64(group.EntriesRead), dbLabel, key, group.Name)
		e.registerConstMetricGauge(ch, "stream_group_lag", float64(group.Lag), dbLabel, key, group.Name)
		if group.NackedCountAvailable {
			e.registerConstMetricGauge(ch, "stream_group_messages_nacked", float64(group.NackedCount), dbLabel, key, group.Name)
		}
		if !e.options.StreamsExcludeConsumerMetrics {
			for _, consumer := range group.StreamGroupConsumersInfo {
				e.registerConstMetricGauge(ch, "stream_group_consumer_messages_pending", float64(consumer.Pending), dbLabel, key, group.Name, consumer.Name)
				e.registerConstMetricGauge(ch, "stream_group_consumer_idle_seconds", float64(consumer.Idle)/1e3, dbLabel, key, group.Name, consumer.Name)
			}
		}
	}
}

func (e *Exporter) extractStreamMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	streams, err := parseKeyArg(e.options.CheckStreams)
	if err != nil {
		log.Errorf("Couldn't parse given stream keys: %s", err)
		return
	}

	singleStreams, err := parseKeyArg(e.options.CheckSingleStreams)
	if err != nil {
		log.Errorf("Couldn't parse check-single-streams: %s", err)
		return
	}
	allStreams := append([]dbKeyPair{}, singleStreams...)

	scannedStreams, err := getKeysFromPatterns(c, streams, e.options.CheckKeysBatchSize)
	if err != nil {
		log.Errorf("Error expanding key patterns: %s", err)
	} else {
		allStreams = append(allStreams, scannedStreams...)
	}

	log.Debugf("allStreams: %#v", allStreams)
	for _, k := range allStreams {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			log.Debugf("Couldn't select database '%s' when getting stream info", k.db)
			continue
		}
		info, err := getStreamInfo(c, k.key)
		if err != nil {
			log.Errorf("couldn't get info for stream '%s', err: %s", k.key, err)
			continue
		}
		dbLabel := "db" + k.db
		e.registerStreamMetrics(ch, dbLabel, k.key, info)
	}
}
