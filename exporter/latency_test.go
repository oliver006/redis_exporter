package exporter

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestLatencySpike(t *testing.T) {
	e := getTestExporter()

	setupLatency(t, os.Getenv("TEST_REDIS_URI"))
	defer resetLatency(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		if strings.Contains(m.Desc().String(), "latency_spike_duration_seconds") {
			got := &dto.Metric{}
			m.Write(got)

			// The metric value is in seconds, but our sleep interval is specified
			// in milliseconds, so we need to convert
			val := got.GetGauge().GetValue() * 1000
			// Because we're dealing with latency, there might be a slight delay
			// even after sleeping for a specific amount of time so checking
			// to see if we're between +-5 of our expected value
			if math.Abs(float64(TimeToSleep)-val) > 5 {
				t.Errorf("values not matching, %f != %f", float64(TimeToSleep), val)
			}
		}
	}

	resetLatency(t, os.Getenv("TEST_REDIS_URI"))

	chM = make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "latency_spike_duration_seconds") {
				t.Errorf("latency threshold was not reset")
			}
		}
	}
}

func setupLatency(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("CONFIG", "SET", "LATENCY-MONITOR-THRESHOLD", 100)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	// Have to pass in the sleep time in seconds so we have to divide
	// the number of milliseconds by 1000 to get number of seconds
	_, err = c.Do("DEBUG", "SLEEP", TimeToSleep/1000.0)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func resetLatency(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("LATENCY", "RESET")
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}

func TestLatencyHistogram(t *testing.T) {
	redisSevenAddr := os.Getenv("TEST_REDIS7_URI")

	// Since Redis v7 we should have latency histogram stats
	e := getTestExporterWithAddr(redisSevenAddr)
	setupDBKeys(t, redisSevenAddr)

	want := map[string]bool{"commands_latencies_usec": false}
	commandStatsCheck(t, e, want)
	deleteKeysFromDB(t, redisSevenAddr)
}

func TestExtractTotalUsecForCommand(t *testing.T) {
	statsOutString := `# Commandstats
cmdstat_testerr|1:calls=1,usec_per_call=5.00,rejected_calls=0,failed_calls=0
cmdstat_testerr:calls=1,usec=2,usec_per_call=5.00,rejected_calls=0,failed_calls=0
cmdstat_testerr2:calls=1,usec=-2,usec_per_call=5.00,rejected_calls=0,failed_calls=0
cmdstat_testerr3:calls=1,usec=` + fmt.Sprintf("%d1", uint64(math.MaxUint64)) + `,usec_per_call=5.00,rejected_calls=0,failed_calls=0
cmdstat_config|get:calls=69103,usec=15005068,usec_per_call=217.14,rejected_calls=0,failed_calls=0
cmdstat_config|set:calls=3,usec=58,usec_per_call=19.33,rejected_calls=0,failed_calls=3

# Latencystats
latency_percentiles_usec_pubsub|channels:p50=5.023,p99=5.023,p99.9=5.023
latency_percentiles_usec_config|get:p50=272.383,p99=346.111,p99.9=395.263
latency_percentiles_usec_config|set:p50=23.039,p99=27.007,p99.9=27.007`

	testMap := map[string]uint64{
		"config|set": 58,
		"config":     58 + 15005068,
		"testerr|1":  0,
		"testerr":    2 + 0,
		"testerr2":   0,
		"testerr3":   0,
	}

	for cmd, expected := range testMap {
		if res := extractTotalUsecForCommand(statsOutString, cmd); res != expected {
			t.Errorf("Incorrect usec extracted. Expected %d but got %d!", expected, res)
		}
	}
}
