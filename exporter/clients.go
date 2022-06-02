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

/*
	Valid Examples
	id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex
	id=14 addr=127.0.0.1:64958 fd=9 name= age=5 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client
*/
func parseClientListString(clientInfo string) ([]string, bool) {
	if matched, _ := regexp.MatchString(`^id=\d+ addr=\d+`, clientInfo); !matched {
		return nil, false
	}
	connectedClient := map[string]string{}
	for _, kvPart := range strings.Split(clientInfo, " ") {
		vPart := strings.Split(kvPart, "=")
		if len(vPart) != 2 {
			log.Debugf("Invalid format for client list string, got: %s", kvPart)
			return nil, false
		}
		connectedClient[vPart[0]] = vPart[1]
	}

	createdAtTs, err := durationFieldToTimestamp(connectedClient["age"])
	if err != nil {
		log.Debugf("cloud not parse age field(%s): %s", connectedClient["age"], err.Error())
		return nil, false
	}

	idleSinceTs, err := durationFieldToTimestamp(connectedClient["idle"])
	if err != nil {
		log.Debugf("cloud not parse idle field(%s): %s", connectedClient["idle"], err.Error())
		return nil, false
	}

	hostPortString := strings.Split(connectedClient["addr"], ":")
	if len(hostPortString) != 2 {
		return nil, false
	}

	return []string{
		connectedClient["name"],
		createdAtTs,
		idleSinceTs,
		connectedClient["flags"],
		connectedClient["db"],
		connectedClient["omem"],
		connectedClient["cmd"],

		hostPortString[0], // host
		hostPortString[1], // port
	}, true

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

			// port is the last item, we'll trim it if it's not needed
			if !e.options.ExportClientsInclPort {
				lbls = lbls[:len(lbls)-1]
			}
			e.registerConstMetricGauge(
				ch, "connected_clients_details", 1.0,
				lbls...,
			)
		}
	}
}
