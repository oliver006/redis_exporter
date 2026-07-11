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

const (
	commandLogTestKey      = "commandlog_test_key"
	commandLogPollInterval = 50 * time.Millisecond
	commandLogPollTimeout  = 2 * time.Second
)

func TestCommandLog(t *testing.T) {
	addr := os.Getenv("TEST_VALKEY8_URI")
	if addr == "" {
		t.Skipf("TEST_VALKEY8_URI not set - skipping")
	}

	e := getTestExporterWithAddr(addr)
	defer cleanupCommandLogTest(t, addr)

	t.Run("slow", func(t *testing.T) {
		resetCommandLog(t, addr, "slow")
		defer resetCommandLog(t, addr, "slow")

		setupCommandLogSlow(t, addr)

		assertCommandLogLength(t, e, "commandlog_slow_length", true)
		assertCommandLogGauge(t, e, "commandlog_execution_slower_than", 10000)
	})

	t.Run("large-request", func(t *testing.T) {
		resetCommandLog(t, addr, "large-request")
		defer resetCommandLog(t, addr, "large-request")

		setupCommandLogLargeRequest(t, addr)

		assertCommandLogLength(t, e, "commandlog_large_request_length", true)
		assertCommandLogGauge(t, e, "commandlog_request_larger_than", 10)
	})

	t.Run("large-reply", func(t *testing.T) {
		resetCommandLog(t, addr, "large-reply")
		defer resetCommandLog(t, addr, "large-reply")

		setupCommandLogLargeReply(t, addr)

		assertCommandLogLength(t, e, "commandlog_large_reply_length", true)
		assertCommandLogGauge(t, e, "commandlog_reply_larger_than", 10)
	})

	t.Run("reset", func(t *testing.T) {
		setupCommandLogSlow(t, addr)
		resetCommandLog(t, addr, "slow")

		assertCommandLogLength(t, e, "commandlog_slow_length", false)
	})
}

func assertCommandLogLength(t *testing.T, e *Exporter, metricName string, wantNonZero bool) {
	t.Helper()

	deadline := time.Now().Add(commandLogPollTimeout)
	for time.Now().Before(deadline) {
		val, found := collectCommandLogMetricValue(e, metricName)
		if !found {
			time.Sleep(commandLogPollInterval)
			continue
		}
		if wantNonZero && val > 0 {
			return
		}
		if !wantNonZero && val == 0 {
			return
		}
		time.Sleep(commandLogPollInterval)
	}

	val, found := collectCommandLogMetricValue(e, metricName)
	if !found {
		t.Errorf("metric %s not found", metricName)
		return
	}
	if wantNonZero && val == 0 {
		t.Errorf("%s is zero", metricName)
	}
	if !wantNonZero && val != 0 {
		t.Errorf("%s was not reset, got %f", metricName, val)
	}
}

func assertCommandLogGauge(t *testing.T, e *Exporter, metricName string, want float64) {
	t.Helper()

	deadline := time.Now().Add(commandLogPollTimeout)
	for time.Now().Before(deadline) {
		val, found := collectCommandLogMetricValue(e, metricName)
		if found && val == want {
			return
		}
		time.Sleep(commandLogPollInterval)
	}

	val, found := collectCommandLogMetricValue(e, metricName)
	if !found {
		t.Errorf("metric %s not found", metricName)
		return
	}
	if val != want {
		t.Errorf("%s = %f, want %f", metricName, val, want)
	}
}

func collectCommandLogMetricValue(e *Exporter, metricName string) (float64, bool) {
	fqName := prometheus.BuildFQName(e.options.Namespace, "", metricName)

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	var val float64
	found := false
	for m := range chM {
		if !strings.Contains(m.Desc().String(), `fqName: "`+fqName+`"`) {
			continue
		}

		got := &dto.Metric{}
		_ = m.Write(got)
		val = got.GetGauge().GetValue()
		found = true
	}

	return val, found
}

func commandLogLen(addr, logType string) (int64, error) {
	c, err := redis.DialURL(addr)
	if err != nil {
		return 0, err
	}
	defer c.Close()

	return redis.Int64(c.Do("COMMANDLOG", "LEN", logType))
}

func waitForCommandLogLen(t *testing.T, addr, logType string, want func(int64) bool) {
	t.Helper()

	deadline := time.Now().Add(commandLogPollTimeout)
	for time.Now().Before(deadline) {
		length, err := commandLogLen(addr, logType)
		if err == nil && want(length) {
			return
		}
		time.Sleep(commandLogPollInterval)
	}

	length, err := commandLogLen(addr, logType)
	if err != nil {
		t.Fatalf("timeout waiting for commandlog %s at %s, last err: %s", logType, addr, err)
	}
	t.Fatalf("timeout waiting for commandlog %s at %s, last length: %d", logType, addr, length)
}

func resetCommandLog(t *testing.T, addr string, logType string) {
	t.Helper()

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("couldn't connect to valkey, err: %s", err)
	}
	defer c.Close()

	if _, err = c.Do("COMMANDLOG", "RESET", logType); err != nil {
		t.Fatalf("couldn't reset commandlog %s, err: %s", logType, err)
	}
}

func setupCommandLogSlow(t *testing.T, addr string) {
	t.Helper()

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("couldn't connect to valkey, err: %s", err)
	}
	defer c.Close()

	if _, err = c.Do("CONFIG", "SET", "commandlog-execution-slower-than", 10000); err != nil {
		t.Fatalf("couldn't set commandlog-execution-slower-than, err: %s", err)
	}

	before, err := commandLogLen(addr, "slow")
	if err != nil {
		t.Fatalf("couldn't read commandlog slow length, err: %s", err)
	}

	_, err = c.Do("DEBUG", "SLEEP", latencyTestTimeToSleepInMillis/1000.0)
	if err != nil {
		t.Fatalf("couldn't trigger slow command, err: %s", err)
	}

	waitForCommandLogLen(t, addr, "slow", func(n int64) bool { return n > before })
}

func setupCommandLogLargeRequest(t *testing.T, addr string) {
	t.Helper()

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("couldn't connect to valkey, err: %s", err)
	}
	defer c.Close()

	if _, err = c.Do("CONFIG", "SET", "commandlog-request-larger-than", 10); err != nil {
		t.Fatalf("couldn't set commandlog-request-larger-than, err: %s", err)
	}

	before, err := commandLogLen(addr, "large-request")
	if err != nil {
		t.Fatalf("couldn't read commandlog large-request length, err: %s", err)
	}

	largeValue := strings.Repeat("x", 1024)
	if _, err = c.Do("SET", commandLogTestKey, largeValue); err != nil {
		t.Fatalf("couldn't trigger large request, err: %s", err)
	}

	waitForCommandLogLen(t, addr, "large-request", func(n int64) bool { return n > before })
}

func setupCommandLogLargeReply(t *testing.T, addr string) {
	t.Helper()

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("couldn't connect to valkey, err: %s", err)
	}
	defer c.Close()

	if _, err = c.Do("CONFIG", "SET", "commandlog-reply-larger-than", 10); err != nil {
		t.Fatalf("couldn't set commandlog-reply-larger-than, err: %s", err)
	}

	before, err := commandLogLen(addr, "large-reply")
	if err != nil {
		t.Fatalf("couldn't read commandlog large-reply length, err: %s", err)
	}

	largeValue := strings.Repeat("y", 1024)
	if _, err = c.Do("SET", commandLogTestKey, largeValue); err != nil {
		t.Fatalf("couldn't set large value, err: %s", err)
	}

	if _, err = c.Do("GET", commandLogTestKey); err != nil {
		t.Fatalf("couldn't trigger large reply, err: %s", err)
	}

	waitForCommandLogLen(t, addr, "large-reply", func(n int64) bool { return n > before })
}

func cleanupCommandLogTest(t *testing.T, addr string) {
	t.Helper()

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't connect to valkey for cleanup, err: %s", err)
		return
	}
	defer c.Close()

	if _, err = c.Do("DEL", commandLogTestKey); err != nil {
		t.Errorf("couldn't delete %s, err: %s", commandLogTestKey, err)
	}

	for _, logType := range []string{"slow", "large-request", "large-reply"} {
		if _, err = c.Do("COMMANDLOG", "RESET", logType); err != nil {
			t.Errorf("couldn't reset commandlog %s, err: %s", logType, err)
		}
	}

	for _, cfg := range []struct {
		key   string
		value int
	}{
		{"commandlog-execution-slower-than", 10000},
		{"commandlog-request-larger-than", 1048576},
		{"commandlog-reply-larger-than", 1048576},
	} {
		if _, err = c.Do("CONFIG", "SET", cfg.key, cfg.value); err != nil {
			t.Errorf("couldn't reset %s, err: %s", cfg.key, err)
		}
	}
}
