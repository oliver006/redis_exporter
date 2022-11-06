package exporter

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// precompiled regexps

// keydb multimaster
/*
master_host:kdb0.server.local
master_port:6377
master_1_host:kdb1.server.local
master_1_port:6377
*/
var reMasterHost = regexp.MustCompile(`^master(_[0-9]+)?_host`)
var reMasterPort = regexp.MustCompile(`^master(_[0-9]+)?_port`)
var reMasterLinkStatus = regexp.MustCompile(`^master(_[0-9]+)?_link_status`)

// info fieldKey:fieldValue -> metric redis_fieldKey{master_host, master_port} fieldValue
var reMasterDirect = regexp.MustCompile(`^(master(_[0-9]+)?_(last_io_seconds_ago|sync_in_progress)|slave_repl_offset)`)

// numbered slaves
/*
slave0:ip=10.254.11.1,port=6379,state=online,offset=1751844676,lag=0
slave1:ip=10.254.11.2,port=6379,state=online,offset=1751844222,lag=0
*/
var reSlave = regexp.MustCompile(`^slave\d+`)

func extractVal(s string) (val float64, err error) {
	split := strings.Split(s, "=")
	if len(split) != 2 {
		return 0, fmt.Errorf("nope")
	}
	val, err = strconv.ParseFloat(split[1], 64)
	if err != nil {
		return 0, fmt.Errorf("nope")
	}
	return
}

func extractPercentileVal(s string) (percentile float64, val float64, err error) {
	split := strings.Split(s, "=")
	if len(split) != 2 {
		return
	}
	percentile, err = strconv.ParseFloat(split[0][1:], 64)
	if err != nil {
		return
	}
	val, err = strconv.ParseFloat(split[1], 64)
	return
}

func (e *Exporter) extractInfoMetrics(ch chan<- prometheus.Metric, info string, dbCount int) {
	keyValues := map[string]string{}
	handledDBs := map[string]bool{}
	cmdCount := map[string]uint64{}
	cmdSum := map[string]float64{}
	cmdLatencyMap := map[string]map[float64]float64{}

	fieldClass := ""
	lines := strings.Split(info, "\n")
	masterHost := ""
	masterPort := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		log.Debugf("info: %s", line)
		if len(line) > 0 && strings.HasPrefix(line, "# ") {
			fieldClass = line[2:]
			log.Debugf("set fieldClass: %s", fieldClass)
			continue
		}

		if (len(line) < 2) || (!strings.Contains(line, ":")) {
			continue
		}

		split := strings.SplitN(line, ":", 2)
		fieldKey := split[0]
		fieldValue := split[1]

		keyValues[fieldKey] = fieldValue

		if reMasterHost.MatchString(fieldKey) {
			masterHost = fieldValue
		}

		if reMasterPort.MatchString(fieldKey) {
			masterPort = fieldValue
		}

		switch fieldClass {

		case "Replication":
			if ok := e.handleMetricsReplication(ch, masterHost, masterPort, fieldKey, fieldValue); ok {
				continue
			}

		case "Server":
			e.handleMetricsServer(ch, fieldKey, fieldValue)

		case "Commandstats":
			cmd, calls, usecsTotal := e.handleMetricsCommandStats(ch, fieldKey, fieldValue)
			cmdCount[cmd] = uint64(calls)
			cmdSum[cmd] = usecsTotal
			continue

		case "Latencystats":
			e.handleMetricsLatencyStats(fieldKey, fieldValue, cmdLatencyMap)
			continue

		case "Errorstats":
			e.handleMetricsErrorStats(ch, fieldKey, fieldValue)
			continue

		case "Keyspace":
			if keysTotal, keysEx, avgTTL, keysCached, ok := parseDBKeyspaceString(fieldKey, fieldValue); ok {
				dbName := fieldKey

				e.registerConstMetricGauge(ch, "db_keys", keysTotal, dbName)
				e.registerConstMetricGauge(ch, "db_keys_expiring", keysEx, dbName)
				if keysCached > -1 {
					e.registerConstMetricGauge(ch, "db_keys_cached", keysCached, dbName)
				}
				if avgTTL > -1 {
					e.registerConstMetricGauge(ch, "db_avg_ttl_seconds", avgTTL, dbName)
				}
				handledDBs[dbName] = true
				continue
			}

		case "Sentinel":
			e.handleMetricsSentinel(ch, fieldKey, fieldValue)
		}

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}

	// To be able to generate the latency summaries we need the count and sum that we get
	// from #Commandstats processing and the percentile info that we get from the #Latencystats processing
	e.generateCommandLatencySummaries(ch, cmdLatencyMap, cmdCount, cmdSum)

	for dbIndex := 0; dbIndex < dbCount; dbIndex++ {
		dbName := "db" + strconv.Itoa(dbIndex)
		if _, exists := handledDBs[dbName]; !exists {
			e.registerConstMetricGauge(ch, "db_keys", 0, dbName)
			e.registerConstMetricGauge(ch, "db_keys_expiring", 0, dbName)
		}
	}

	e.registerConstMetricGauge(ch, "instance_info", 1,
		keyValues["role"],
		keyValues["redis_version"],
		keyValues["redis_build_id"],
		keyValues["redis_mode"],
		keyValues["os"],
		keyValues["maxmemory_policy"],
		keyValues["tcp_port"], keyValues["run_id"], keyValues["process_id"],
	)

	if keyValues["role"] == "slave" {
		e.registerConstMetricGauge(ch, "slave_info", 1,
			keyValues["master_host"],
			keyValues["master_port"],
			keyValues["slave_read_only"])
	}
}

func (e *Exporter) generateCommandLatencySummaries(ch chan<- prometheus.Metric, cmdLatencyMap map[string]map[float64]float64, cmdCount map[string]uint64, cmdSum map[string]float64) {
	for cmd, latencyMap := range cmdLatencyMap {
		count, okCount := cmdCount[cmd]
		sum, okSum := cmdSum[cmd]
		if okCount && okSum {
			e.registerConstSummary(ch, "latency_percentiles_usec", []string{"cmd"}, count, sum, latencyMap, cmd)
		}
	}
}

func (e *Exporter) extractClusterInfoMetrics(ch chan<- prometheus.Metric, info string) {
	lines := strings.Split(info, "\r\n")

	for _, line := range lines {
		log.Debugf("info: %s", line)

		split := strings.Split(line, ":")
		if len(split) != 2 {
			continue
		}
		fieldKey := split[0]
		fieldValue := split[1]

		if !e.includeMetric(fieldKey) {
			continue
		}
		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}
}

/*
	valid example: db0:keys=1,expires=0,avg_ttl=0,cached_keys=0
*/
func parseDBKeyspaceString(inputKey string, inputVal string) (keysTotal float64, keysExpiringTotal float64, avgTTL float64, keysCachedTotal float64, ok bool) {
	log.Debugf("parseDBKeyspaceString inputKey: [%s] inputVal: [%s]", inputKey, inputVal)

	if !strings.HasPrefix(inputKey, "db") {
		log.Debugf("parseDBKeyspaceString inputKey not starting with 'db': [%s]", inputKey)
		return
	}

	split := strings.Split(inputVal, ",")
	if len(split) < 2 || len(split) > 4 {
		log.Debugf("parseDBKeyspaceString strings.Split(inputVal) invalid: %#v", split)
		return
	}

	var err error
	if keysTotal, err = extractVal(split[0]); err != nil {
		log.Debugf("parseDBKeyspaceString extractVal(split[0]) invalid, err: %s", err)
		return
	}
	if keysExpiringTotal, err = extractVal(split[1]); err != nil {
		log.Debugf("parseDBKeyspaceString extractVal(split[1]) invalid, err: %s", err)
		return
	}

	avgTTL = -1
	if len(split) > 2 {
		if avgTTL, err = extractVal(split[2]); err != nil {
			log.Debugf("parseDBKeyspaceString extractVal(split[2]) invalid, err: %s", err)
			return
		}
		avgTTL /= 1000
	}

	keysCachedTotal = -1
	if len(split) > 3 {
		if keysCachedTotal, err = extractVal(split[3]); err != nil {
			log.Debugf("parseDBKeyspaceString extractVal(split[3]) invalid, err: %s", err)
			return
		}
	}

	ok = true
	return
}

/*
	slave0:ip=10.254.11.1,port=6379,state=online,offset=1751844676,lag=0
	slave1:ip=10.254.11.2,port=6379,state=online,offset=1751844222,lag=0
*/
func parseConnectedSlaveString(slaveName string, keyValues string) (offset float64, ip string, port string, state string, lag float64, ok bool) {
	ok = false
	if !reSlave.MatchString(slaveName) {
		return
	}
	connectedkeyValues := make(map[string]string)
	for _, kvPart := range strings.Split(keyValues, ",") {
		x := strings.Split(kvPart, "=")
		if len(x) != 2 {
			log.Debugf("Invalid format for connected slave string, got: %s", kvPart)
			return
		}
		connectedkeyValues[x[0]] = x[1]
	}
	offset, err := strconv.ParseFloat(connectedkeyValues["offset"], 64)
	if err != nil {
		log.Debugf("Can not parse connected slave offset, got: %s", connectedkeyValues["offset"])
		return
	}

	if lagStr, exists := connectedkeyValues["lag"]; !exists {
		// Prior to Redis 3.0, "lag" property does not exist
		lag = -1
	} else {
		lag, err = strconv.ParseFloat(lagStr, 64)
		if err != nil {
			log.Debugf("Can not parse connected slave lag, got: %s", lagStr)
			return
		}
	}

	ok = true
	ip = connectedkeyValues["ip"]
	port = connectedkeyValues["port"]
	state = connectedkeyValues["state"]

	return
}

func (e *Exporter) handleMetricsReplication(ch chan<- prometheus.Metric, masterHost string, masterPort string, fieldKey string, fieldValue string) bool {
	// only slaves have this field
	if reMasterLinkStatus.MatchString(fieldKey) {
		if fieldValue == "up" {
			e.registerConstMetricGauge(ch, "master_link_up", 1, masterHost, masterPort)
		} else {
			e.registerConstMetricGauge(ch, "master_link_up", 0, masterHost, masterPort)
		}
		return true
	}

	if reMasterDirect.MatchString(fieldKey) {
		if strings.HasSuffix(fieldKey, "last_io_seconds_ago") {
			fieldKey = "master_last_io_seconds_ago"
		} else if strings.HasSuffix(fieldKey, "sync_in_progress") {
			fieldKey = "master_sync_in_progress"
		}
		val, _ := strconv.Atoi(fieldValue)
		e.registerConstMetricGauge(ch, fieldKey, float64(val), masterHost, masterPort)
		return true
	}

	// not a slave, try extracting master metrics
	if slaveOffset, slaveIP, slavePort, slaveState, slaveLag, ok := parseConnectedSlaveString(fieldKey, fieldValue); ok {
		e.registerConstMetricGauge(ch,
			"connected_slave_offset_bytes",
			slaveOffset,
			slaveIP, slavePort, slaveState,
		)

		if slaveLag > -1 {
			e.registerConstMetricGauge(ch,
				"connected_slave_lag_seconds",
				slaveLag,
				slaveIP, slavePort, slaveState,
			)
		}
		return true
	}

	return false
}

func (e *Exporter) handleMetricsServer(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	if fieldKey == "uptime_in_seconds" {
		if uptime, err := strconv.ParseFloat(fieldValue, 64); err == nil {
			e.registerConstMetricGauge(ch, "start_time_seconds", float64(time.Now().Unix())-uptime)
		}
	}
}

func parseMetricsCommandStats(fieldKey string, fieldValue string) (cmd string, calls float64, rejectedCalls float64, failedCalls float64, usecTotal float64, extendedStats bool, errorOut error) {
	/*
		There are 2 formats. (One before Redis 6.2 and one after it)
		Format before v6.2:
			cmdstat_get:calls=21,usec=175,usec_per_call=8.33
			cmdstat_set:calls=61,usec=3139,usec_per_call=51.46
			cmdstat_setex:calls=75,usec=1260,usec_per_call=16.80
			cmdstat_georadius_ro:calls=75,usec=1260,usec_per_call=16.80
		Format from v6.2 forward:
			cmdstat_get:calls=21,usec=175,usec_per_call=8.33,rejected_calls=0,failed_calls=0
			cmdstat_set:calls=61,usec=3139,usec_per_call=51.46,rejected_calls=0,failed_calls=0
			cmdstat_setex:calls=75,usec=1260,usec_per_call=16.80,rejected_calls=0,failed_calls=0
			cmdstat_georadius_ro:calls=75,usec=1260,usec_per_call=16.80,rejected_calls=0,failed_calls=0

		broken up like this:
			fieldKey  = cmdstat_get
			fieldValue= calls=21,usec=175,usec_per_call=8.33
	*/

	const cmdPrefix = "cmdstat_"
	extendedStats = false

	if !strings.HasPrefix(fieldKey, cmdPrefix) {
		errorOut = errors.New("Invalid fieldKey")
		return
	}
	cmd = strings.TrimPrefix(fieldKey, cmdPrefix)

	splitValue := strings.Split(fieldValue, ",")
	splitLen := len(splitValue)
	if splitLen < 3 {
		errorOut = errors.New("Invalid fieldValue")
		return
	}

	// internal error variable
	var err error
	calls, err = extractVal(splitValue[0])
	if err != nil {
		errorOut = errors.New("Invalid splitValue[0]")
		return
	}

	usecTotal, err = extractVal(splitValue[1])
	if err != nil {
		errorOut = errors.New("Invalid splitValue[1]")
		return
	}

	// pre 6.2 did not include rejected/failed calls stats so if we have less than 5 tokens we're done here
	if splitLen < 5 {
		return
	}

	rejectedCalls, err = extractVal(splitValue[3])
	if err != nil {
		errorOut = errors.New("Invalid rejected_calls while parsing splitValue[3]")
		return
	}

	failedCalls, err = extractVal(splitValue[4])
	if err != nil {
		errorOut = errors.New("Invalid failed_calls while parsing splitValue[4]")
		return
	}
	extendedStats = true
	return
}

func parseMetricsLatencyStats(fieldKey string, fieldValue string) (cmd string, percentileMap map[float64]float64, errorOut error) {
	/*
		# Latencystats
		latency_percentiles_usec_rpop:p50=0.001,p99=1.003,p99.9=4.015
		latency_percentiles_usec_zadd:p50=0.001,p99=1.003,p99.9=4.015
		latency_percentiles_usec_hset:p50=0.001,p99=1.003,p99.9=3.007
		latency_percentiles_usec_set:p50=0.001,p99=1.003,p99.9=4.015
		latency_percentiles_usec_lpop:p50=0.001,p99=1.003,p99.9=4.015
		latency_percentiles_usec_lpush:p50=0.001,p99=1.003,p99.9=4.015
		latency_percentiles_usec_lrange:p50=17.023,p99=21.119,p99.9=27.007
		latency_percentiles_usec_get:p50=0.001,p99=1.003,p99.9=3.007
		latency_percentiles_usec_mset:p50=1.003,p99=1.003,p99.9=1.003
		latency_percentiles_usec_spop:p50=0.001,p99=1.003,p99.9=1.003
		latency_percentiles_usec_incr:p50=0.001,p99=1.003,p99.9=3.007
		latency_percentiles_usec_rpush:p50=0.001,p99=1.003,p99.9=4.015
		latency_percentiles_usec_zpopmin:p50=0.001,p99=1.003,p99.9=3.007
		latency_percentiles_usec_config|resetstat:p50=280.575,p99=280.575,p99.9=280.575
		latency_percentiles_usec_config|get:p50=8.031,p99=27.007,p99.9=27.007
		latency_percentiles_usec_ping:p50=0.001,p99=1.003,p99.9=1.003
		latency_percentiles_usec_sadd:p50=0.001,p99=1.003,p99.9=3.007

		broken up like this:
			fieldKey  = latency_percentiles_usec_ping
			fieldValue= p50=0.001,p99=1.003,p99.9=3.007
	*/

	const cmdPrefix = "latency_percentiles_usec_"
	percentileMap = map[float64]float64{}

	if !strings.HasPrefix(fieldKey, cmdPrefix) {
		errorOut = errors.New("Invalid fieldKey")
		return
	}
	cmd = strings.TrimPrefix(fieldKey, cmdPrefix)
	splitValue := strings.Split(fieldValue, ",")
	splitLen := len(splitValue)
	if splitLen < 1 {
		errorOut = errors.New("Invalid fieldValue")
		return
	}
	for pos, kv := range splitValue {
		percentile, value, err := extractPercentileVal(kv)
		if err != nil {
			errorOut = fmt.Errorf("Invalid splitValue[%d]", pos)
			return
		}
		percentileMap[percentile] = value
	}
	return
}

func parseMetricsErrorStats(fieldKey string, fieldValue string) (errorType string, count float64, errorOut error) {
	/*
		Format:
			errorstat_ERR:count=4
			errorstat_NOAUTH:count=3

		broken up like this:
			fieldKey  = errorstat_ERR
			fieldValue= count=3
	*/

	const prefix = "errorstat_"

	if !strings.HasPrefix(fieldKey, prefix) {
		errorOut = errors.New("Invalid fieldKey. errorstat_ prefix not present")
		return
	}
	errorType = strings.TrimPrefix(fieldKey, prefix)
	count, err := extractVal(fieldValue)
	if err != nil {
		errorOut = errors.New("Invalid error type on splitValue[0]")
		return
	}
	return
}

func (e *Exporter) handleMetricsCommandStats(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) (cmd string, calls float64, usecTotal float64) {
	cmd, calls, rejectedCalls, failedCalls, usecTotal, extendedStats, err := parseMetricsCommandStats(fieldKey, fieldValue)
	if err == nil {
		e.registerConstMetric(ch, "commands_total", calls, prometheus.CounterValue, cmd)
		e.registerConstMetric(ch, "commands_duration_seconds_total", usecTotal/1e6, prometheus.CounterValue, cmd)
		if extendedStats {
			e.registerConstMetric(ch, "commands_rejected_calls_total", rejectedCalls, prometheus.CounterValue, cmd)
			e.registerConstMetric(ch, "commands_failed_calls_total", failedCalls, prometheus.CounterValue, cmd)
		}
	}
	return
}

func (e *Exporter) handleMetricsLatencyStats(fieldKey string, fieldValue string, cmdLatencyMap map[string]map[float64]float64) {
	cmd, latencyMap, err := parseMetricsLatencyStats(fieldKey, fieldValue)
	if err == nil {
		cmdLatencyMap[cmd] = latencyMap
	}
}

func (e *Exporter) handleMetricsErrorStats(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	if errorPrefix, count, err := parseMetricsErrorStats(fieldKey, fieldValue); err == nil {
		e.registerConstMetric(ch, "errors_total", count, prometheus.CounterValue, errorPrefix)
	}
}
