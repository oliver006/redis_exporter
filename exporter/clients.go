package exporter

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type ClientInfo struct {
	Id,
	Name,
	User,
	Flags,
	Db,
	Host,
	Port,
	Resp string
	CreatedAt,
	IdleSince,
	Sub,
	Psub,
	Ssub,
	Watch,
	Qbuf,
	QbufFree,
	Obl,
	Oll,
	OMem,
	TotMem int64
}

/*
Valid Examples
id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex user=default resp=2
id=14 addr=127.0.0.1:64958 fd=9 name= age=5 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client user=default resp=3
id=40253233 addr=fd40:1481:21:dbe0:7021:300:a03:1a06:44426 fd=19 name= age=782 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 argv-mem=10 obl=0 oll=0 omem=0 tot-mem=61466 ow=0 owmem=0 events=r cmd=client user=default lib-name=redis-py lib-ver=5.0.1 numops=9
*/
func parseClientListString(clientInfo string) (*ClientInfo, bool) {
	if matched, _ := regexp.MatchString(`^id=\d+ addr=\S+`, clientInfo); !matched {
		return nil, false
	}
	connectedClient := ClientInfo{}
	connectedClient.Ssub = -1  // mark it as missing - introduced in Redis 7.0.3
	connectedClient.Watch = -1 // mark it as missing - introduced in Redis 7.4
	for _, kvPart := range strings.Split(clientInfo, " ") {
		vPart := strings.Split(kvPart, "=")
		if len(vPart) != 2 {
			log.Debugf("Invalid format for client list string, got: %s", kvPart)
			return nil, false
		}

		switch vPart[0] {
		case "id":
			connectedClient.Id = vPart[1]
		case "name":
			connectedClient.Name = vPart[1]
		case "user":
			connectedClient.User = vPart[1]
		case "age":
			createdAt, err := durationFieldToTimestamp(vPart[1])
			if err != nil {
				log.Debugf("could not parse 'age' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.CreatedAt = createdAt
		case "idle":
			idleSinceTs, err := durationFieldToTimestamp(vPart[1])
			if err != nil {
				log.Debugf("could not parse 'idle' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.IdleSince = idleSinceTs
		case "flags":
			connectedClient.Flags = vPart[1]
		case "db":
			connectedClient.Db = vPart[1]
		case "sub":
			Sub, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'sub' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Sub = Sub
		case "psub":
			Psub, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'psub' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Psub = Psub
		case "ssub":
			Ssub, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'ssub' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Ssub = Ssub
		case "watch":
			Watch, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'watch' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Watch = Watch
		case "qbuf":
			Qbuf, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'qbuf' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Qbuf = Qbuf
		case "qbuf-free":
			QbufFree, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'qbuf-free' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.QbufFree = QbufFree
		case "obl":
			Obl, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'obl' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Obl = Obl
		case "oll":
			Oll, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'oll' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.Oll = Oll
		case "omem":
			OMem, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'omem' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.OMem = OMem
		case "tot-mem":
			TotMem, err := strconv.ParseInt(vPart[1], 10, 64)
			if err != nil {
				log.Debugf("could not parse 'tot-mem' field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.TotMem = TotMem
		case "addr":
			hostPortString := strings.Split(vPart[1], ":")
			if len(hostPortString) < 2 {
				log.Debug("Invalid value for 'addr' found in client info")
				return nil, false
			}
			connectedClient.Host = strings.Join(hostPortString[:len(hostPortString)-1], ":")
			connectedClient.Port = hostPortString[len(hostPortString)-1]
		case "resp":
			connectedClient.Resp = vPart[1]
		}
	}

	return &connectedClient, true
}

func durationFieldToTimestamp(field string) (int64, error) {
	parsed, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Now().Unix() - parsed, nil
}

func (e *Exporter) extractConnectedClientMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	reply, err := redis.String(doRedisCmd(c, "CLIENT", "LIST"))
	if err != nil {
		log.Errorf("CLIENT LIST err: %s", err)
		return
	}

	for _, c := range strings.Split(reply, "\n") {
		if info, ok := parseClientListString(c); ok {
			clientInfoLabels := []string{"id", "name", "flags", "db", "host"}
			clientInfoLabelsValues := []string{info.Id, info.Name, info.Flags, info.Db, info.Host}

			if e.options.ExportClientsInclPort {
				clientInfoLabels = append(clientInfoLabels, "port")
				clientInfoLabelsValues = append(clientInfoLabelsValues, info.Port)
			}

			if user := info.User; user != "" {
				clientInfoLabels = append(clientInfoLabels, "user")
				clientInfoLabelsValues = append(clientInfoLabelsValues, user)
			}

			// introduced in Redis 7.0
			if resp := info.Resp; resp != "" {
				clientInfoLabels = append(clientInfoLabels, "resp")
				clientInfoLabelsValues = append(clientInfoLabelsValues, resp)
			}

			e.metricDescriptions["connected_client_info"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_info",
				"Details about a connected client",
				clientInfoLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_info", 1.0,
				clientInfoLabelsValues...,
			)

			// keep the old name for backwards compatability
			e.metricDescriptions["connected_clients_details"] = newMetricDescr(
				e.options.Namespace,
				"connected_clients_details",
				"Details about a connected client",
				clientInfoLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_clients_details", 1.0,
				clientInfoLabelsValues...,
			)

			clientBaseLabels := []string{"id", "name"}
			clientBaseLabelsValues := []string{info.Id, info.Name}

			e.metricDescriptions["connected_client_output_buffer_memory_usage_bytes"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_output_buffer_memory_usage_bytes",
				"A connected client's output buffer memory usage in bytes",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_output_buffer_memory_usage_bytes", float64(info.OMem),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_total_memory_consumed_bytes"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_total_memory_consumed_bytes",
				"Total memory consumed by a client in its various buffers",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_total_memory_consumed_bytes", float64(info.TotMem),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_created_at_timestamp"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_created_at_timestamp",
				"A connected client's creation timestamp",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_created_at_timestamp", float64(info.CreatedAt),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_idle_since_timestamp"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_idle_since_timestamp",
				"A connected client's idle since timestamp",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_idle_since_timestamp", float64(info.IdleSince),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_channel_subscriptions_count"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_channel_subscriptions_count",
				"A connected client's number of channel subscriptions",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_channel_subscriptions_count", float64(info.Sub),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_pattern_matching_subscriptions_count"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_pattern_matching_subscriptions_count",
				"A connected client's number of pattern matching subscriptions",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_pattern_matching_subscriptions_count", float64(info.Psub),
				clientBaseLabelsValues...,
			)

			if info.Ssub != -1 {
				e.metricDescriptions["connected_client_shard_channel_subscriptions_count"] = newMetricDescr(
					e.options.Namespace,
					"connected_client_shard_channel_subscriptions_count",
					"a connected client's number of shard channel subscriptions",
					clientBaseLabels,
				)
				e.registerConstMetricGauge(
					ch, "connected_client_shard_channel_subscriptions_count", float64(info.Ssub),
					clientBaseLabelsValues...,
				)
			}
			if info.Watch != -1 {
				e.metricDescriptions["connected_client_shard_channel_watched_keys"] = newMetricDescr(
					e.options.Namespace,
					"connected_client_shard_channel_watched_keys",
					"a connected client's number of keys it's currently watching",
					clientBaseLabels,
				)
				e.registerConstMetricGauge(
					ch, "connected_client_shard_channel_watched_keys", float64(info.Watch),
					clientBaseLabelsValues...,
				)
			}

			e.metricDescriptions["connected_client_query_buffer_length_bytes"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_query_buffer_length_bytes",
				"A connected client's query buffer length in bytes (0 means no query pending)",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_query_buffer_length_bytes", float64(info.Qbuf),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_query_buffer_free_space_bytes"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_query_buffer_free_space_bytes",
				"A connected client's free space of the query buffer in bytes (0 means the buffer is full)",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_query_buffer_free_space_bytes", float64(info.QbufFree),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_output_buffer_length_bytes"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_output_buffer_length_bytes",
				"A connected client's output buffer length in bytes",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_output_buffer_length_bytes", float64(info.Obl),
				clientBaseLabelsValues...,
			)

			e.metricDescriptions["connected_client_output_list_length"] = newMetricDescr(
				e.options.Namespace,
				"connected_client_output_list_length",
				"A connected client's output list length (replies are queued in this list when the buffer is full)",
				clientBaseLabels,
			)
			e.registerConstMetricGauge(
				ch, "connected_client_output_list_length", float64(info.Oll),
				clientBaseLabelsValues...,
			)
		}
	}
}
