package exporter

import (
	"os"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type scanStreamFixture struct {
	name       string
	stream     string
	streamInfo streamInfo
	groups     []streamGroupsInfo
	consumers  []streamGroupConsumersInfo
}

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

func TestStreamsGetStreamInfo(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_URI")
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	setupDBKeys(t, addr)
	defer deleteKeysFromDB(t, addr)

	if _, err = c.Do("SELECT", dbNumStr); err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	tsts := []scanStreamFixture{
		{
			name:   "Stream test",
			stream: TestStreamName,
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
				t.Errorf("Stream length mismatch.\nActual: %#v;\nExpected: %#v", info.Length, tst.streamInfo.Length)
			}
			if info.RadixTreeKeys != tst.streamInfo.RadixTreeKeys {
				t.Errorf("Stream RadixTreeKeys mismatch.\nActual: %#v;\nExpected: %#v", info.RadixTreeKeys, tst.streamInfo.RadixTreeKeys)
			}
			if info.RadixTreeNodes != tst.streamInfo.RadixTreeNodes {
				t.Errorf("Stream RadixTreeNodes mismatch.\nActual: %#v;\nExpected: %#v", info.RadixTreeNodes, tst.streamInfo.RadixTreeNodes)
			}
			if info.Groups != tst.streamInfo.Groups {
				t.Errorf("Stream Groups mismatch.\nActual: %#v;\nExpected: %#v", info.Groups, tst.streamInfo.Groups)
			}
			if isNotTestTimestamp(info.LastGeneratedId) {
				t.Errorf("Stream LastGeneratedId mismatch.\nActual: %#v;\nExpected any of: %#v", info.LastGeneratedId, TestStreamTimestamps)
			}
			if info.FirstEntryId != "" {
				t.Errorf("Stream FirstEntryId mismatch.\nActual: %#v; - Expected empty", info.FirstEntryId)
			}
			if info.LastEntryId != "" {
				t.Errorf("Stream LastEntryId mismatch.\nActual: %#v;\nExpected empty", info.LastEntryId)
			}
			if info.MaxDeletedEntryId != "" {
				t.Errorf("Stream MaxDeletedEntryId mismatch.\nActual: %#v;\nExpected: %#v", info.MaxDeletedEntryId, "0-0")
			}
		})
	}
}

func TestStreamsGetStreamInfoUsingRedis7(t *testing.T) {
	if os.Getenv("TEST_REDIS7_URI") == "" {
		t.Skipf("TEST_REDIS7_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS7_URI")
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	setupDBKeys(t, addr)
	defer deleteKeysFromDB(t, addr)

	if _, err = c.Do("SELECT", dbNumStr); err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	tsts := []scanStreamFixture{
		{
			name:   "Stream test",
			stream: TestStreamName,
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
				t.Errorf("Stream length mismatch.\nActual: %#v;\nExpected: %#v", info.Length, tst.streamInfo.Length)
			}
			if info.RadixTreeKeys != tst.streamInfo.RadixTreeKeys {
				t.Errorf("Stream RadixTreeKeys mismatch.\nActual: %#v;\nExpected: %#v", info.RadixTreeKeys, tst.streamInfo.RadixTreeKeys)
			}
			if info.RadixTreeNodes != tst.streamInfo.RadixTreeNodes {
				t.Errorf("Stream RadixTreeNodes mismatch.\nActual: %#v;\nExpected: %#v", info.RadixTreeNodes, tst.streamInfo.RadixTreeNodes)
			}
			if info.Groups != tst.streamInfo.Groups {
				t.Errorf("Stream Groups mismatch.\nActual: %#v;\nExpected: %#v", info.Groups, tst.streamInfo.Groups)
			}
			if isNotTestTimestamp(info.LastGeneratedId) {
				t.Errorf("Stream LastGeneratedId mismatch.\nActual: %#v;\nExpected any of: %#v", info.LastGeneratedId, TestStreamTimestamps)
			}
			if info.FirstEntryId != TestStreamTimestamps[0] {
				t.Errorf("Stream FirstEntryId mismatch.\nActual: %#v;\nExpected any of: %#v", info.FirstEntryId, TestStreamTimestamps)
			}
			if info.LastEntryId != TestStreamTimestamps[len(TestStreamTimestamps)-1] {
				t.Errorf("Stream LastEntryId mismatch.\nActual: %#v;\nExpected any of: %#v", info.LastEntryId, TestStreamTimestamps)
			}
			if info.MaxDeletedEntryId != "0-0" {
				t.Errorf("Stream MaxDeletedEntryId mismatch.\nActual: %#v;\nExpected: %#v", info.MaxDeletedEntryId, "0-0")
			}
		})
	}
}

func TestStreamsScanStreamGroups123(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	if _, err = c.Do("SELECT", dbNumStr); err != nil {
		t.Errorf("Couldn't select database %#v", dbNumStr)
	}

	fixtures := []keyFixture{
		{"XADD", "test_stream_1", []interface{}{"1638006862521-0", "field_1", "str_1"}},
		{"XADD", "test_stream_2", []interface{}{"1638006862522-0", "field_pattern_1", "str_pattern_1"}},
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
			scannedGroup, _ := scanStreamGroups(c, tst.stream)
			if err != nil {
				t.Fatalf("Err: %s", err)
				return
			}

			if len(scannedGroup) == len(tst.groups) {
				for i := range scannedGroup {
					if scannedGroup[i].Name != tst.groups[i].Name {
						t.Errorf("%d) Group name mismatch.\nExpected: %#v;\nActual: %#v", i, tst.groups[i].Name, scannedGroup[i].Name)
					}
					if scannedGroup[i].Consumers != tst.groups[i].Consumers {
						t.Errorf("%d) Consumers count mismatch.\nExpected: %#v;\nActual: %#v", i, tst.groups[i].Consumers, scannedGroup[i].Consumers)
					}
					if scannedGroup[i].Pending != tst.groups[i].Pending {
						t.Errorf("%d) Pending items mismatch.\nExpected: %#v;\nActual: %#v", i, tst.groups[i].Pending, scannedGroup[i].Pending)
					}
					if parseStreamItemId(scannedGroup[i].LastDeliveredId) != parseStreamItemId(tst.groups[i].LastDeliveredId) {
						t.Errorf("%d) LastDeliveredId items mismatch.\nExpected: %#v;\nActual: %#v", i, tst.groups[i].LastDeliveredId, scannedGroup[i].LastDeliveredId)
					}
					if scannedGroup[i].Lag != tst.groups[i].Lag {
						t.Errorf("%d) Lag mismatch.\nExpected: %#v;\nActual: %#v", i, tst.groups[i].Lag, scannedGroup[i].Lag)
					}
					if scannedGroup[i].EntriesRead != tst.groups[i].EntriesRead {
						t.Errorf("%d) EntriesRead mismatch.\nExpected: %#v;\nActual: %#v", i, tst.groups[i].EntriesRead, scannedGroup[i].EntriesRead)
					}
				}
			} else {
				t.Errorf("Consumers entries mismatch.\nExpected: %d;\nActual: %d", len(tst.consumers), len(scannedGroup))
			}
		})
	}
}

func TestStreamsScanStreamGroupsUsingRedis7(t *testing.T) {
	if os.Getenv("TEST_REDIS7_URI") == "" {
		t.Skipf("TEST_REDIS7_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS7_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	if _, err = c.Do("SELECT", db); err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"XADD", "test_stream_1", []interface{}{"1638006862521-0", "field_1", "str_1"}},
		{"XADD", "test_stream_2", []interface{}{"1638006862522-0", "field_pattern_1", "str_pattern_1"}},
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
			scannedGroup, _ := scanStreamGroups(c, tst.stream)
			if err != nil {
				t.Errorf("Err: %s", err)
			}

			if len(scannedGroup) == len(tst.groups) {
				for i := range scannedGroup {
					if scannedGroup[i].Name != tst.groups[i].Name {
						t.Errorf("Group name mismatch.\nExpected: %#v;\nActual: %#v", tst.groups[i].Name, scannedGroup[i].Name)
					}
					if scannedGroup[i].Consumers != tst.groups[i].Consumers {
						t.Errorf("Consumers count mismatch.\nExpected: %#v;\nActual: %#v", tst.groups[i].Consumers, scannedGroup[i].Consumers)
					}
					if scannedGroup[i].Pending != tst.groups[i].Pending {
						t.Errorf("Pending items mismatch.\nExpected: %#v;\nActual: %#v", tst.groups[i].Pending, scannedGroup[i].Pending)
					}
					if parseStreamItemId(scannedGroup[i].LastDeliveredId) != parseStreamItemId(tst.groups[i].LastDeliveredId) {
						t.Errorf("LastDeliveredId items mismatch.\nExpected: %#v;\nActual: %#v", tst.groups[i].LastDeliveredId, scannedGroup[i].LastDeliveredId)
					}
					if scannedGroup[i].Lag != tst.groups[i].Lag {
						t.Errorf("Lag mismatch.\nExpected: %#v;\nActual: %#v", tst.groups[i].Lag, scannedGroup[i].Lag)
					}
					if scannedGroup[i].EntriesRead != tst.groups[i].EntriesRead {
						t.Errorf("EntriesRead mismatch.\nExpected: %#v;\nActual: %#v", tst.groups[i].EntriesRead, scannedGroup[i].EntriesRead)
					}
				}
			} else {
				t.Errorf("Consumers entries mismatch.\nExpected: %d;\nActual: %d", len(tst.consumers), len(scannedGroup))
			}
		})
	}
}

func TestStreamsScanStreamGroupsConsumers(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS7_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	if _, err = c.Do("SELECT", db); err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"XADD", "single_consumer_stream", []interface{}{"*", "field_1", "str_1"}},
		{"XADD", "multiple_consumer_stream", []interface{}{"*", "field_pattern_1", "str_pattern_1"}},
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
							t.Errorf("Consumer name mismatch.\nExpected: %#v;\nActual: %#v", tst.consumers[i].Name, g.StreamGroupConsumersInfo[i].Name)
						}
						if g.StreamGroupConsumersInfo[i].Pending != tst.consumers[i].Pending {
							t.Errorf("Pending items mismatch for %s.\nExpected: %#v;\nActual: %#v", g.StreamGroupConsumersInfo[i].Name, tst.consumers[i].Pending, g.StreamGroupConsumersInfo[i].Pending)
						}

					}
				} else {
					t.Errorf("Consumers entries mismatch.\nExpected: %d;\nActual: %d", len(tst.consumers), len(g.StreamGroupConsumersInfo))
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
		Options{Namespace: "test", CheckSingleStreams: dbNumStrFull + "=" + TestStreamName},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	setupDBKeys(t, addr)
	defer deleteKeysFromDB(t, addr)

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
