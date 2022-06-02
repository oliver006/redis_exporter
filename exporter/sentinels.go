package exporter

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) handleMetricsSentinel(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) bool {

	switch fieldKey {

	case "sentinel_masters", "sentinel_tilt", "sentinel_running_scripts", "sentinel_scripts_queue_length", "sentinel_simulate_failure_flags":
		val, _ := strconv.Atoi(fieldValue)
		e.registerConstMetricGauge(ch, fieldKey, float64(val))
		return true
	}

	if masterName, masterStatus, masterAddress, masterSlaves, masterSentinels, ok := parseSentinelMasterString(fieldKey, fieldValue); ok {
		masterStatusNum := 0.0
		if masterStatus == "ok" {
			masterStatusNum = 1
		}
		e.registerConstMetricGauge(ch, "sentinel_master_status", masterStatusNum, masterName, masterAddress, masterStatus)
		e.registerConstMetricGauge(ch, "sentinel_master_slaves", masterSlaves, masterName, masterAddress)
		e.registerConstMetricGauge(ch, "sentinel_master_sentinels", masterSentinels, masterName, masterAddress)
		return true
	}

	return false
}

func (e *Exporter) extractSentinelMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	masterDetails, err := redis.Values(doRedisCmd(c, "SENTINEL", "MASTERS"))
	if err != nil {
		log.Debugf("Error getting sentinel master details %s:", err)
		return
	}

	log.Debugf("Sentinel master details: %#v", masterDetails)

	for _, masterDetail := range masterDetails {
		masterDetailMap, err := redis.StringMap(masterDetail, nil)
		if err != nil {
			log.Debugf("Error getting masterDetailmap from masterDetail: %s, err: %s", masterDetail, err)
			continue
		}

		masterName, ok := masterDetailMap["name"]
		if !ok {
			continue
		}

		masterIp, ok := masterDetailMap["ip"]
		if !ok {
			continue
		}

		masterPort, ok := masterDetailMap["port"]
		if !ok {
			continue
		}
		masterAddr := masterIp + ":" + masterPort

		sentinelDetails, _ := redis.Values(doRedisCmd(c, "SENTINEL", "SENTINELS", masterName))
		log.Debugf("Sentinel details for master %s: %s", masterName, sentinelDetails)
		e.processSentinelSentinels(ch, sentinelDetails, masterName, masterAddr)

		slaveDetails, _ := redis.Values(doRedisCmd(c, "SENTINEL", "SLAVES", masterName))
		log.Debugf("Slave details for master %s: %s", masterName, slaveDetails)
		e.processSentinelSlaves(ch, slaveDetails, masterName, masterAddr)
	}
}

func (e *Exporter) processSentinelSentinels(ch chan<- prometheus.Metric, sentinelDetails []interface{}, labels ...string) {

	// If we are here then this master is in ok state
	masterOkSentinels := 1

	for _, sentinelDetail := range sentinelDetails {
		sentinelDetailMap, err := redis.StringMap(sentinelDetail, nil)
		if err != nil {
			log.Debugf("Error getting sentinelDetailMap from sentinelDetail: %s, err: %s", sentinelDetail, err)
			continue
		}

		sentinelFlags, ok := sentinelDetailMap["flags"]
		if !ok {
			continue
		}
		if strings.Contains(sentinelFlags, "o_down") {
			continue
		}
		if strings.Contains(sentinelFlags, "s_down") {
			continue
		}
		masterOkSentinels = masterOkSentinels + 1
	}
	e.registerConstMetricGauge(ch, "sentinel_master_ok_sentinels", float64(masterOkSentinels), labels...)
}

func (e *Exporter) processSentinelSlaves(ch chan<- prometheus.Metric, slaveDetails []interface{}, labels ...string) {
	masterOkSlaves := 0
	for _, slaveDetail := range slaveDetails {
		slaveDetailMap, err := redis.StringMap(slaveDetail, nil)
		if err != nil {
			log.Debugf("Error getting slavedetailMap from slaveDetail: %s, err: %s", slaveDetail, err)
			continue
		}

		slaveFlags, ok := slaveDetailMap["flags"]
		if !ok {
			continue
		}
		if strings.Contains(slaveFlags, "o_down") {
			continue
		}
		if strings.Contains(slaveFlags, "s_down") {
			continue
		}
		masterOkSlaves = masterOkSlaves + 1
	}
	e.registerConstMetricGauge(ch, "sentinel_master_ok_slaves", float64(masterOkSlaves), labels...)
}

/*
	valid examples:
		master0:name=user03,status=sdown,address=192.169.2.52:6381,slaves=1,sentinels=5
		master1:name=user02,status=ok,address=192.169.2.54:6380,slaves=1,sentinels=5
*/
func parseSentinelMasterString(master string, masterInfo string) (masterName string, masterStatus string, masterAddr string, masterSlaves float64, masterSentinels float64, ok bool) {
	ok = false
	if matched, _ := regexp.MatchString(`^master\d+`, master); !matched {
		return
	}
	matchedMasterInfo := make(map[string]string)
	for _, kvPart := range strings.Split(masterInfo, ",") {
		x := strings.Split(kvPart, "=")
		if len(x) != 2 {
			log.Errorf("Invalid format for sentinel's master string, got: %s", kvPart)
			continue
		}
		matchedMasterInfo[x[0]] = x[1]
	}

	masterName = matchedMasterInfo["name"]
	masterStatus = matchedMasterInfo["status"]
	masterAddr = matchedMasterInfo["address"]
	masterSlaves, err := strconv.ParseFloat(matchedMasterInfo["slaves"], 64)
	if err != nil {
		log.Debugf("parseSentinelMasterString(): couldn't parse slaves value, got: %s, err: %s", matchedMasterInfo["slaves"], err)
		return
	}
	masterSentinels, err = strconv.ParseFloat(matchedMasterInfo["sentinels"], 64)
	if err != nil {
		log.Debugf("parseSentinelMasterString(): couldn't parse sentinels value, got: %s, err: %s", matchedMasterInfo["sentinels"], err)
		return
	}
	ok = true

	return
}
