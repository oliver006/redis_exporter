package exporter

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var reSentinelMaster = regexp.MustCompile(`^master\d+`)

func (e *Exporter) handleMetricsSentinel(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	switch fieldKey {
	case
		"sentinel_masters",
		"sentinel_tilt",
		"sentinel_running_scripts",
		"sentinel_scripts_queue_length",
		"sentinel_simulate_failure_flags":
		val, _ := strconv.Atoi(fieldValue)
		e.registerConstMetricGauge(ch, fieldKey, float64(val))
		return
	}

	if masterName, masterStatus, masterAddress, masterSlaves, masterSentinels, ok := parseSentinelMasterString(fieldKey, fieldValue); ok {
		masterStatusNum := 0.0
		if masterStatus == "ok" {
			masterStatusNum = 1
		}
		e.registerConstMetricGauge(ch, "sentinel_master_status", masterStatusNum, masterName, masterAddress, masterStatus)
		e.registerConstMetricGauge(ch, "sentinel_master_slaves", masterSlaves, masterName, masterAddress)
		e.registerConstMetricGauge(ch, "sentinel_master_sentinels", masterSentinels, masterName, masterAddress)
		return
	}
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

		masterCkquorumMsg, err := redis.String(doRedisCmd(c, "SENTINEL", "CKQUORUM", masterName))
		log.Debugf("Sentinel ckquorum status for master %s: %s %s", masterName, masterCkquorumMsg, err)
		masterCkquorumStatus := 1
		if err != nil {
			masterCkquorumStatus = 0
			masterCkquorumMsg = err.Error()
		}
		e.registerConstMetricGauge(ch, "sentinel_master_ckquorum_status", float64(masterCkquorumStatus), masterName, masterCkquorumMsg)

		masterCkquorum, _ := strconv.ParseFloat(masterDetailMap["quorum"], 64)
		masterFailoverTimeout, _ := strconv.ParseFloat(masterDetailMap["failover-timeout"], 64)
		masterParallelSyncs, _ := strconv.ParseFloat(masterDetailMap["parallel-syncs"], 64)
		masterDownAfterMs, _ := strconv.ParseFloat(masterDetailMap["down-after-milliseconds"], 64)
		masterConfigEpoch, _ := strconv.ParseFloat(masterDetailMap["config-epoch"], 64)
		masterLastOkPingReplyMs, _ := strconv.ParseFloat(masterDetailMap["last-ok-ping-reply"], 64)
		masterLastOkPingReplySeconds := masterLastOkPingReplyMs / 1000.0

		e.registerConstMetricGauge(ch, "sentinel_master_setting_ckquorum", masterCkquorum, masterName, masterAddr)
		e.registerConstMetricGauge(ch, "sentinel_master_setting_failover_timeout", masterFailoverTimeout, masterName, masterAddr)
		e.registerConstMetricGauge(ch, "sentinel_master_setting_parallel_syncs", masterParallelSyncs, masterName, masterAddr)
		e.registerConstMetricGauge(ch, "sentinel_master_setting_down_after_milliseconds", masterDownAfterMs, masterName, masterAddr)
		e.registerConstMetricGauge(ch, "sentinel_master_config_epoch", masterConfigEpoch, masterName, masterAddr)
		e.registerConstMetricGauge(ch, "sentinel_master_last_ok_ping_reply_seconds", masterLastOkPingReplySeconds, masterName, masterAddr)

		sentinelDetails, _ := redis.Values(doRedisCmd(c, "SENTINEL", "SENTINELS", masterName))
		log.Debugf("Sentinel details for master %s: %s", masterName, sentinelDetails)
		e.processSentinelSentinels(ch, sentinelDetails, masterName, masterAddr)

		slaveDetails, _ := redis.Values(doRedisCmd(c, "SENTINEL", "SLAVES", masterName))
		log.Debugf("Slave details for master %s: %s", masterName, slaveDetails)
		e.processSentinelSlaves(ch, slaveDetails, masterName, masterAddr)
	}
}

func (e *Exporter) extractSentinelConfig(ch chan<- prometheus.Metric, c redis.Conn) {
	if !e.options.InclConfigMetrics {
		return
	}
	sentinelConfig, err := redis.StringMap(doRedisCmd(c, "SENTINEL", "config", "get", "*"))
	if err != nil {
		log.Errorf("Error getting sentinel config: %s", err)
		return
	}

	log.Debugf("Sentinel config: %v", sentinelConfig)

	for strKey, strVal := range sentinelConfig {
		e.registerConstMetricGauge(ch, "sentinel_config_key_value", 1.0, strKey, strVal)
		if val, err := strconv.ParseFloat(strVal, 64); err == nil {
			e.registerConstMetricGauge(ch, "sentinel_config_value", val, strKey)
		}
	}
}

func (e *Exporter) processSentinelSentinels(ch chan<- prometheus.Metric, sentinelDetails []any, labels ...string) {

	// If we are here then this master is in ok state
	masterOkSentinels := 1
	hasMasterLabels := len(labels) >= 2
	masterName := ""
	masterAddr := ""
	if hasMasterLabels {
		masterName = labels[0]
		masterAddr = labels[1]
	}

	for _, sentinelDetail := range sentinelDetails {
		sentinelDetailMap, err := redis.StringMap(sentinelDetail, nil)
		if err != nil {
			log.Debugf("Error getting sentinelDetailMap from sentinelDetail: %s, err: %s", sentinelDetail, err)
			continue
		}

		name := ""
		if v, ok := sentinelDetailMap["name"]; ok {
			name = v
		}
		ip := ""
		if v, ok := sentinelDetailMap["ip"]; ok {
			ip = v
		}
		port := ""
		if v, ok := sentinelDetailMap["port"]; ok {
			port = v
		}
		runid := ""
		if v, ok := sentinelDetailMap["runid"]; ok {
			runid = v
		}
		flags := ""
		flagsFound := false
		if v, ok := sentinelDetailMap["flags"]; ok {
			flags = v
			flagsFound = true
		}
		if e.options.InclSentinelPeerInfo && hasMasterLabels {
			e.registerConstMetricGauge(ch, "sentinel_peer_info", 1, masterName, masterAddr, name, ip, port, runid, flags)
		}

		if !flagsFound {
			continue
		}
		if strings.Contains(flags, "o_down") {
			continue
		}
		if strings.Contains(flags, "s_down") {
			continue
		}
		masterOkSentinels = masterOkSentinels + 1
	}
	e.registerConstMetricGauge(ch, "sentinel_master_ok_sentinels", float64(masterOkSentinels), labels...)
}

func (e *Exporter) processSentinelSlaves(ch chan<- prometheus.Metric, slaveDetails []any, labels ...string) {
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
	if len(labels) >= 2 {
		e.registerConstMetricGauge(ch, "sentinel_master_ok_slaves", float64(masterOkSlaves), labels...)
	}
}

/*
valid examples:

	master0:name=user03,status=sdown,address=192.169.2.52:6381,slaves=1,sentinels=5
	master1:name=user02,status=ok,address=192.169.2.54:6380,slaves=1,sentinels=5
*/
func parseSentinelMasterString(master string, masterInfo string) (masterName string, masterStatus string, masterAddr string, masterSlaves float64, masterSentinels float64, ok bool) {
	ok = false
	if !reSentinelMaster.MatchString(master) {
		return
	}
	matchedMasterInfo := make(map[string]string)
	for kvPart := range strings.SplitSeq(masterInfo, ",") {
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
