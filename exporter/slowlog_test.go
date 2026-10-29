package exporter

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestSlowLog(t *testing.T) {
	e := getTestExporter(t)

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

func TestExcludeSlowlogMetrics(t *testing.T) {
	addr := os.Getenv("TEST_VALKEY9_URI")
	if addr == "" {
		t.Skipf("TEST_VALKEY9_URI not set - skipping")
	}

	logCommands := []string{"slowlog|len", "slowlog|get", "command|info", "commandlog|len"}
	logMetrics := []string{"slowlog_length", "slowlog_last_id", "last_slow_execution_duration_seconds", "commandlog_slow_length", "commandlog_execution_slower_than"}

	for _, tst := range []struct {
		name    string
		exclude bool
	}{
		{name: "included", exclude: false},
		{name: "excluded", exclude: true},
	} {
		t.Run(tst.name, func(t *testing.T) {
			e := getTestExporterWithAddrAndOptions(addr, Options{Namespace: "test", ExcludeSlowlogMetrics: tst.exclude})

			callsBefore := commandCalls(t, addr)
			names := collectMetricNames(e)
			callsAfter := commandCalls(t, addr)

			for _, cmd := range logCommands {
				sent := callsAfter[cmd] > callsBefore[cmd]
				if sent == tst.exclude {
					t.Errorf("command %q sent: %t, want %t", cmd, sent, !tst.exclude)
				}
			}
			for _, metric := range logMetrics {
				if names[metric] == tst.exclude {
					t.Errorf("metric %q exported: %t, want %t", metric, names[metric], !tst.exclude)
				}
			}
		})
	}
}

func collectMetricNames(e *Exporter) map[string]bool {
	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	names := map[string]bool{}
	for m := range chM {
		if sub := fqNameRE.FindStringSubmatch(m.Desc().String()); sub != nil {
			names[sub[1]] = true
		}
	}
	return names
}

// commandCalls returns the per-command call counts from INFO commandstats, keyed like "slowlog|len".
func commandCalls(t *testing.T, addr string) map[string]int64 {
	t.Helper()

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("couldn't connect to %s, err: %s", addr, err)
	}
	defer c.Close()

	info, err := redis.String(c.Do("INFO", "commandstats"))
	if err != nil {
		t.Fatalf("couldn't read INFO commandstats, err: %s", err)
	}

	calls := map[string]int64{}
	for _, line := range strings.Split(info, "\r\n") {
		rest, ok := strings.CutPrefix(line, "cmdstat_")
		if !ok {
			continue
		}
		cmd, stats, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		for _, kv := range strings.Split(stats, ",") {
			if n, ok := strings.CutPrefix(kv, "calls="); ok {
				calls[cmd], _ = strconv.ParseInt(n, 10, 64)
			}
		}
	}
	return calls
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
	_, err = c.Do("DEBUG", "SLEEP", latencyTestTimeToSleepInMillis/1000.0)
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
