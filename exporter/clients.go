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
	Name,
	User,
	CreatedAt,
	IdleSince,
	Flags,
	Db,
	OMem,
	Cmd,
	Host,
	Port,
	Resp string
}

/*
	Valid Examples
	id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex user=default resp=2
	id=14 addr=127.0.0.1:64958 fd=9 name= age=5 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client user=default resp=3
*/
func parseClientListString(clientInfo string) (*ClientInfo, bool) {
	if matched, _ := regexp.MatchString(`^id=\d+ addr=\d+`, clientInfo); !matched {
		return nil, false
	}
	connectedClient := ClientInfo{}
	for _, kvPart := range strings.Split(clientInfo, " ") {
		vPart := strings.Split(kvPart, "=")
		if len(vPart) != 2 {
			log.Debugf("Invalid format for client list string, got: %s", kvPart)
			return nil, false
		}

		switch vPart[0] {
		case "name":
			connectedClient.Name = vPart[1]
		case "user":
			connectedClient.User = vPart[1]
		case "age":
			createdAt, err := durationFieldToTimestamp(vPart[1])
			if err != nil {
				log.Debugf("cloud not parse age field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.CreatedAt = createdAt
		case "idle":
			idleSinceTs, err := durationFieldToTimestamp(vPart[1])
			if err != nil {
				log.Debugf("cloud not parse idle field(%s): %s", vPart[1], err.Error())
				return nil, false
			}
			connectedClient.IdleSince = idleSinceTs
		case "flags":
			connectedClient.Flags = vPart[1]
		case "db":
			connectedClient.Db = vPart[1]
		case "omem":
			connectedClient.OMem = vPart[1]
		case "cmd":
			connectedClient.Cmd = vPart[1]
		case "addr":
			hostPortString := strings.Split(vPart[1], ":")
			if len(hostPortString) != 2 {
				return nil, false
			}
			connectedClient.Host = hostPortString[0]
			connectedClient.Port = hostPortString[1]
		case "resp":
			connectedClient.Resp = vPart[1]
		}
	}

	return &connectedClient, true
}

func durationFieldToTimestamp(field string) (string, error) {
	parsed, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return "", err
	}

	return strconv.FormatInt(time.Now().Unix()-parsed, 10), nil
}

func (e *Exporter) extractConnectedClientMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	reply, err := redis.String(doRedisCmd(c, "CLIENT", "LIST"))
	if err != nil {
		log.Errorf("CLIENT LIST err: %s", err)
		return
	}

	for _, c := range strings.Split(reply, "\n") {
		if lbls, ok := parseClientListString(c); ok {
			connectedClientsLabels := []string{"name", "created_at", "idle_since", "flags", "db", "omem", "cmd", "host"}
			connectedClientsLabelsValues := []string{lbls.Name, lbls.CreatedAt, lbls.IdleSince, lbls.Flags, lbls.Db, lbls.OMem, lbls.Cmd, lbls.Host}

			if e.options.ExportClientsInclPort {
				connectedClientsLabels = append(connectedClientsLabels, "port")
				connectedClientsLabelsValues = append(connectedClientsLabelsValues, lbls.Port)
			}

			if user := lbls.User; user != "" {
				connectedClientsLabels = append(connectedClientsLabels, "user")
				connectedClientsLabelsValues = append(connectedClientsLabelsValues, user)
			}

			if resp := lbls.Resp; resp != "" {
				connectedClientsLabels = append(connectedClientsLabels, "resp")
				connectedClientsLabelsValues = append(connectedClientsLabelsValues, resp)
			}

			e.metricDescriptions["connected_clients_details"] = newMetricDescr(e.options.Namespace, "connected_clients_details", "Details about connected clients", connectedClientsLabels)

			e.registerConstMetricGauge(
				ch, "connected_clients_details", 1.0,
				connectedClientsLabelsValues...,
			)
		}
	}
}
