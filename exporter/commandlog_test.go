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

const commandLogTestKey = "commandlog_test_key"

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
	})

	t.Run("large-request", func(t *testing.T) {
		resetCommandLog(t, addr, "large-request")
		defer resetCommandLog(t, addr, "large-request")

		setupCommandLogLargeRequest(t, addr)

		assertCommandLogLength(t, e, "commandlog_large_request_length", true)
	})

	t.Run("large-reply", func(t *testing.T) {
		resetCommandLog(t, addr, "large-reply")
		defer resetCommandLog(t, addr, "large-reply")

		setupCommandLogLargeReply(t, addr)

		assertCommandLogLength(t, e, "commandlog_large_reply_length", true)
	})

	t.Run("reset", func(t *testing.T) {
		setupCommandLogSlow(t, addr)
		resetCommandLog(t, addr, "slow")

		assertCommandLogLength(t, e, "commandlog_slow_length", false)
	})
}

func assertCommandLogLength(t *testing.T, e *Exporter, metricName string, wantNonZero bool) {
	t.Helper()

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	found := false
	for m := range chM {
		if !strings.Contains(m.Desc().String(), metricName) {
			continue
		}

		found = true
		got := &dto.Metric{}
		m.Write(got)

		val := got.GetGauge().GetValue()
		if wantNonZero && val == 0 {
			t.Errorf("%s is zero", metricName)
		}
		if !wantNonZero && val != 0 {
			t.Errorf("%s was not reset, got %f", metricName, val)
		}
	}

	if !found {
		t.Errorf("metric %s not found", metricName)
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

	_, err = c.Do("DEBUG", "SLEEP", latencyTestTimeToSleepInMillis/1000.0)
	if err != nil {
		t.Fatalf("couldn't trigger slow command, err: %s", err)
	}

	time.Sleep(50 * time.Millisecond)
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

	largeValue := strings.Repeat("x", 1024)
	if _, err = c.Do("SET", commandLogTestKey, largeValue); err != nil {
		t.Fatalf("couldn't trigger large request, err: %s", err)
	}

	time.Sleep(50 * time.Millisecond)
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

	largeValue := strings.Repeat("y", 1024)
	if _, err = c.Do("SET", commandLogTestKey, largeValue); err != nil {
		t.Fatalf("couldn't set large value, err: %s", err)
	}

	if _, err = c.Do("GET", commandLogTestKey); err != nil {
		t.Fatalf("couldn't trigger large reply, err: %s", err)
	}

	time.Sleep(50 * time.Millisecond)
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

	time.Sleep(50 * time.Millisecond)
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
}
