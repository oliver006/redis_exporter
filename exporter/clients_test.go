package exporter

import (
	"os"
	"strings"
	"testing"
	"time"

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
			expectedInfo: ClientInfo{Id: "11", CreatedAt: convertDurationToTimestampInt64("6321"), IdleSince: convertDurationToTimestampInt64("6320"), IdleSeconds: 6320, Flags: "N", Db: "0", Ssub: -1, Watch: -1, Obl: 3, Oll: 8, OMem: 0, TotMem: 0, Host: "127.0.0.1", Port: "63508", Cmd: "setex"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=client",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", Name: "foo", CreatedAt: convertDurationToTimestampInt64("5"), IdleSince: convertDurationToTimestampInt64("0"), IdleSeconds: 0, Flags: "N", Db: "1", Ssub: -1, Watch: -1, Qbuf: 26, QbufFree: 32742, OMem: 0, TotMem: 0, Host: "127.0.0.1", Port: "64958", Cmd: "client"},
		}, {
			in:           "id=14 addr=127.0.0.1:64959 fd=9 name= age=5 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=client user=default resp=3",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "14", CreatedAt: convertDurationToTimestampInt64("5"), IdleSince: convertDurationToTimestampInt64("0"), IdleSeconds: 0, Flags: "N", Db: "0", Ssub: -1, Watch: -1, Qbuf: 26, QbufFree: 32742, OMem: 0, TotMem: 0, Host: "127.0.0.1", Port: "64959", User: "default", Resp: "3", Cmd: "client"},
		}, {
			in:           "id=40253233 addr=fd40:1481:21:dbe0:7021:300:a03:1a06:44426 fd=19 name= age=782 idle=0 flags=N db=0 sub=896 psub=18 ssub=17 watch=3 multi=-1 qbuf=26 qbuf-free=32742 argv-mem=10 obl=0 oll=555 omem=0 tot-mem=61466 ow=0 owmem=0 events=r cmd=client user=default lib-name=redis-py lib-ver=5.0.1 numops=9",
			expectedOk:   true,
			expectedInfo: ClientInfo{Id: "40253233", CreatedAt: convertDurationToTimestampInt64("782"), IdleSince: convertDurationToTimestampInt64("0"), IdleSeconds: 0, Flags: "N", Db: "0", Sub: 896, Psub: 18, Ssub: 17, Watch: 3, Qbuf: 26, QbufFree: 32742, Oll: 555, OMem: 0, TotMem: 61466, Host: "fd40:1481:21:dbe0:7021:300:a03:1a06", Port: "44426", User: "default", Cmd: "client"},
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

const testClientListForIdleCount = `id=1 addr=127.0.0.1:1111 fd=8 name= age=100 idle=400 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=ping
id=2 addr=127.0.0.1:2222 fd=9 name= age=100 idle=100 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=ping
id=3 addr=127.0.0.1:3333 fd=10 name= age=100 idle=299 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=ping
`

func TestIdleClientCount(t *testing.T) {
	for _, tst := range []struct {
		name              string
		threshold         int64
		exportClientList  bool
		wantIdleCount     float64
		wantIdleMetric    bool
		wantPerClientInfo bool
	}{
		{
			name:           "disabled when threshold is zero",
			threshold:      0,
			wantIdleMetric: false,
		},
		{
			name:           "counts clients at or above threshold",
			threshold:      300,
			wantIdleCount:  1,
			wantIdleMetric: true,
		},
		{
			name:           "counts multiple clients at or above threshold",
			threshold:      100,
			wantIdleCount:  3,
			wantIdleMetric: true,
		},
		{
			name:           "exact threshold match is included",
			threshold:      299,
			wantIdleCount:  2,
			wantIdleMetric: true,
		},
		{
			name:              "does not emit per-client metrics without export-client-list",
			threshold:         300,
			exportClientList:  false,
			wantIdleCount:     1,
			wantIdleMetric:    true,
			wantPerClientInfo: false,
		},
		{
			name:              "emits both idle aggregate and per-client metrics when both enabled",
			threshold:         300,
			exportClientList:  true,
			wantIdleCount:     1,
			wantIdleMetric:    true,
			wantPerClientInfo: true,
		},
	} {
		t.Run(tst.name, func(t *testing.T) {
			e, err := NewRedisExporter("", Options{
				Namespace:                  "test",
				ClientIdleThresholdSeconds: tst.threshold,
				ExportClientList:           tst.exportClientList,
			})
			if err != nil {
				t.Fatalf("NewRedisExporter() err: %s", err)
			}

			chM := make(chan prometheus.Metric)
			go func() {
				e.parseConnectedClientMetrics(testClientListForIdleCount, chM)
				close(chM)
			}()

			var idleCount float64
			foundIdleMetric := false
			foundPerClientInfo := false

			for m := range chM {
				desc := m.Desc().String()
				if strings.Contains(desc, "connected_client_info") {
					foundPerClientInfo = true
				}
				if !strings.Contains(desc, "idle_clients") {
					continue
				}
				foundIdleMetric = true

				var dtoM dto.Metric
				if err := m.Write(&dtoM); err != nil {
					t.Fatalf("failed to write metric: %s", err)
				}
				idleCount = dtoM.GetGauge().GetValue()
			}

			if foundIdleMetric != tst.wantIdleMetric {
				t.Fatalf("idle_clients presence = %v, want %v", foundIdleMetric, tst.wantIdleMetric)
			}
			if tst.wantIdleMetric && idleCount != tst.wantIdleCount {
				t.Fatalf("idle_clients = %v, want %v", idleCount, tst.wantIdleCount)
			}
			if foundPerClientInfo != tst.wantPerClientInfo {
				t.Fatalf("connected_client_info presence = %v, want %v", foundPerClientInfo, tst.wantPerClientInfo)
			}
		})
	}
}

func TestClientCountsTowardIdleMetric(t *testing.T) {
	for _, tst := range []struct {
		flags string
		want  bool
	}{
		{flags: "N", want: true},
		{flags: "", want: true},
		{flags: "r", want: true},
		{flags: "x", want: true},
		{flags: "d", want: true},
		{flags: "t", want: true},
		{flags: "T", want: true},
		{flags: "B", want: true},
		{flags: "R", want: true},
		{flags: "P", want: false},
		{flags: "N=P", want: false},
		{flags: "b", want: false},
		{flags: "N=b", want: false},
		{flags: "M", want: false},
		{flags: "S", want: false},
		{flags: "O", want: false},
		{flags: "g", want: false},
		{flags: "o", want: false},
	} {
		if got := clientCountsTowardIdleMetric(tst.flags); got != tst.want {
			t.Errorf("clientCountsTowardIdleMetric(%q) = %v, want %v", tst.flags, got, tst.want)
		}
	}
}

func TestIdleClientCountExcludesTimeoutExemptClients(t *testing.T) {
	const clientList = `id=1 addr=127.0.0.1:1111 fd=8 name= age=100 idle=400 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=ping
id=2 addr=127.0.0.1:2222 fd=9 name= age=100 idle=400 flags=P db=0 sub=1 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=subscribe
id=3 addr=127.0.0.1:3333 fd=10 name= age=100 idle=400 flags=b db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=blpop
id=4 addr=127.0.0.1:4444 fd=11 name= age=100 idle=400 flags=S db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=replconf
id=5 addr=127.0.0.1:5555 fd=12 name= age=100 idle=400 flags=O db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=monitor
id=6 addr=127.0.0.1:6666 fd=13 name= age=100 idle=400 flags=t db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=get
id=7 addr=127.0.0.1:7777 fd=14 name= age=100 idle=400 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=sentinel|ping
id=8 addr=127.0.0.1:8888 fd=15 name= age=100 idle=400 flags=x db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=exec
`

	e, err := NewRedisExporter("", Options{
		Namespace:                  "test",
		ClientIdleThresholdSeconds: 300,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	chM := make(chan prometheus.Metric)
	go func() {
		e.parseConnectedClientMetrics(clientList, chM)
		close(chM)
	}()

	var idleCount float64
	for m := range chM {
		if !strings.Contains(m.Desc().String(), "idle_clients") {
			continue
		}
		var dtoM dto.Metric
		if err := m.Write(&dtoM); err != nil {
			t.Fatalf("failed to write metric: %s", err)
		}
		idleCount = dtoM.GetGauge().GetValue()
	}

	if idleCount != 4 {
		t.Fatalf("idle_clients = %v, want 4 (timeout-eligible idle clients)", idleCount)
	}
}

func TestNewRedisExporterRejectsNegativeIdleThreshold(t *testing.T) {
	_, err := NewRedisExporter("redis://localhost:6379", Options{
		ClientIdleThresholdSeconds: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative client-idle-threshold-seconds")
	}
}

func TestExportClientList(t *testing.T) {
	for _, isExportClientList := range []bool{true, false} {
		e := getTestExporterWithOptions(t, Options{
			Namespace:        "test",
			ExportClientList: isExportClientList,
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
		for m := range chM {
			desc := m.Desc().String()
			for i := range tsts {
				if strings.Contains(desc, tsts[i].in) {
					tsts[i].found = true
				}
			}
		}

		for _, tst := range tsts {
			if isExportClientList && !tst.found {
				t.Errorf("%s was *not* found in isExportClientList metrics but expected", tst.in)
			} else if !isExportClientList && tst.found {
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
