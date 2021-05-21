package exporter

import (
	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// All fields of the streamInfo struct must be exported
// because of redis.ScanStruct (reflect) limitations
type streamInfo struct {
	Length           int64 `redis:"length"`
	RadixTreeKeys    int64 `redis:"radix-tree-keys"`
	RadixTreeNodes   int64 `redis:"radix-tree-nodes"`
	Groups           int64 `redis:"groups"`
	StreamGroupsInfo []streamGroupsInfo
}

type streamGroupsInfo struct {
	Name                     string `redis:"name"`
	Consumers                int64  `redis:"consumers"`
	Pending                  int64  `redis:"pending"`
	StreamGroupConsumersInfo []streamGroupConsumersInfo
}

type streamGroupConsumersInfo struct {
	Name    string `redis:"name"`
	Pending int64  `redis:"pending"`
	Idle    int64  `redis:"idle"`
}

func getStreamInfo(c redis.Conn, key string) (*streamInfo, error) {
	v, err := redis.Values(doRedisCmd(c, "XINFO", "STREAM", key))
	if err != nil {
		return nil, err
	}
	// Scan slice to struct
	var stream streamInfo
	if err := redis.ScanStruct(v, &stream); err != nil {
		return nil, err
	}

	stream.StreamGroupsInfo, err = scanStreamGroups(c, key)
	if err != nil {
		return nil, err
	}

	log.Debugf("stream: %#v", &stream)
	return &stream, nil
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
		e.registerConstMetricGauge(ch, "stream_length", float64(info.Length), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_radix_tree_keys", float64(info.RadixTreeKeys), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_radix_tree_nodes", float64(info.RadixTreeNodes), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_groups", float64(info.Groups), dbLabel, k.key)

		for _, g := range info.StreamGroupsInfo {
			e.registerConstMetricGauge(ch, "stream_group_consumers", float64(g.Consumers), dbLabel, k.key, g.Name)
			e.registerConstMetricGauge(ch, "stream_group_messages_pending", float64(g.Pending), dbLabel, k.key, g.Name)
			for _, c := range g.StreamGroupConsumersInfo {
				e.registerConstMetricGauge(ch, "stream_group_consumer_messages_pending", float64(c.Pending), dbLabel, k.key, g.Name, c.Name)
				e.registerConstMetricGauge(ch, "stream_group_consumer_idle_seconds", float64(c.Idle)/1e3, dbLabel, k.key, g.Name, c.Name)
			}
		}
	}
}
