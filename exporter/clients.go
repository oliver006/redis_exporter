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

var reClientListId = regexp.MustCompile(`^id=\d+ addr=\S+`)

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
	if !reClientListId.MatchString(clientInfo) {
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
			connectedClient.Sub, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "psub":
			connectedClient.Psub, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "ssub":
			connectedClient.Ssub, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "watch":
			connectedClient.Watch, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "qbuf":
			connectedClient.Qbuf, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "qbuf-free":
			connectedClient.QbufFree, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "obl":
			connectedClient.Obl, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "oll":
			connectedClient.Oll, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "omem":
			connectedClient.OMem, _ = strconv.ParseInt(vPart[1], 10, 64)
		case "tot-mem":
			connectedClient.TotMem, _ = strconv.ParseInt(vPart[1], 10, 64)
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
	e.parseConnectedClientMetrics(reply, ch)
}

func (e *Exporter) parseConnectedClientMetrics(input string, ch chan<- prometheus.Metric) {

	for _, s := range strings.Split(input, "\n") {
		info, ok := parseClientListString(s)
		if !ok {
			log.Debugf("parseClientListString( %s ) - couldn';t parse input", s)
			continue
		}
		clientInfoLabels := []string{"id", "name", "flags", "db", "host"}
		clientInfoLabelValues := []string{info.Id, info.Name, info.Flags, info.Db, info.Host}

		if e.options.ExportClientsInclPort {
			clientInfoLabels = append(clientInfoLabels, "port")
			clientInfoLabelValues = append(clientInfoLabelValues, info.Port)
		}

		if user := info.User; user != "" {
			clientInfoLabels = append(clientInfoLabels, "user")
			clientInfoLabelValues = append(clientInfoLabelValues, user)
		}

		// introduced in Redis 7.0
		if resp := info.Resp; resp != "" {
			clientInfoLabels = append(clientInfoLabels, "resp")
			clientInfoLabelValues = append(clientInfoLabelValues, resp)
		}

		e.createMetricDescription("connected_client_info", clientInfoLabels)
		e.registerConstMetricGauge(
			ch, "connected_client_info", 1.0,
			clientInfoLabelValues...,
		)

		clientBaseLabels := []string{"id", "name"}
		clientBaseLabelsValues := []string{info.Id, info.Name}

		for _, metricName := range []string{
			"connected_client_output_buffer_memory_usage_bytes",
			"connected_client_total_memory_consumed_bytes",
			"connected_client_created_at_timestamp",
			"connected_client_idle_since_timestamp",
			"connected_client_channel_subscriptions_count",
			"connected_client_pattern_matching_subscriptions_count",
			"connected_client_query_buffer_length_bytes",
			"connected_client_query_buffer_free_space_bytes",
			"connected_client_output_buffer_length_bytes",
			"connected_client_output_list_length",
		} {
			e.createMetricDescription(metricName, clientBaseLabels)
		}

		e.registerConstMetricGauge(
			ch, "connected_client_output_buffer_memory_usage_bytes", float64(info.OMem),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_total_memory_consumed_bytes", float64(info.TotMem),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_created_at_timestamp", float64(info.CreatedAt),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_idle_since_timestamp", float64(info.IdleSince),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_channel_subscriptions_count", float64(info.Sub),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_pattern_matching_subscriptions_count", float64(info.Psub),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_query_buffer_length_bytes", float64(info.Qbuf),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_query_buffer_free_space_bytes", float64(info.QbufFree),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_output_buffer_length_bytes", float64(info.Obl),
			clientBaseLabelsValues...,
		)

		e.registerConstMetricGauge(
			ch, "connected_client_output_list_length", float64(info.Oll),
			clientBaseLabelsValues...,
		)

		if info.Ssub != -1 {
			e.createMetricDescription("connected_client_shard_channel_subscriptions_count", clientBaseLabels)
			e.registerConstMetricGauge(
				ch, "connected_client_shard_channel_subscriptions_count", float64(info.Ssub),
				clientBaseLabelsValues...,
			)
		}
		if info.Watch != -1 {
			e.createMetricDescription("connected_client_shard_channel_watched_keys", clientBaseLabels)
			e.registerConstMetricGauge(
				ch, "connected_client_shard_channel_watched_keys", float64(info.Watch),
				clientBaseLabelsValues...,
			)
		}
	}
}
