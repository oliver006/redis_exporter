package exporter

import (
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
