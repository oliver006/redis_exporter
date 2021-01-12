package exporter

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestSlowLog(t *testing.T) {
	e := getTestExporter()

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	oldSlowLogID := float64(0)

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "slowlog_last_id") {
				got := &dto.Metric{}
				m.Write(got)

				oldSlowLogID = got.GetGauge().GetValue()
			}
		}
	}

	setupSlowLog(t, os.Getenv("TEST_REDIS_URI"))
	defer resetSlowLog(t, os.Getenv("TEST_REDIS_URI"))

	chM = make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "slowlog_last_id") {
				got := &dto.Metric{}
				m.Write(got)

				val := got.GetGauge().GetValue()

				if oldSlowLogID > val {
					t.Errorf("no new slowlogs found")
				}
			}
			if strings.Contains(m.Desc().String(), "slowlog_length") {
				got := &dto.Metric{}
				m.Write(got)

				val := got.GetGauge().GetValue()
				if val == 0 {
					t.Errorf("slowlog length is zero")
				}
			}
		}
	}

	resetSlowLog(t, os.Getenv("TEST_REDIS_URI"))

	chM = make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	for m := range chM {
		switch m := m.(type) {
		case prometheus.Gauge:
			if strings.Contains(m.Desc().String(), "slowlog_length") {
				got := &dto.Metric{}
				m.Write(got)

				val := got.GetGauge().GetValue()
				if val != 0 {
					t.Errorf("Slowlog was not reset")
				}
			}
		}
	}
}

func setupSlowLog(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("CONFIG", "SET", "SLOWLOG-LOG-SLOWER-THAN", 10000)
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

func resetSlowLog(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("SLOWLOG", "RESET")
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	time.Sleep(time.Millisecond * 50)

	return nil
}
