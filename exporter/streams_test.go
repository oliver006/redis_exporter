package exporter

import (
	"errors"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
)

type scanStreamFixture struct {
	name       string
	stream     string
	streamInfo streamInfo
	groups     []streamGroupsInfo
	consumers  []streamGroupConsumersInfo
}

type redisReply struct {
	value any
	err   error
}

type queuedRedisConn struct {
	replies []redisReply
	calls   [][]any
}

func (c *queuedRedisConn) Close() error { return nil }
func (c *queuedRedisConn) Err() error   { return nil }
func (c *queuedRedisConn) Do(commandName string, args ...any) (any, error) {
	c.calls = append(c.calls, append([]any{commandName}, args...))
	if len(c.replies) == 0 {
		return nil, errors.New("unexpected Redis command")
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	return reply.value, reply.err
}
func (c *queuedRedisConn) Send(string, ...any) error { return nil }
func (c *queuedRedisConn) Flush() error              { return nil }
func (c *queuedRedisConn) Receive() (any, error)     { return nil, nil }

var (
	TestStreamTimestamps = []string{
		"1638006862416-0",
		"1638006862417-2",
	}
)

func isNotTestTimestamp(returned string) bool {
	for _, expected := range TestStreamTimestamps {
		if parseStreamItemId(expected) == parseStreamItemId(returned) {
			return false
		}
	}
	return true
}

func TestParseRedisIDMPStreamInfo(t *testing.T) {
	values := []any{
		nil, nil,
		[]byte("length"), int64(2),
		[]byte("last-generated-id"), []byte("1638006862417-2"),
		[]byte("idmp-duration"), int64(60),
		[]byte("idmp-maxsize"), int64(100),
		[]byte("pids-tracked"), int64(3),
		[]byte("iids-tracked"), int64(7),
		[]byte("iids-added"), int64(11),
		[]byte("iids-duplicates"), int64(2),
		[]byte("first-entry"), []any{[]byte("1638006862416-0"), []any{}},
		[]byte("last-entry"), []any{[]byte("1638006862417-2"), []any{}},
	}

	info, err := parseStreamInfo(values)
	if err != nil {
		t.Fatalf("parseStreamInfo() err: %s", err)
	}
	if !info.IDMPAvailable {
		t.Fatal("IDMP fields were not marked available")
	}
	if info.IDMPDuration != 60 || info.IDMPMaxSize != 100 || info.PIDsTracked != 3 || info.IIDsTracked != 7 || info.IIDsAdded != 11 || info.IIDsDuplicates != 2 {
		t.Errorf("unexpected IDMP stream info: %#v", info)
	}
	if info.FirstEntryId != "1638006862416-0" || info.LastEntryId != "1638006862417-2" {
		t.Errorf("unexpected stream entry IDs: first=%q last=%q", info.FirstEntryId, info.LastEntryId)
	}
}

func TestParseStreamInfoRejectsMalformedReply(t *testing.T) {
	if _, err := parseStreamInfo([]any{[]byte("length")}); err == nil {
		t.Fatal("parseStreamInfo() expected an error for an odd-length reply")
	}
}

func TestParseStreamGroupNackedCounts(t *testing.T) {
	values := []any{
		nil, nil,
		[]byte("length"), int64(3),
		[]byte("groups"), []any{
			[]any{nil, nil, []byte("name"), []byte("workers"), []byte("nacked-count"), int64(2)},
			[]any{[]byte("name"), []byte("other"), []byte("nacked-count"), int64(0)},
			[]any{[]byte("name"), []byte("pre-8.8")},
		},
	}

	got, err := parseStreamGroupNackedCounts(values)
	if err != nil {
		t.Fatalf("parseStreamGroupNackedCounts() err: %s", err)
	}
	if len(got) != 2 || got["workers"] != 2 || got["other"] != 0 {
		t.Errorf("parseStreamGroupNackedCounts() = %#v, want workers=2 and other=0", got)
	}
}

func TestParseStreamGroupNackedCountsErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []any
	}{
		{name: "invalid groups", values: []any{[]byte("groups"), int64(1)}},
		{name: "invalid group", values: []any{[]byte("groups"), []any{int64(1)}}},
		{name: "invalid name", values: []any{[]byte("groups"), []any{[]any{[]byte("name"), int64(1)}}}},
		{name: "invalid nacked count", values: []any{[]byte("groups"), []any{[]any{[]byte("name"), []byte("workers"), []byte("nacked-count"), []byte("bad")}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseStreamGroupNackedCounts(test.values); err == nil {
				t.Fatal("parseStreamGroupNackedCounts() expected an error")
			}
		})
	}
}

func TestGetStreamInfoRedisEightEight(t *testing.T) {
	conn := &queuedRedisConn{replies: []redisReply{
		{value: []any{[]byte("length"), int64(2), []byte("groups"), int64(1), []byte("idmp-duration"), int64(60)}},
		{value: []any{[]any{[]byte("name"), []byte("workers"), []byte("consumers"), int64(0)}}},
		{value: []any{}},
		{value: []any{[]byte("groups"), []any{[]any{[]byte("name"), []byte("workers"), []byte("nacked-count"), int64(2)}}}},
	}}

	info, err := getStreamInfo(conn, "events")
	if err != nil {
		t.Fatalf("getStreamInfo() err: %s", err)
	}
	if !info.IDMPAvailable || len(info.StreamGroupsInfo) != 1 || !info.StreamGroupsInfo[0].NackedCountAvailable || info.StreamGroupsInfo[0].NackedCount != 2 {
		t.Fatalf("unexpected Redis 8.8 stream info: %#v", info)
	}
	wantFullCall := []any{"XINFO", "STREAM", "events", "FULL", "COUNT", 1}
	if !reflect.DeepEqual(conn.calls[len(conn.calls)-1], wantFullCall) {
		t.Errorf("last Redis call = %#v, want %#v", conn.calls[len(conn.calls)-1], wantFullCall)
	}
}

func TestGetStreamInfoErrorsAndFallbacks(t *testing.T) {
	errRedis := errors.New("redis error")
	streamReply := []any{[]byte("length"), int64(2), []byte("groups"), int64(1)}
	groupsReply := []any{[]any{[]byte("name"), []byte("workers"), []byte("consumers"), int64(0)}}
	for _, test := range []struct {
		name      string
		replies   []redisReply
		wantError bool
	}{
		{name: "stream command", replies: []redisReply{{err: errRedis}}, wantError: true},
		{name: "malformed stream", replies: []redisReply{{value: []any{[]byte("length")}}}, wantError: true},
		{name: "groups command", replies: []redisReply{{value: streamReply}, {err: errRedis}}, wantError: true},
		{name: "no groups", replies: []redisReply{{value: []any{[]byte("length"), int64(2), []byte("groups"), int64(0)}}, {value: []any{}}}},
		{name: "full unavailable", replies: []redisReply{{value: streamReply}, {value: groupsReply}, {value: []any{}}, {err: errRedis}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			info, err := getStreamInfo(&queuedRedisConn{replies: test.replies}, "events")
			if (err != nil) != test.wantError {
				t.Fatalf("getStreamInfo() err = %v, wantError %t", err, test.wantError)
			}
			if err == nil && len(info.StreamGroupsInfo) > 0 && info.StreamGroupsInfo[0].NackedCountAvailable {
				t.Errorf("fallback unexpectedly marked XNACK state available: %#v", info.StreamGroupsInfo[0])
			}
		})
	}
}

func TestRedisIDMPAndXNACKStreamMetrics(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}
	info := &streamInfo{
		IDMPAvailable:  true,
		IDMPDuration:   60,
		IDMPMaxSize:    100,
		PIDsTracked:    3,
		IIDsTracked:    7,
		IIDsAdded:      11,
		IIDsDuplicates: 2,
		StreamGroupsInfo: []streamGroupsInfo{{
			Name: "workers", NackedCount: 2, NackedCountAvailable: true,
			StreamGroupConsumersInfo: []streamGroupConsumersInfo{{Name: "worker-1", Pending: 4, Idle: 1500}},
		}},
	}
	type expectedMetric struct {
		value   float64
		counter bool
		found   bool
	}
	want := map[string]*expectedMetric{
		"stream_idmp_duration_seconds":           {value: 60},
		"stream_idmp_max_size":                   {value: 100},
		"stream_idmp_producer_ids_tracked":       {value: 3},
		"stream_idmp_idempotent_ids_tracked":     {value: 7},
		"stream_idmp_entries_added_total":        {value: 11, counter: true},
		"stream_idmp_duplicates_total":           {value: 2, counter: true},
		"stream_group_messages_nacked":           {value: 2},
		"stream_group_consumer_messages_pending": {value: 4},
		"stream_group_consumer_idle_seconds":     {value: 1.5},
	}

	ch := make(chan prometheus.Metric)
	go func() {
		e.registerStreamMetrics(ch, "db0", "events", info)
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
			var value float64
			if expected.counter {
				if got.Counter == nil {
					t.Errorf("%s is not a counter", name)
					continue
				}
				value = got.Counter.GetValue()
			} else {
				if got.Gauge == nil {
					t.Errorf("%s is not a gauge", name)
					continue
				}
				value = got.Gauge.GetValue()
			}
			if value != expected.value {
				t.Errorf("%s value = %v, want %v", name, value, expected.value)
			}
			expected.found = true
		}
	}
	for name, expected := range want {
		if !expected.found {
			t.Errorf("metric %s was not emitted", name)
		}
	}
}

func TestStreamMetricsOmitUnavailableIDMPAndXNACKState(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}
	info := &streamInfo{StreamGroupsInfo: []streamGroupsInfo{{Name: "workers"}}}

	ch := make(chan prometheus.Metric)
	go func() {
		e.registerStreamMetrics(ch, "db0", "events", info)
		close(ch)
	}()
	for metric := range ch {
		desc := metric.Desc().String()
		if strings.Contains(desc, "stream_idmp_") || strings.Contains(desc, "stream_group_messages_nacked") {
			t.Errorf("unsupported stream state was emitted: %s", desc)
		}
	}
}

func TestStreamsGetStreamInfo(t *testing.T) {
	if os.Getenv("TEST_REDIS6_URI") == "" {
		t.Skipf("TEST_REDIS6_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS6_URI")
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	if _, err = c.Do("SELECT", dbNumStr); err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	tsts := []scanStreamFixture{
		{
			name:   "Stream test",
			stream: TestKeyNameStream,
			streamInfo: streamInfo{
				Length:         2,
				RadixTreeKeys:  1,
				RadixTreeNodes: 2,
				Groups:         2,
			},
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			info, err := getStreamInfo(c, tst.stream)
			if err != nil {
				t.Fatalf("Error getting stream info for %#v: %s", tst.stream, err)
			}

			if info.Length != tst.streamInfo.Length {
				t.Errorf("Stream length mismatch.\nActual: %#v \nExpected: %#v", info.Length, tst.streamInfo.Length)
			}
			if info.RadixTreeKeys != tst.streamInfo.RadixTreeKeys {
				t.Errorf("Stream RadixTreeKeys mismatch.\nActual: %#v \nExpected: %#v", info.RadixTreeKeys, tst.streamInfo.RadixTreeKeys)
			}
			if info.RadixTreeNodes != tst.streamInfo.RadixTreeNodes {
				t.Errorf("Stream RadixTreeNodes mismatch.\nActual: %#v \nExpected: %#v", info.RadixTreeNodes, tst.streamInfo.RadixTreeNodes)
			}
			if info.Groups != tst.streamInfo.Groups {
				t.Errorf("Stream Groups mismatch.\nActual: %#v \nExpected: %#v", info.Groups, tst.streamInfo.Groups)
			}
			if isNotTestTimestamp(info.LastGeneratedId) {
				t.Errorf("Stream LastGeneratedId mismatch.\nActual: %#v \nExpected any of: %#v", info.LastGeneratedId, TestStreamTimestamps)
			}
			if info.FirstEntryId != TestStreamTimestamps[0] {
				t.Errorf("Stream FirstEntryId mismatch.\nActual: %#v \nExpected any of: %#v", info.FirstEntryId, TestStreamTimestamps)
			}
			if info.LastEntryId != TestStreamTimestamps[len(TestStreamTimestamps)-1] {
				t.Errorf("Stream LastEntryId mismatch.\nActual: %#v \nExpected any of: %#v", info.LastEntryId, TestStreamTimestamps)
			}
			if info.MaxDeletedEntryId != "" {
				t.Errorf("Stream MaxDeletedEntryId mismatch.\nActual: %#v \nExpected: %#v", info.MaxDeletedEntryId, "0-0")
			}
		})
	}
}

func TestStreamsGetStreamInfoUsingValkey9(t *testing.T) {
	if os.Getenv("TEST_VALKEY9_URI") == "" {
		t.Skipf("TEST_VALKEY9_URI not set - skipping")
	}

	addr := strings.Replace(os.Getenv("TEST_VALKEY9_URI"), "valkey://", "redis://", 1)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	if _, err = c.Do("SELECT", dbNumStr); err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	tsts := []scanStreamFixture{
		{
			name:   "Stream test",
			stream: TestKeyNameStream,
			streamInfo: streamInfo{
				Length:         2,
				RadixTreeKeys:  1,
				RadixTreeNodes: 2,
				Groups:         2,
			},
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			info, err := getStreamInfo(c, tst.stream)
			if err != nil {
				t.Fatalf("Error getting stream info for %#v: %s", tst.stream, err)
			}

			if info.Length != tst.streamInfo.Length {
				t.Errorf("Stream length mismatch.\nActual: %#v \nExpected: %#v", info.Length, tst.streamInfo.Length)
			}
			if info.RadixTreeKeys != tst.streamInfo.RadixTreeKeys {
				t.Errorf("Stream RadixTreeKeys mismatch.\nActual: %#v \nExpected: %#v", info.RadixTreeKeys, tst.streamInfo.RadixTreeKeys)
			}
			if info.RadixTreeNodes != tst.streamInfo.RadixTreeNodes {
				t.Errorf("Stream RadixTreeNodes mismatch.\nActual: %#v \nExpected: %#v", info.RadixTreeNodes, tst.streamInfo.RadixTreeNodes)
			}
			if info.Groups != tst.streamInfo.Groups {
				t.Errorf("Stream Groups mismatch.\nActual: %#v \nExpected: %#v", info.Groups, tst.streamInfo.Groups)
			}
			if isNotTestTimestamp(info.LastGeneratedId) {
				t.Errorf("Stream LastGeneratedId mismatch.\nActual: %#v \nExpected any of: %#v", info.LastGeneratedId, TestStreamTimestamps)
			}
			if info.FirstEntryId != TestStreamTimestamps[0] {
				t.Errorf("Stream FirstEntryId mismatch.\nActual: %#v \nExpected any of: %#v", info.FirstEntryId, TestStreamTimestamps)
			}
			if info.LastEntryId != TestStreamTimestamps[len(TestStreamTimestamps)-1] {
				t.Errorf("Stream LastEntryId mismatch.\nActual: %#v \nExpected any of: %#v", info.LastEntryId, TestStreamTimestamps)
			}
			if info.MaxDeletedEntryId != "0-0" {
				t.Errorf("Stream MaxDeletedEntryId mismatch.\nActual: %#v \nExpected: %#v", info.MaxDeletedEntryId, "0-0")
			}
		})
	}
}

func TestStreamsScanStreamGroups123(t *testing.T) {
	if os.Getenv("TEST_REDIS6_URI") == "" {
		t.Skipf("TEST_REDIS6_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS6_URI")

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	if _, err = c.Do("SELECT", dbNumStr); err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	fixtures := []keyFixture{
		{"XADD", "test_stream_1", []any{"1638006862521-0", "field_1", "str_1"}},
		{"XADD", "test_stream_2", []any{"1638006862522-0", "field_pattern_1", "str_pattern_1"}},
	}
	// Create test streams
	c.Do("XGROUP", "CREATE", "test_stream_1", "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", "test_stream_2", "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", "test_stream_2", "test_group_2", "$")

	// Add simple values
	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()
	createKeyFixtures(t, c, fixtures)

	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "test_stream_1", ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "test_stream_2", ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", "test_stream_2", "0")

	tsts := []scanStreamFixture{
		{
			name:   "Single group test",
			stream: "test_stream_1",
			groups: []streamGroupsInfo{
				{
					Name:            "test_group_1",
					Consumers:       1,
					Pending:         1,
					EntriesRead:     0,
					Lag:             0,
					LastDeliveredId: "1638006862521-0",
					StreamGroupConsumersInfo: []streamGroupConsumersInfo{
						{
							Name:    "test_consumer_1",
							Pending: 1,
						},
					},
				},
			}},
		{
			name:   "Multiple groups test",
			stream: "test_stream_2",
			groups: []streamGroupsInfo{
				{
					Name:            "test_group_1",
					Consumers:       2,
					Pending:         1,
					Lag:             0,
					EntriesRead:     0,
					LastDeliveredId: "1638006862522-0",
				},
				{
					Name:      "test_group_2",
					Consumers: 0,
					Pending:   0,
					Lag:       0,
				},
			}},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			scannedGroup, err := scanStreamGroups(c, tst.stream)
			t.Logf("scanStreamGroups() err: %s", err)
			if err != nil {
				t.Fatalf("Err: %s", err)
				return
			}

			if len(scannedGroup) == len(tst.groups) {
				for i := range scannedGroup {
					if scannedGroup[i].Name != tst.groups[i].Name {
						t.Errorf("%d) Group name mismatch.\nExpected: %#v \nActual: %#v", i, tst.groups[i].Name, scannedGroup[i].Name)
					}
					if scannedGroup[i].Consumers != tst.groups[i].Consumers {
						t.Errorf("%d) Consumers count mismatch.\nExpected: %#v \nActual: %#v", i, tst.groups[i].Consumers, scannedGroup[i].Consumers)
					}
					if scannedGroup[i].Pending != tst.groups[i].Pending {
						t.Errorf("%d) Pending items mismatch.\nExpected: %#v \nActual: %#v", i, tst.groups[i].Pending, scannedGroup[i].Pending)
					}
					if parseStreamItemId(scannedGroup[i].LastDeliveredId) != parseStreamItemId(tst.groups[i].LastDeliveredId) {
						t.Errorf("%d) LastDeliveredId items mismatch.\nExpected: %#v \nActual: %#v", i, tst.groups[i].LastDeliveredId, scannedGroup[i].LastDeliveredId)
					}
					if scannedGroup[i].Lag != tst.groups[i].Lag {
						t.Errorf("%d) Lag mismatch.\nExpected: %#v \nActual: %#v", i, tst.groups[i].Lag, scannedGroup[i].Lag)
					}
					if scannedGroup[i].EntriesRead != tst.groups[i].EntriesRead {
						t.Errorf("%d) EntriesRead mismatch.\nExpected: %#v \nActual: %#v", i, tst.groups[i].EntriesRead, scannedGroup[i].EntriesRead)
					}
				}
			} else {
				t.Errorf("Consumers entries mismatch.\nExpected: %d\nActual: %d", len(tst.consumers), len(scannedGroup))
			}
		})
	}
}

func TestStreamsScanStreamGroupsUsingValkey9(t *testing.T) {
	if os.Getenv("TEST_VALKEY9_URI") == "" {
		t.Skipf("TEST_VALKEY9_URI not set - skipping")
	}
	addr := strings.Replace(os.Getenv("TEST_VALKEY9_URI"), "valkey://", "redis://", 1)
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	if _, err = c.Do("SELECT", db); err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"XADD", "test_stream_1", []any{"1638006862521-0", "field_1", "str_1"}},
		{"XADD", "test_stream_2", []any{"1638006862522-0", "field_pattern_1", "str_pattern_1"}},
	}

	// Create test streams
	c.Do("XGROUP", "CREATE", "test_stream_1", "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", "test_stream_2", "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", "test_stream_2", "test_group_2", "$")

	// Add simple values
	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()
	createKeyFixtures(t, c, fixtures)

	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "test_stream_1", ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "test_stream_2", ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", "test_stream_2", "0")

	tsts := []scanStreamFixture{
		{
			name:   "Single group test",
			stream: "test_stream_1",
			groups: []streamGroupsInfo{
				{
					Name:            "test_group_1",
					Consumers:       1,
					Pending:         1,
					EntriesRead:     1,
					Lag:             0,
					LastDeliveredId: "1638006862521-0",
					StreamGroupConsumersInfo: []streamGroupConsumersInfo{
						{
							Name:    "test_consumer_1",
							Pending: 1,
						},
					},
				},
			}},
		{
			name:   "Multiple groups test",
			stream: "test_stream_2",
			groups: []streamGroupsInfo{
				{
					Name:            "test_group_1",
					Consumers:       2,
					Pending:         1,
					Lag:             0,
					EntriesRead:     1,
					LastDeliveredId: "1638006862522-0",
				},
				{
					Name:      "test_group_2",
					Consumers: 0,
					Pending:   0,
					Lag:       1,
				},
			}},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			scannedGroup, err := scanStreamGroups(c, tst.stream)
			t.Logf("scanStreamGroups() err: %s", err)
			if err != nil {
				t.Errorf("Err: %s", err)
			}

			if len(scannedGroup) == len(tst.groups) {
				for i := range scannedGroup {
					if scannedGroup[i].Name != tst.groups[i].Name {
						t.Errorf("Group name mismatch.\nExpected: %#v \nActual: %#v", tst.groups[i].Name, scannedGroup[i].Name)
					}
					if scannedGroup[i].Consumers != tst.groups[i].Consumers {
						t.Errorf("Consumers count mismatch.\nExpected: %#v \nActual: %#v", tst.groups[i].Consumers, scannedGroup[i].Consumers)
					}
					if scannedGroup[i].Pending != tst.groups[i].Pending {
						t.Errorf("Pending items mismatch.\nExpected: %#v \nActual: %#v", tst.groups[i].Pending, scannedGroup[i].Pending)
					}
					if parseStreamItemId(scannedGroup[i].LastDeliveredId) != parseStreamItemId(tst.groups[i].LastDeliveredId) {
						t.Errorf("LastDeliveredId items mismatch.\nExpected: %#v \nActual: %#v", tst.groups[i].LastDeliveredId, scannedGroup[i].LastDeliveredId)
					}
					if scannedGroup[i].Lag != tst.groups[i].Lag {
						t.Errorf("Lag mismatch.\nExpected: %#v \nActual: %#v", tst.groups[i].Lag, scannedGroup[i].Lag)
					}
					if scannedGroup[i].EntriesRead != tst.groups[i].EntriesRead {
						t.Errorf("EntriesRead mismatch.\nExpected: %#v \nActual: %#v", tst.groups[i].EntriesRead, scannedGroup[i].EntriesRead)
					}
				}
			} else {
				t.Errorf("Consumers entries mismatch.\nExpected: %d\nActual: %d", len(tst.consumers), len(scannedGroup))
			}
		})
	}
}

func TestStreamsScanStreamGroupsConsumers(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	if _, err = c.Do("SELECT", db); err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"XADD", "single_consumer_stream", []any{"*", "field_1", "str_1"}},
		{"XADD", "multiple_consumer_stream", []any{"*", "field_pattern_1", "str_pattern_1"}},
	}

	// Create test streams
	c.Do("XGROUP", "CREATE", "single_consumer_stream", "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", "multiple_consumer_stream", "test_group_1", "$", "MKSTREAM")

	// Add simple test items to streams
	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()
	createKeyFixtures(t, c, fixtures)

	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "single_consumer_stream", ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", "multiple_consumer_stream", ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", "multiple_consumer_stream", "0")

	tsts := []scanStreamFixture{
		{
			name:   "Single group test",
			stream: "single_consumer_stream",
			groups: []streamGroupsInfo{{Name: "test_group_1"}},
			consumers: []streamGroupConsumersInfo{
				{
					Name:    "test_consumer_1",
					Pending: 1,
				},
			},
		},
		{
			name:   "Multiple consumers test",
			stream: "multiple_consumer_stream",
			groups: []streamGroupsInfo{{Name: "test_group_1"}},
			consumers: []streamGroupConsumersInfo{
				{
					Name:    "test_consumer_1",
					Pending: 1,
				},
				{
					Name:    "test_consumer_2",
					Pending: 0,
				},
			},
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {

			// For each group
			for _, g := range tst.groups {
				g.StreamGroupConsumersInfo, err = scanStreamGroupConsumers(c, tst.stream, g.Name)
				if err != nil {
					t.Errorf("Err: %s", err)
				}
				if len(g.StreamGroupConsumersInfo) == len(tst.consumers) {
					for i := range g.StreamGroupConsumersInfo {
						if g.StreamGroupConsumersInfo[i].Name != tst.consumers[i].Name {
							t.Errorf("Consumer name mismatch.\nExpected: %#v \nActual: %#v", tst.consumers[i].Name, g.StreamGroupConsumersInfo[i].Name)
						}
						if g.StreamGroupConsumersInfo[i].Pending != tst.consumers[i].Pending {
							t.Errorf("Pending items mismatch for %s.\nExpected: %#v \nActual: %#v", g.StreamGroupConsumersInfo[i].Name, tst.consumers[i].Pending, g.StreamGroupConsumersInfo[i].Pending)
						}

					}
				} else {
					t.Errorf("Consumers entries mismatch.\nExpected: %d\nActual: %d", len(tst.consumers), len(g.StreamGroupConsumersInfo))
				}
			}

		})
	}
}

func TestStreamsExtractStreamMetrics(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test", CheckSingleStreams: dbNumStrFull + "=" + TestKeyNameStream},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	chM := make(chan prometheus.Metric)
	go func() {
		e.extractStreamMetrics(chM, c)
		close(chM)
	}()
	want := map[string]bool{
		"stream_length":                          false,
		"stream_radix_tree_keys":                 false,
		"stream_radix_tree_nodes":                false,
		"stream_last_generated_id":               false,
		"stream_groups":                          false,
		"stream_max_deleted_entry_id":            false,
		"stream_first_entry_id":                  false,
		"stream_last_entry_id":                   false,
		"stream_entries_added_total":             false,
		"stream_group_consumers":                 false,
		"stream_group_messages_pending":          false,
		"stream_group_last_delivered_id":         false,
		"stream_group_entries_read":              false,
		"stream_group_lag":                       false,
		"stream_group_consumer_messages_pending": false,
		"stream_group_consumer_idle_seconds":     false,
	}

	for m := range chM {
		for k := range want {
			log.Debugf("metric: %s", m.Desc().String())
			log.Debugf("want: %s", k)

			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}

	}
}

func TestStreamsExtractStreamMetricsExcludeConsumer(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test", CheckSingleStreams: dbNumStrFull + "=" + TestKeyNameStream, StreamsExcludeConsumerMetrics: true},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	chM := make(chan prometheus.Metric)
	go func() {
		e.extractStreamMetrics(chM, c)
		close(chM)
	}()
	want := map[string]bool{
		"stream_length":                  false,
		"stream_radix_tree_keys":         false,
		"stream_radix_tree_nodes":        false,
		"stream_last_generated_id":       false,
		"stream_groups":                  false,
		"stream_max_deleted_entry_id":    false,
		"stream_first_entry_id":          false,
		"stream_last_entry_id":           false,
		"stream_entries_added_total":     false,
		"stream_group_consumers":         false,
		"stream_group_messages_pending":  false,
		"stream_group_last_delivered_id": false,
		"stream_group_entries_read":      false,
		"stream_group_lag":               false,
	}

	dontWant := map[string]bool{
		"stream_group_consumer_messages_pending": false,
		"stream_group_consumer_idle_seconds":     false,
	}

	for m := range chM {
		for k := range want {
			log.Debugf("metric: %s", m.Desc().String())
			log.Debugf("want: %s", k)

			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
		for k := range dontWant {
			log.Debugf("metric: %s", m.Desc().String())
			log.Debugf("don't want: %s", k)

			if strings.Contains(m.Desc().String(), k) {
				dontWant[k] = true
			}
		}
	}

	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s metric, which should be collected", k)
		}
	}
	for k, found := range dontWant {
		if found {
			t.Errorf("found %s metric, which shouldn't be collected", k)
		}
	}
}

func TestClusterStreamMetricsExtraction(t *testing.T) {
	if os.Getenv("TEST_VALKEY_CLUSTER_MASTER_URI") == "" {
		t.Skipf("TEST_VALKEY_CLUSTER_MASTER_URI not set - skipping cluster stream test")
	}

	clusterURI := os.Getenv("TEST_VALKEY_CLUSTER_MASTER_URI")

	// Test streams to create
	testStreams := []string{"audit_stream", "sa_audit_stream", "test_stream_cluster"}

	// Setup cluster connection to create test streams
	// Use cluster-aware connection to avoid MOVED errors
	tempExporter, err := NewRedisExporter(clusterURI, Options{IsCluster: true})
	if err != nil {
		t.Fatalf("Couldn't create temp exporter for cluster setup: %v", err)
	}

	c, err := tempExporter.connectToRedisCluster()
	if err != nil {
		t.Fatalf("Couldn't connect to cluster: %v", err)
	}
	defer c.Close()

	// Create test streams with some data using cluster connection
	for _, streamName := range testStreams {
		// Add entries to streams - cluster connection handles MOVED redirects automatically
		_, err = c.Do("XADD", streamName, "*", "field1", "value1", "field2", "value2")
		if err != nil {
			t.Logf("Warning: couldn't create stream %s: %v", streamName, err)
			continue
		}
		_, err = c.Do("XADD", streamName, "*", "field3", "value3")
		if err != nil {
			t.Logf("Warning: couldn't add to stream %s: %v", streamName, err)
		}
	}

	// Cleanup function - use the same cluster connection to avoid MOVED errors
	defer func() {
		for _, streamName := range testStreams {
			_, err := c.Do("DEL", streamName)
			if err != nil {
				t.Logf("Warning: couldn't clean up stream %s: %v", streamName, err)
			}
		}
	}()

	// Create exporter with cluster mode and single streams config
	// This reproduces the exact command from the GitHub issue:
	// redis_exporter --check-single-streams=audit,sa_audit --is-cluster=true
	streamConfig := "db0=audit_stream,db0=sa_audit_stream,db0=test_stream_cluster"

	e, err := NewRedisExporter(
		clusterURI,
		Options{
			Namespace:          "test",
			CheckSingleStreams: streamConfig,
			IsCluster:          true,
		},
	)
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	// Test the full HTTP endpoint (this tests the complete path including cluster connection fix)
	ts := httptest.NewServer(e)
	defer ts.Close()

	metricsOutput := downloadURL(t, ts.URL+"/metrics")

	// Check if we got HTML instead of metrics (indicates an error during metrics collection)
	if strings.Contains(metricsOutput, "<html>") {
		t.Logf("Got HTML response instead of metrics, this indicates an error during metrics collection")
		t.Logf("This could be due to Redis connection issues or cluster MOVED errors")
		t.Logf("First 500 chars of response: %.500s...", metricsOutput)
		t.Fatal("Expected Prometheus metrics but got HTML error page - check Redis cluster connectivity")
	}

	// Parse the metrics output to find stream metrics
	foundMetrics := make(map[string]bool)
	lines := strings.SplitSeq(metricsOutput, "\n")

	for line := range lines {
		// Look for stream_length metrics with our test streams
		if strings.Contains(line, "stream_length") {
			for _, streamName := range testStreams {
				if strings.Contains(line, `stream="`+streamName+`"`) {
					foundMetrics[streamName] = true
					t.Logf("Found stream metric for: %s", streamName)
				}
			}
		}
	}

	// Verify that we found metrics for our test streams
	// This ensures that the cluster MOVED errors are properly handled
	expectedStreams := 0
	for _, streamName := range testStreams {
		if foundMetrics[streamName] {
			expectedStreams++
			t.Logf("✓ Successfully found metrics for stream: %s", streamName)
		} else {
			t.Logf("⚠ Did not find metrics for stream: %s", streamName)
		}
	}

	if expectedStreams == 0 {
		t.Error("Expected to find metrics for at least one test stream in cluster mode")
		t.Error("This indicates that the cluster MOVED error fix may not be working properly")

		// Additional debugging info
		t.Logf("Test streams created: %v", testStreams)
		t.Logf("Found stream metrics for: %v", foundMetrics)

		// Show sample of metrics output for debugging
		sampleLines := strings.Split(metricsOutput, "\n")
		t.Log("Sample metrics output (first 10 lines):")
		for i, line := range sampleLines {
			if i >= 10 || line == "" {
				break
			}
			t.Logf("  %s", line)
		}
	} else {
		t.Logf("✓ SUCCESS: Found stream metrics for %d/%d streams in cluster mode", expectedStreams, len(testStreams))
		t.Logf("This confirms that Redis cluster MOVED errors are properly handled for streams")
		t.Logf("HTTP endpoint successfully returned metrics without cluster MOVED errors")
	}
}
