package exporter

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// defaultCount is used for `SCAN whatever COUNT defaultCount` command
const (
	defaultCount int64 = 10
	invalidCount int64 = 0
)

func TestKeyValuesAndSizes(t *testing.T) {
	e, _ := NewRedisExporter(
		os.Getenv("TEST_REDIS_URI"),
		Options{
			Namespace:       "test",
			CheckSingleKeys: dbNumStrFull + "=" + url.QueryEscape(keys[0]),
			Registry:        prometheus.NewRegistry()},
	)
	ts := httptest.NewServer(e)
	defer ts.Close()

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	for _, want := range []string{
		"test_key_size",
		"test_key_value",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("didn't find %s, body: %s", want, body)
			return
		}
	}
}

func TestKeyValuesAsLabel(t *testing.T) {
	for _, exc := range []bool{true, false} {
		e, _ := NewRedisExporter(
			os.Getenv("TEST_REDIS_URI"),
			Options{
				Namespace:                 "test",
				CheckSingleKeys:           dbNumStrFull + "=" + url.QueryEscape(singleStringKey),
				DisableExportingKeyValues: exc,
				Registry:                  prometheus.NewRegistry()},
		)
		ts := httptest.NewServer(e)
		defer ts.Close()

		setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
		defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

		chM := make(chan prometheus.Metric, 10000)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		body := downloadURL(t, ts.URL+"/metrics")
		for _, match := range []string{
			"key_value_as_string",
			"test_key_value",
		} {
			if exc && strings.Contains(body, match) {
				t.Fatalf("didn't expect %s with DisableExportingKeyValues enabled, body: %s", match, body)
			} else if !exc && !strings.Contains(body, match) {
				t.Fatalf("didn't find %s with DisableExportingKeyValues disabled, body: %s", match, body)
			}
		}
	}
}

func TestClusterKeyValuesAndSizes(t *testing.T) {
	if os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI") == "" {
		t.Skipf("Skipping TestClusterKeyValuesAndSizes, don't have env var TEST_REDIS_CLUSTER_MASTER_URI")
	}
	for _, exc := range []bool{true, false} {
		e, _ := NewRedisExporter(
			os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI"),
			Options{Namespace: "test", DisableExportingKeyValues: exc, CheckSingleKeys: dbNumStrFull + "=" + url.QueryEscape(keys[0]), IsCluster: true},
		)

		uri := os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI")

		setupDBKeysCluster(t, uri)
		defer deleteKeysFromDBCluster(uri)

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		want := map[string]bool{"test_key_size": false, "test_key_value": false}

		for m := range chM {
			for k := range want {
				if strings.Contains(m.Desc().String(), k) {
					want[k] = true
				}
			}
		}
		for k, found := range want {
			if k == "test_key_value" {
				if found && exc {
					t.Errorf("didn't expect %s with DisableExportingKeyValues enabled", k)
				} else if !found && !exc {
					t.Errorf("didn't find %s with DisableExportingKeyValues disabled", k)
				}
			} else if !found {
				t.Errorf("didn't find %s", k)
			}
		}
	}
}

func TestParseKeyArg(t *testing.T) {
	for _, test := range []struct {
		name          string
		keyArgs       string
		expected      []dbKeyPair
		expectSuccess bool
	}{
		// positive tests
		{"empty_args", "", []dbKeyPair{}, true},
		{"default_database", "my-key", []dbKeyPair{{"0", "my-key"}}, true},
		{"prefixed_database", "db0=my-key", []dbKeyPair{{"0", "my-key"}}, true},
		{"indexed_database", "0=my-key", []dbKeyPair{{"0", "my-key"}}, true},
		{"triple_key", "check-key-01", []dbKeyPair{{"0", "check-key-01"}}, true},
		{
			name:    "default_database_multiple_keys",
			keyArgs: "my-key1,my-key2",
			expected: []dbKeyPair{
				{"0", "my-key1"},
				{"0", "my-key2"},
			},
			expectSuccess: true,
		},
		{
			name:    "key_with_leading_space",
			keyArgs: "my-key-noSpace, my-key-withSpace",
			expected: []dbKeyPair{
				{"0", "my-key-noSpace"},
				{"0", "my-key-withSpace"},
			},
			expectSuccess: true,
		},
		{
			name:    "key_with_spaces",
			keyArgs: "my-key-noSpace1, my-key-withSpaces ,my-key-noSpace2",
			expected: []dbKeyPair{
				{"0", "my-key-noSpace1"},
				{"0", "my-key-withSpaces"},
				{"0", "my-key-noSpace2"},
			},
			expectSuccess: true,
		},
		{
			name:    "different_databases",
			keyArgs: "db0=key1,db1=key1",
			expected: []dbKeyPair{
				{"0", "key1"},
				{"1", "key1"},
			},
			expectSuccess: true,
		},
		{
			name:    "dbdb_replace",
			keyArgs: "dbdbdb0=key1,db1=key1",
			expected: []dbKeyPair{
				{"0", "key1"},
				{"1", "key1"},
			},
			expectSuccess: true,
		},
		{
			name:    "default_database_with_another",
			keyArgs: "key1,db1=key1",
			expected: []dbKeyPair{
				{"0", "key1"},
				{"1", "key1"},
			},
			expectSuccess: true,
		},
		{
			"invalid_args_with_args_separator_skipped",
			"=", []dbKeyPair{}, true,
		},
		{
			"empty_args_with_comma_separators_skipped",
			",,,my-key", []dbKeyPair{{"0", "my-key"}}, true,
		},
		{
			"multiple_invalid_args_skipped",
			"=,=,,0=my-key", []dbKeyPair{{"0", "my-key"}}, true,
		},
		{
			"empty_key_with_args_separator_skipped",
			"0=", []dbKeyPair{}, true,
		},
		{
			"empty_database_with_args_separator_skipped",
			"=my-key", []dbKeyPair{}, true,
		},
		// negative tests
		{
			"string_database_index",
			"wrong=my-key", []dbKeyPair{}, false,
		},
		{
			"prefixed_string_database_index",
			"dbwrong=my-key", []dbKeyPair{}, false,
		},
		{
			"wrong_args_count",
			"wrong=wrong=wrong", []dbKeyPair{}, false,
		},
		{
			"wrong_args",
			"wrong=wrong=1", []dbKeyPair{}, false,
		},
		{
			"negative_database_index",
			"db-1=my-key", []dbKeyPair{}, false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parseKeyArg(test.keyArgs)
			if test.expectSuccess && err != nil {
				t.Errorf("Expected success for test: %s, got err: %s", test.name, err)
				return
			}
			if len(parsed) != len(test.expected) {
				t.Errorf("Parsed elements count don't match expected: parsed %d; expected %d", len(parsed), len(test.expected))
				return
			}
			for i, pair := range test.expected {
				if pair != parsed[i] {
					t.Errorf("Parsed elements don't match expected dbKeyPair:\n parsed %#v;\nexpected %#v", parsed[i], pair)
					return
				}
			}

			if !test.expectSuccess && err == nil {
				t.Errorf("Expected failure for test: %s, got no err", test.name)
				return
			}
			if !test.expectSuccess && err != nil {
				t.Logf("Expected failure for test: %s, got err: %s", test.name, err)
				return
			}
		})
	}
}

type keyFixture struct {
	command string
	key     string
	args    []interface{}
}

func newKeyFixture(command string, key string, args ...interface{}) keyFixture {
	return keyFixture{command, key, args}
}

func createKeyFixtures(t *testing.T, c redis.Conn, fixtures []keyFixture) {
	for _, f := range fixtures {
		args := append([]interface{}{f.key}, f.args...)
		if _, err := c.Do(f.command, args...); err != nil {
			t.Fatalf("Error creating fixture: %#v, %#v", f, err)
		}
	}
}

func deleteKeyFixtures(t *testing.T, c redis.Conn, fixtures []keyFixture) {
	for _, f := range fixtures {
		if _, err := c.Do("DEL", f.key); err != nil {
			t.Errorf("Error deleting fixture: %#v, %#v", f, err)
		}
	}
}

func TestScanKeys(t *testing.T) {
	numKeys := 1000
	var fixtures []keyFixture

	// Make 1000 keys that match
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("get_keys_test_shouldmatch_%v", i)
		fixtures = append(fixtures, newKeyFixture("SET", key, "Woohoo!"))
	}

	// And 1000 that don't
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("get_keys_test_shouldnotmatch_%v", i)
		fixtures = append(fixtures, newKeyFixture("SET", key, "Rats!"))
	}

	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()

	createKeyFixtures(t, c, fixtures)

	matches, err := redis.Strings(scanKeys(c, "get_keys_test_*shouldmatch*", defaultCount))
	if err != nil {
		t.Errorf("Error getting keys matching a pattern: %#v", err)
	}

	numMatches := len(matches)
	if numMatches != numKeys {
		t.Errorf("Expected %#v matches, got %#v.", numKeys, numMatches)
	}

	for _, match := range matches {
		if !strings.HasPrefix(match, "get_keys_test_shouldmatch") {
			t.Errorf("Expected match to have prefix: get_keys_test_shouldmatch")
		}
	}

	// Test expected errors separately
	invalidFixtures := map[string]int64{
		// empty string is a string after all
		"":        100,
		"pattern": invalidCount,
	}
	for pattern, count := range invalidFixtures {
		got, err := redis.Strings(scanKeys(c, pattern, count))
		if err != nil {
			t.Logf("\"Passed\" expected, got error: %#v", err)
			if pattern == "" && err.Error() != "Pattern shouldn't be empty" {
				t.Errorf("\"Empty pattern\" error message expected, but got: %s", err.Error())
			}
		} else {
			if len(got) >= 0 {
				t.Errorf("Error expected, got valid response: %#v", got)
			}
		}
	}
}

func TestGetKeysFromPatterns(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_URI")
	dbMain := dbNumStr
	dbAlt := altDBNumStr
	dbInvalid := invalidDBNumStr

	dbMainFixtures := []keyFixture{
		newKeyFixture("SET", "dbMainNoPattern1", "woohoo!"),
		newKeyFixture("SET", "dbMainSomePattern1", "woohoo!"),
		newKeyFixture("SET", "dbMainSomePattern2", "woohoo!"),
	}

	dbAltFixtures := []keyFixture{
		newKeyFixture("SET", "dbAltNoPattern1", "woohoo!"),
		newKeyFixture("SET", "dbAltSomePattern1", "woohoo!"),
		newKeyFixture("SET", "dbAltSomePattern2", "woohoo!"),
	}

	keys := []dbKeyPair{
		{db: dbMain, key: "dbMainNoPattern1"},
		{db: dbMain, key: "*SomePattern*"},
		{db: dbAlt, key: "dbAltNoPattern1"},
		{db: dbAlt, key: "*SomePattern*"},
	}
	invalidKeys := []dbKeyPair{
		{db: dbInvalid, key: "someUnusedPattern*"},
	}

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	defer func() {
		_, err = c.Do("SELECT", dbMain)
		if err != nil {
			t.Errorf("Couldn't select database %#v", dbMain)
		}
		deleteKeyFixtures(t, c, dbMainFixtures)

		_, err = c.Do("SELECT", dbAlt)
		if err != nil {
			t.Errorf("Couldn't select database %#v", dbAlt)
		}
		deleteKeyFixtures(t, c, dbAltFixtures)
		c.Close()
	}()

	_, err = c.Do("SELECT", dbMain)
	if err != nil {
		t.Errorf("Couldn't select database %#v", dbMain)
	}
	createKeyFixtures(t, c, dbMainFixtures)

	_, err = c.Do("SELECT", dbAlt)
	if err != nil {
		t.Errorf("Couldn't select database %#v", dbAlt)
	}
	createKeyFixtures(t, c, dbAltFixtures)

	expandedKeys, err := getKeysFromPatterns(c, keys, defaultCount)
	if err != nil {
		t.Errorf("Error getting keys from patterns: %#v", err)
	}

	expectedKeys := []dbKeyPair{
		{db: dbMain, key: "dbMainNoPattern1"},
		{db: dbMain, key: "dbMainSomePattern1"},
		{db: dbMain, key: "dbMainSomePattern2"},
		{db: dbAlt, key: "dbAltNoPattern1"},
		{db: dbAlt, key: "dbAltSomePattern1"},
		{db: dbAlt, key: "dbAltSomePattern2"},
	}

	sort.Slice(expectedKeys, func(i, j int) bool {
		return (expectedKeys[i].db + expectedKeys[i].key) < (expectedKeys[j].db + expectedKeys[j].key)
	})

	sort.Slice(expandedKeys, func(i, j int) bool {
		return (expandedKeys[i].db + expandedKeys[i].key) < (expandedKeys[j].db + expandedKeys[j].key)
	})

	if !reflect.DeepEqual(expectedKeys, expandedKeys) {
		t.Errorf("When expanding keys:\nexpected: %#v\nactual:   %#v", expectedKeys, expandedKeys)
	}

	got, err := getKeysFromPatterns(c, invalidKeys, defaultCount)
	if err != nil {
		t.Logf("Expected error - \"invalid DB\": %#v", err)
	} else {
		if len(got) != 0 {
			t.Errorf("Error expected with invalid database %#v, got valid response: %#v", invalidKeys, got)
		}
	}

}

func TestGetKeyInfo(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"SET", "key_info_test_string", []interface{}{"Woohoo!"}},
		{"HSET", "key_info_test_hash", []interface{}{"hashkey1", "hashval1"}},
		{"PFADD", "key_info_test_hll", []interface{}{"hllval1", "hllval2"}},
		{"LPUSH", "key_info_test_list", []interface{}{"listval1", "listval2", "listval3"}},
		{"SADD", "key_info_test_set", []interface{}{"setval1", "setval2", "setval3", "setval4"}},
		{"ZADD", "key_info_test_zset", []interface{}{
			"1", "zsetval1",
			"2", "zsetval2",
			"3", "zsetval3",
			"4", "zsetval4",
			"5", "zsetval5",
		}},
		{"XADD", "key_info_test_stream", []interface{}{"*", "field1", "str1"}},
	}

	createKeyFixtures(t, c, fixtures)

	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()

	expectedSizes := map[string]float64{
		"key_info_test_string": 7,
		"key_info_test_hash":   1,
		"key_info_test_hll":    2,
		"key_info_test_list":   3,
		"key_info_test_set":    4,
		"key_info_test_zset":   5,
		"key_info_test_stream": 1,
	}

	// Test all known types
	for _, f := range fixtures {
		info, err := getKeyInfo(c, f.key, false)
		if err != nil {
			t.Errorf("Error getting key info for %#v.", f.key)
		}

		expected := expectedSizes[f.key]
		if info.size != expected {
			t.Logf("%#v", info)
			t.Errorf("Wrong size for key: %#v. Expected: %#v; Actual: %#v", f.key, expected, info.size)
		}
	}

	// Test absent key returns the correct error
	_, err = getKeyInfo(c, "absent_key", false)
	if !errors.Is(err, errKeyTypeNotFound) {
		t.Error("Expected `errKeyTypeNotFound` for absent key.  Got a different error.")
	}
}

func TestKeySizeList(t *testing.T) {
	s := dbNumStrFull + "=" + listKeys[0]
	e, _ := NewRedisExporter(
		os.Getenv("TEST_REDIS_URI"),
		Options{Namespace: "test", CheckSingleKeys: s},
	)

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	found := false
	for m := range chM {
		if strings.Contains(m.Desc().String(), "test_key_size") {
			found = true
		}
	}

	if !found {
		t.Errorf("didn't find the key")
	}
}

func TestKeyValueInvalidDB(t *testing.T) {
	e, _ := NewRedisExporter(
		os.Getenv("TEST_REDIS_URI"),
		Options{
			Namespace:       "test",
			CheckSingleKeys: "999=" + url.QueryEscape(keys[0]),
		},
	)

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	dontWant := map[string]bool{"test_key_size": false}
	for m := range chM {
		switch m.(type) {
		case prometheus.Gauge:
			for k := range dontWant {
				if strings.Contains(m.Desc().String(), k) {
					log.Println(m.Desc().String())
					dontWant[k] = true
				}
			}
		default:
			log.Debugf("default: m: %#v", m)
		}
	}
	for k, found := range dontWant {
		if found {
			t.Errorf("we found %s but it shouldn't be there", k)
		}
	}
}

func TestCheckKeys(t *testing.T) {
	for _, tst := range []struct {
		SingleCheckKey string
		CheckKeys      string
		ExpectSuccess  bool
	}{
		{"", "", true},
		{"db1=key3", "", true},
		{"check-key-01", "", true},
		{"", "check-key-02", true},
		{"wrong=wrong=1", "", false},
		{"", "wrong=wrong=2", false},
	} {
		_, err := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: tst.SingleCheckKey, CheckKeys: tst.CheckKeys})
		if tst.ExpectSuccess && err != nil {
			t.Errorf("Expected success for test: %#v, got err: %s", tst, err)
			return
		}

		if !tst.ExpectSuccess && err == nil {
			t.Errorf("Expected failure for test: %#v, got no err", tst)
			return
		}
	}
}

func TestCheckSingleKeyDefaultsTo0(t *testing.T) {
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: "single", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, `test_key_size{db="db0",key="single"} 0`) {
		t.Errorf("Expected metric `test_key_size` with key=`single` and value 0 but got:\n%s", body)
	}
}

func TestGetKeysCount(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_URI")
	db := dbNumStr

	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("Couldn't select database %#v", db)
	}

	fixtures := []keyFixture{
		{"SET", "count_test:keys_count_test_string1", []interface{}{"Woohoo!"}},
		{"SET", "count_test:keys_count_test_string2", []interface{}{"!oohooW"}},
		{"LPUSH", "count_test:keys_count_test_list1", []interface{}{"listval1", "listval2", "listval3"}},
		{"LPUSH", "count_test:keys_count_test_list2", []interface{}{"listval1", "listval2", "listval3"}},
		{"LPUSH", "count_test:keys_count_test_list3", []interface{}{"listval1", "listval2", "listval3"}},
	}

	createKeyFixtures(t, c, fixtures)
	defer func() {
		deleteKeyFixtures(t, c, fixtures)
		c.Close()
	}()

	expectedCount := map[string]int{
		"count_test:keys_count_test_string*": 2,
		"count_test:keys_count_test_list*":   3,
		"count_test:*":                       5,
	}

	for pattern, count := range expectedCount {
		actualCount, err := getKeysCount(c, pattern, defaultCount)
		if err != nil {
			t.Errorf("Error getting count for pattern \"%#v\"", pattern)
		}

		if actualCount != count {
			t.Errorf("Wrong count for pattern \"%#v\". Expected: %#v; Actual: %#v", pattern, count, actualCount)
		}
	}

	got, err := getKeysCount(c, "pattern", invalidCount)
	if err != nil {
		t.Logf("Expected error - \"error retrieving keys\": %#v", err)
	} else {
		if got >= 0 {
			t.Errorf("Error expected with invalidCount option \"%#v\", got valid response: %#v", invalidCount, got)
		}
	}
}
