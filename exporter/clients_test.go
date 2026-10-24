package exporter

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestDurationFieldToTimestamp(t *testing.T) {
	nowTs := time.Now().Unix()
	for _, tst := range []struct {
		in          string
		expectedOk  bool
		expectedVal int64
	}{
		{
			in:          "123",
			expectedOk:  true,
			expectedVal: nowTs - 123,
		},
		{
			in:          "0",
			expectedOk:  true,
			expectedVal: nowTs - 0,
		},
		{
			in:         "abc",
			expectedOk: false,
		},
	} {
		res, err := durationFieldToTimestamp(tst.in)
		if err == nil && !tst.expectedOk {
			t.Fatalf("expected not ok, but got no error, input: [%s]", tst.in)
		} else if err != nil && tst.expectedOk {
			t.Fatalf("expected ok, but got error: %s, input: [%s]", err, tst.in)
		}
		if tst.expectedOk {
			if res != tst.expectedVal {
				t.Fatalf("expected %d, but got: %d", tst.expectedVal, res)
			}
		}
	}
}

func TestParseClientListString(t *testing.T) {
	convertDurationToTimestampInt64 := func(duration string) int64 {
		ts, err := durationFieldToTimestamp(duration)
		if err != nil {
			panic(err)
		}
		return ts
	}

	tsts := []struct {
		in           string
		expectedOk   bool
		expectedInfo ClientInfo
	}{
		{
			in:           "id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=3 oll=8 omem=0 tot-mem=0 events=r cmd=setex",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "11", CreatedAt: convertDurationToTimestampInt64("6321"), IdleSince: convertDurationToTimestampInt64("6320"), IdleSeconds: 6320, Flags: "N", Db: "0", Ssub: -1, Watch: -1, Obl: 3, Oll: 8, OMem: 0, TotMem: 0, Host: "127.0.0.1", Port: "63508"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=client",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Name: "foo", CreatedAt: convertDurationToTimestampInt64("5"), IdleSince: convertDurationToTimestampInt64("0"), Flags: "N", Db: "1", Ssub: -1, Watch: -1, Qbuf: 26, QbufFree: 32742, OMem: 0, TotMem: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64959 fd=9 name= age=5 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=client user=default resp=3",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", CreatedAt: convertDurationToTimestampInt64("5"), IdleSince: convertDurationToTimestampInt64("0"), Flags: "N", Db: "0", Ssub: -1, Watch: -1, Qbuf: 26, QbufFree: 32742, OMem: 0, TotMem: 0, Host: "127.0.0.1", Port: "64959", User: "default", Resp: "3"},
		}, {
			in:           "id=40253233 addr=fd40:1481:21:dbe0:7021:300:a03:1a06:44426 fd=19 name= age=782 idle=0 flags=N db=0 sub=896 psub=18 ssub=17 watch=3 multi=-1 qbuf=26 qbuf-free=32742 argv-mem=10 obl=0 oll=555 omem=0 tot-mem=61466 ow=0 owmem=0 events=r cmd=client user=default lib-name=redis-py lib-ver=5.0.1 numops=9",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "40253233", CreatedAt: convertDurationToTimestampInt64("782"), IdleSince: convertDurationToTimestampInt64("0"), Flags: "N", Db: "0", Sub: 896, Psub: 18, Ssub: 17, Watch: 3, Qbuf: 26, QbufFree: 32742, Oll: 555, OMem: 0, TotMem: 61466, Host: "fd40:1481:21:dbe0:7021:300:a03:1a06", Port: "44426", User: "default"},
		}, {
			in:         "id=15 addr=127.0.0.1:64960 name=worker read-events=42 avg-pipeline-len-sum=12 avg-pipeline-len-cnt=4",
			expectedOk: true,
			expectedInfo: ClientInfo{
				Id: "15", Name: "worker", Ssub: -1, Watch: -1, Host: "127.0.0.1", Port: "64960",
				ReadEvents: 42, PipelineLengthSum: 12, PipelineLengthCount: 4,
				readEventsAvailable: true, pipelineLengthSumAvailable: true, pipelineLengthCountAvailable: true,
			},
		}, {
			in:           "id=17 addr=127.0.0.1:1234 name=app=web lib-name=test=lib",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "17", Name: "app=web", Ssub: -1, Watch: -1, Host: "127.0.0.1", Port: "1234"},
		}, {
			in:         "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=ABCDE idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=client",
			expectedOk: false,
		}, {
			in:         "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=NOPE flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=client",
			expectedOk: false,
		}, {
			in:           "id=14 addr=127.0.0.1:64958 sub=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Sub: 0, Ssub: -1, Watch: -1, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 psub=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Psub: 0, Ssub: -1, Watch: -1, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 ssub=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: 0, Watch: -1, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 watch=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 qbuf=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: -1, Qbuf: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 qbuf-free=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: -1, QbufFree: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 obl=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: -1, Obl: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 oll=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: -1, Oll: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 omem=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: -1, OMem: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 tot-mem=ERR",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Ssub: -1, Watch: -1, TotMem: 0, Host: "127.0.0.1", Port: "64958"},
		}, {
			in:         "",
			expectedOk: false,
		},
	}

	for _, tst := range tsts {
		info, ok := parseClientListString(tst.in)
		if !tst.expectedOk {
			if ok {
				t.Errorf("expected NOT ok, but got ok, input: %s", tst.in)
			}
			continue
		}

		if *info != tst.expectedInfo {
			t.Errorf("TestParseClientListString( %s ) error. Given: %#v Wanted: %#v", tst.in, info, tst.expectedInfo)
		}
	}
}

func TestRedisEightEightClientProcessingMetrics(t *testing.T) {
	e := getTestExporterWithOptions(t, Options{Namespace: "test", ExportClientList: true})
	input := "id=15 addr=127.0.0.1:64960 name=worker read-events=42 avg-pipeline-len-sum=12 avg-pipeline-len-cnt=4\n"
	want := map[string]float64{
		"connected_client_read_events_total":       42,
		"connected_client_pipeline_commands_total": 12,
		"connected_client_pipeline_batches_total":  4,
	}
	found := map[string]bool{}

	ch := make(chan prometheus.Metric)
	go func() {
		e.parseConnectedClientMetrics(input, ch)
		close(ch)
	}()
	for metric := range ch {
		for name, expected := range want {
			if !strings.Contains(metric.Desc().String(), `fqName: "test_`+name+`"`) {
				continue
			}
			var got dto.Metric
			if err := metric.Write(&got); err != nil {
				t.Fatalf("metric.Write(%s) err: %s", name, err)
			}
			if got.Counter == nil {
				t.Errorf("%s is not a counter", name)
				continue
			}
			if got.Counter.GetValue() != expected {
				t.Errorf("%s value = %v, want %v", name, got.Counter.GetValue(), expected)
			}
			found[name] = true
		}
	}
	for name := range want {
		if !found[name] {
			t.Errorf("metric %s was not emitted", name)
		}
	}
}

func TestIdleClientMetrics(t *testing.T) {
	for _, tt := range []struct {
		name      string
		input     string
		threshold int64
		err       error
		want      float64
	}{
		{name: "disabled", input: "id=1 addr=127.0.0.1:1234 idle=61 flags=N\n"},
		{name: "empty", threshold: 60},
		{
			name: "threshold boundary", threshold: 60, want: 1,
			input: "id=1 addr=127.0.0.1:1234 idle=59 flags=N\n" +
				"id=2 addr=127.0.0.1:1235 idle=60 flags=N\n" +
				"id=3 addr=127.0.0.1:1236 idle=61 flags=N\n",
		},
		{name: "custom threshold", threshold: 10, want: 1, input: "id=1 addr=[::1]:1234 idle=11 flags=N\n"},
		{
			name: "equals in client metadata", threshold: 60, want: 2,
			input: "id=1 addr=127.0.0.1:1234 name=app=web idle=61 flags=N\n" +
				"id=2 addr=127.0.0.1:1235 idle=61 flags=N lib-name=test=lib\n",
		},
		{
			name: "special clients", threshold: 60, want: 1,
			input: "id=1 addr=127.0.0.1:1234 idle=100 flags=b\n" +
				"id=2 addr=127.0.0.1:1235 idle=100 flags=P\n" +
				"id=3 addr=127.0.0.1:1236 idle=100 flags=S\n" +
				"id=4 addr=127.0.0.1:1237 idle=100 flags=M\n" +
				"id=5 addr=127.0.0.1:1238 idle=100 flags=bU\n" +
				"id=6 addr=127.0.0.1:1239 idle=100 flags=x\n" +
				"id=7 addr=127.0.0.1:1240 idle=100 flags=O\n" +
				"id=8 addr=127.0.0.1:1241 idle=100 flags=OU\n",
		},
		{
			name: "invalid or missing idle", threshold: 60,
			input: "id=1 addr=127.0.0.1:1234 idle=invalid flags=N\n" +
				"id=2 addr=127.0.0.1:1235 flags=N\n" +
				"id=3 addr=127.0.0.1:1236 idle=-1 flags=N\n" +
				"id=4 addr=127.0.0.1:1237 idle=9223372036854775808 flags=N\n" +
				"invalid\n",
		},
		{name: "command error", threshold: 60, err: redis.Error("NOPERM CLIENT LIST is not allowed")},
	} {
		for _, exportClientList := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/export-client-list=%t", tt.name, exportClientList), func(t *testing.T) {
				e, err := NewRedisExporter("", Options{Namespace: "test", ExportClientList: exportClientList, ExportClientIdleThreshold: tt.threshold})
				if err != nil {
					t.Fatal(err)
				}
				c := &queuedRedisConn{replies: []redisReply{{value: tt.input, err: tt.err}}}
				ch := make(chan prometheus.Metric, 128)
				e.extractConnectedClientMetrics(ch, c)
				close(ch)
				if !reflect.DeepEqual(c.calls, [][]any{{"CLIENT", "LIST"}}) {
					t.Errorf("commands = %v, want a single CLIENT LIST", c.calls)
				}
				found := 0
				for metric := range ch {
					if !strings.Contains(metric.Desc().String(), `fqName: "test_idle_clients"`) {
						if !exportClientList {
							t.Errorf("unexpected per-client metric: %s", metric.Desc())
						}
						continue
					}
					found++
					var got dto.Metric
					if err := metric.Write(&got); err != nil {
						t.Fatal(err)
					}
					if got.Gauge == nil || got.Gauge.GetValue() != tt.want || len(got.Label) != 0 {
						t.Errorf("idle_clients = %v, want an unlabeled gauge with value %v", &got, tt.want)
					}
				}
				wantCount := 0
				if tt.threshold > 0 && tt.err == nil {
					wantCount = 1
				}
				if found != wantCount {
					t.Errorf("idle_clients metric count = %d, want %d", found, wantCount)
				}
			})
		}
	}
}

func TestNegativeClientIdleThreshold(t *testing.T) {
	if _, err := NewRedisExporter("", Options{ExportClientIdleThreshold: -1}); err == nil {
		t.Fatal("expected an error for a negative idle threshold")
	}
}

func TestExportClientList(t *testing.T) {
	for _, opts := range []Options{
		{Namespace: "test"},
		{Namespace: "test", ExportClientList: true},
		{Namespace: "test", ExportClientIdleThreshold: 60},
		{Namespace: "test", ExportClientList: true, ExportClientIdleThreshold: 60},
	} {
		e := getTestExporterWithOptions(t, opts)

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		tsts := []struct {
			in    string
			found bool
		}{
			{in: "connected_client_info"},
			{in: "connected_client_output_buffer_memory_usage_bytes"},
			{in: "connected_client_total_memory_consumed_bytes"},
			{in: "connected_client_created_at_timestamp"},
			{in: "connected_client_idle_since_timestamp"},
			{in: "connected_client_channel_subscriptions_count"},
			{in: "connected_client_pattern_matching_subscriptions_count"},
			{in: "connected_client_query_buffer_length_bytes"},
			{in: "connected_client_query_buffer_free_space_bytes"},
			{in: "connected_client_output_buffer_length_bytes"},
			{in: "connected_client_output_list_length"},
			{in: "connected_client_shard_channel_subscriptions_count"},
			{in: "connected_client_info"},
		}
		foundIdleClients := false
		for m := range chM {
			desc := m.Desc().String()
			if strings.Contains(desc, `fqName: "test_idle_clients"`) {
				foundIdleClients = true
			}
			for i := range tsts {
				if strings.Contains(desc, tsts[i].in) {
					tsts[i].found = true
				}
			}
		}
		if foundIdleClients != (opts.ExportClientIdleThreshold > 0) {
			t.Errorf("idle_clients presence = %t with threshold %d", foundIdleClients, opts.ExportClientIdleThreshold)
		}

		for _, tst := range tsts {
			if opts.ExportClientList && !tst.found {
				t.Errorf("%s was *not* found in isExportClientList metrics but expected", tst.in)
			} else if !opts.ExportClientList && tst.found {
				t.Errorf("%s was *found* in isExportClientList metrics but *not* expected", tst.in)
			}
		}
	}
}

/*
some metrics are only in redis 7 but not yet in valkey 7.2
like "connected_client_shard_channel_watched_keys"
*/
func TestExportClientListRedis7(t *testing.T) {
	redisSevenAddr := os.Getenv("TEST_REDIS7_URI")
	if redisSevenAddr == "" {
		t.Skipf("Skipping TestExportClientListRedis7, env var TEST_REDIS7_URI not set")
	}

	e := getTestExporterWithAddrAndOptions(redisSevenAddr, Options{
		Namespace:        "test",
		ExportClientList: true,
	})

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	tsts := []struct {
		in    string
		found bool
	}{
		{
			in: "connected_client_shard_channel_subscriptions_count",
		}, {
			in: "connected_client_shard_channel_watched_keys",
		},
	}
	for m := range chM {
		desc := m.Desc().String()
		for i := range tsts {
			if strings.Contains(desc, tsts[i].in) {
				tsts[i].found = true
			}
		}
	}

	for _, tst := range tsts {
		if !tst.found {
			t.Errorf(`%s was *not* found in isExportClientList metrics but expected`, tst.in)
		}
	}
}

func TestExportClientListInclPort(t *testing.T) {
	for _, inclPort := range []bool{true, false} {
		e := getTestExporterWithOptions(t, Options{
			Namespace:             "test",
			ExportClientList:      true,
			ExportClientsInclPort: inclPort,
		})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		found := false
		for m := range chM {
			desc := m.Desc().String()
			if strings.Contains(desc, "connected_client_info") {
				if strings.Contains(desc, "port") {
					found = true
				}
			}
		}

		if inclPort && !found {
			t.Errorf(`connected_client_info did *not* include "port" in isExportClientList metrics but was expected`)
		} else if !inclPort && found {
			t.Errorf(`connected_client_info did *include* "port" in isExportClientList metrics but was *not* expected`)
		}
	}
}
