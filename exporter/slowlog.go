package exporter

import (
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func (e *Exporter) extractSlowLogMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	if reply, err := redis.Int64(doRedisCmd(c, "SLOWLOG", "LEN")); err == nil {
		e.registerConstMetricGauge(ch, "slowlog_length", float64(reply))
	}

	values, err := redis.Values(doRedisCmd(c, "SLOWLOG", "GET", "1"))
	if err != nil {
		return
	}

	var slowlogLastID int64
	var lastSlowExecutionDurationSeconds float64
	var commandDurationSeconds float64

	if len(values) > 0 {
		if values, err = redis.Values(values[0], err); err == nil && len(values) > 0 {
			slowlogLastID = values[0].(int64)
			if len(values) > 2 {
				lastSlowExecutionDurationSeconds = float64(values[2].(int64)) / 1e6
			}
		}
	}

	e.registerConstMetricGauge(ch, "slowlog_last_id", float64(slowlogLastID))
	e.registerConstMetricGauge(ch, "last_slow_execution_duration_seconds", lastSlowExecutionDurationSeconds)
	valuesArr, err := redis.Values(doRedisCmd(c, "SLOWLOG", "GET", "10"))
	//fmt.Println(doRedisCmd(c, "SLOWLOG", "GET", "10"))
	if err != nil {
		return
	}
	//fmt.Println("length of values ", len(valuesArr))
	for i := 0; i < len(valuesArr); i++ {
		//fmt.Println("executing the block", i)
		if values, err = redis.Values(valuesArr[i], err); err == nil && len(values) > 2 {
			tmp := values[1].(int64)
			commandExecutedTimeStamp := strconv.Itoa(int(tmp))
			//print(commandExecutedTimeStamp)
			commandDurationSeconds = float64(values[2].(int64)) / 1e6
			commandinfo, err := redis.Values(values[3], err)
			//fmt.Println(fmt.Println(commandname[1]))
			if err != nil {
				fmt.Println("error in values[3")
				fmt.Println(err)
				return
			}
			commandname, err := redis.Values(commandinfo, err)
			if err != nil {
				fmt.Println("error in values[0]")
				fmt.Println(err)
				return
			}
			//fmt.Println(string(commandname[0].([]uint8)))
			command := string(commandname[0].([]uint8))

			e.registerConstMetricGauge(ch, "slowlog_history_last_ten", commandDurationSeconds, commandExecutedTimeStamp, command)

		}

	}
	minDaysToExpire, err := extractCertExpiryMetrics()
	if err != nil {
		return
	}
	e.registerConstMetricGauge(ch, "ssl_cert_expire_days", float64(minDaysToExpire))

}

func extractCertExpiryMetrics() (int, error) {
	ip := getHostIp()
	ip = ip + ":6380"
	fmt.Println("ip ", ip)
	conn, err := tls.Dial("tcp", ip, &tls.Config{
		ServerName:         "test",
		InsecureSkipVerify: true,
	})
	if err != nil {
		return 0, err
	}
	certs := conn.ConnectionState().PeerCertificates
	timeFormat := "2006-01-02"

	minDaysToExpire := math.MaxInt
	for _, cert := range certs {
		certExpireTime := cert.NotAfter.Format(timeFormat)
		t, _ := time.Parse(timeFormat, certExpireTime)
		duration := t.Sub(time.Now())
		daysToExpire := int(duration.Hours() / 24)
		if daysToExpire < minDaysToExpire {
			minDaysToExpire = daysToExpire
		}
	}
	return minDaysToExpire, nil

}
func getHostIp() string {

	// get hostname
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)

	}

	// perform lookup of the hostname
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		panic(err)
	}

	// fetch ip address details
	for _, addr := range addrs {
		return addr
	}
	return "127.0.0.1"
}
