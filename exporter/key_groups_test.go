package exporter

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func getDBCount(c redis.Conn) (dbCount int, err error) {
	dbCount = 16
	var config []string
	if config, err = redis.Strings(doRedisCmd(c, "CONFIG", "GET", "*")); err != nil {
		return
	}

	for pos := 0; pos < len(config)/2; pos++ {
		strKey := config[pos*2]
		strVal := config[pos*2+1]

		if strKey == "databases" {
			if dbCount, err = strconv.Atoi(strVal); err != nil {
				dbCount = 16
			}
			return
		}
	}
	return
}

type keyGroupData struct {
	name                   string
	checkKeyGroups         string
	maxDistinctKeyGroups   int64
	wantedCount            map[string]int
	wantedMemory           map[string]bool
	wantedDistintKeyGroups int
}

func TestKeyGroupMetrics(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	var dbCount int
	if dbCount, err = getDBCount(c); err != nil {
		t.Fatalf("Couldn't get dbCount: %#v", err)
	}
	setupDBKeys(t, addr)
	defer deleteKeysFromDB(t, addr)

	tsts := []keyGroupData{
		{
			name:                 "synchronous with unclassified keys",
			checkKeyGroups:       "^(key_ringo)_[0-9]+$,^(key_paul)_[0-9]+$,^(key_exp)_.+$",
			maxDistinctKeyGroups: 100,
			// The actual counts are a function of keys (all types) being set up in the init() function
			// and the CheckKeyGroups regexes for initializing the Redis exporter above. The count below
			// will need to be updated if either of the aforementioned things have changed.
			wantedCount: map[string]int{
				"key_ringo":    1,
				"key_paul":     1,
				"unclassified": 6,
				"key_exp":      5,
			},
			wantedMemory: map[string]bool{
				"key_ringo":    true,
				"key_paul":     true,
				"unclassified": true,
				"key_exp":      true,
			},
			wantedDistintKeyGroups: 4,
		},
		{
			name:                 "synchronous with overflow keys",
			checkKeyGroups:       "^(.*)$", // Each key is a distinct key group
			maxDistinctKeyGroups: 1,
			// The actual counts depend on the largest key being set up in the init()
			// function (test-stream at the time this code was written) and the total
			// of keys (all types). This will need to be updated to match future
			// updates of the init() function
			wantedCount: map[string]int{
				"test-stream": 1,
				"overflow":    12,
			},
			wantedMemory: map[string]bool{
				"test-stream": true,
				"overflow":    true,
			},
			wantedDistintKeyGroups: 13,
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			e, _ := NewRedisExporter(
				addr,
				Options{
					Namespace:            "test",
					CheckKeyGroups:       tst.checkKeyGroups,
					CheckKeysBatchSize:   1000,
					MaxDistinctKeyGroups: tst.maxDistinctKeyGroups,
				},
			)
			for {
				chM := make(chan prometheus.Metric)
				go func() {
					e.extractKeyGroupMetrics(chM, c, dbCount)
					close(chM)
				}()

				actualCount := make(map[string]int)
				actualMemory := make(map[string]bool)
				actualDistinctKeyGroups := 0

				receivedMetrics := false
				for m := range chM {
					receivedMetrics = true
					got := &dto.Metric{}
					m.Write(got)

					if strings.Contains(m.Desc().String(), "test_key_group_count") {
						for _, label := range got.GetLabel() {
							if *label.Name == "key_group" {
								actualCount[*label.Value] = int(*got.Gauge.Value)
							}
						}
					} else if strings.Contains(m.Desc().String(), "test_key_group_memory_usage_bytes") {
						for _, label := range got.GetLabel() {
							if *label.Name == "key_group" {
								actualMemory[*label.Value] = true
							}
						}
					} else if strings.Contains(m.Desc().String(), "test_number_of_distinct_key_groups") {
						for _, label := range got.GetLabel() {
							if *label.Name == "db" && *label.Value == "db"+dbNumStr {
								actualDistinctKeyGroups = int(*got.Gauge.Value)
							}
						}
					}
				}

				if !receivedMetrics {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				if !reflect.DeepEqual(tst.wantedCount, actualCount) {
					t.Errorf("Key group count metrics are not expected:\n Expected: %#v\nActual: %#v\n", tst.wantedCount, actualCount)
				}

				// It's a little fragile to anticipate how much memory
				// will be allocated for specific key groups, so we
				// are only going to check for presence of memory usage
				// metrics for expected key groups here.
				if !reflect.DeepEqual(tst.wantedMemory, actualMemory) {
					t.Errorf("Key group memory usage metrics are not expected:\n Expected: %#v\nActual: %#v\n", tst.wantedMemory, actualMemory)
				}

				if actualDistinctKeyGroups != tst.wantedDistintKeyGroups {
					t.Errorf("Unexpected number of distinct key groups, expected: %d, actual: %d", tst.wantedDistintKeyGroups, actualDistinctKeyGroups)
				}
				break
			}
		})
	}
}
