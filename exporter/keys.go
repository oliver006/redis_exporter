package exporter

import (
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

type dbKeyPair struct {
	db  string
	key string
}

func getStringInfoNotPipelined(c redis.Conn, key string) (strVal string, keyType string, size int64, err error) {
	if strVal, err = redis.String(doRedisCmd(c, "GET", key)); err != nil {
		slog.Error("GET err", "key", key, "error", err)
	}

	// Check PFCOUNT first because STRLEN on HyperLogLog strings returns the wrong length
	// while PFCOUNT only works on HLL strings and returns an error on regular strings.
	//
	// no pipelining / batching for cluster mode, it's not supported
	if size, err = redis.Int64(doRedisCmd(c, "PFCOUNT", key)); err == nil {
		// hyperloglog
		keyType = "HLL"
		return
	} else if size, err = redis.Int64(doRedisCmd(c, "STRLEN", key)); err == nil {
		keyType = "string"
		return
	}
	return
}

func (e *Exporter) getKeyInfo(ch chan<- prometheus.Metric, c redis.Conn, dbLabel string, keyType string, keyName string) {
	var err error
	var size int64
	var strVal string

	switch keyType {
	case "none":
		slog.Debug("Key not found when trying to get type and size: using default '0.0'", "key", keyName)
		e.registerConstMetricGauge(ch, "key_size", 0.0, dbLabel, keyName)
		return

	case "string":
		strVal, keyType, size, err = getStringInfoNotPipelined(c, keyName)
	case "list":
		size, err = redis.Int64(doRedisCmd(c, "LLEN", keyName))
	case "set":
		size, err = redis.Int64(doRedisCmd(c, "SCARD", keyName))
	case "zset":
		size, err = redis.Int64(doRedisCmd(c, "ZCARD", keyName))
	case "hash":
		size, err = redis.Int64(doRedisCmd(c, "HLEN", keyName))
	case "stream":
		size, err = redis.Int64(doRedisCmd(c, "XLEN", keyName))
	default:
		err = fmt.Errorf("unknown type: %v for key: %v", keyType, keyName)
	}

	if err != nil {
		slog.Error("Failed to get key info", "error", err)
		return
	}

	e.registerConstMetricGauge(ch, "key_size", float64(size), dbLabel, keyName)

	// Only run on single value strings
	if keyType == "string" && !e.options.DisableExportingKeyValues && strVal != "" {
		if val, err := strconv.ParseFloat(strVal, 64); err == nil {
			// Only record value metric if value is float-y
			e.registerConstMetricGauge(ch, "key_value", val, dbLabel, keyName)
		} else {
			// if it's not float-y then we'll record the value as a string label
			e.registerConstMetricGauge(ch, "key_value_as_string", 1.0, dbLabel, keyName, strVal)
		}
	}
}

func (e *Exporter) extractCheckKeyMetrics(ch chan<- prometheus.Metric, c redis.Conn) error {

	keys, err := parseKeyArg(e.options.CheckKeys)
	if err != nil {
		return fmt.Errorf("couldn't parse check-keys: %w", err)
	}
	slog.Debug("keys", "keys", keys)

	singleKeys, err := parseKeyArg(e.options.CheckSingleKeys)
	if err != nil {
		return fmt.Errorf("couldn't parse check-single-keys: %w", err)
	}
	slog.Debug("e.singleKeys", "singleKeys", singleKeys)

	allKeys := append([]dbKeyPair{}, singleKeys...)

	slog.Debug("e.keys", "keys", keys)

	if scannedKeys, err := getKeysFromPatterns(c, keys, e.options.CheckKeysBatchSize); err == nil {
		allKeys = append(allKeys, scannedKeys...)
	} else {
		slog.Error("Error expanding key patterns", "error", err)
	}

	slog.Debug("allKeys", "allKeys", allKeys)

	/*
		important: when adding, modifying, removing metrics both paths here
		(pipelined/non-pipelined) need to be modified
	*/
	if e.options.IsCluster {
		e.extractCheckKeyMetricsNotPipelined(ch, c, allKeys)
	} else {
		e.extractCheckKeyMetricsPipelined(ch, c, allKeys)
	}
	return nil
}

func (e *Exporter) extractCheckKeyMetricsPipelined(ch chan<- prometheus.Metric, c redis.Conn, allKeys []dbKeyPair) {
	//
	// the following commands are all pipelined/batched to improve performance
	// by removing one roundtrip to the redis instance
	// see https://github.com/oliver006/redis_exporter/issues/980
	//

	/*
		group keys by DB so we don't have to do repeated SELECT calls and jump between DBs
		--> saves roundtrips, improves latency
	*/
	keysByDb := map[string][]string{}
	for _, k := range allKeys {
		if a, ok := keysByDb[k.db]; ok {
			// exists already
			a = append(a, k.key)
			keysByDb[k.db] = a
		} else {
			// first time - got to init the array
			keysByDb[k.db] = []string{k.key}
		}
	}

	for dbNum, arrayOfKeys := range keysByDb {
		dbLabel := "db" + dbNum

		slog.Debug("c.Send() SELECT", "db", dbNum)
		if err := c.Send("SELECT", dbNum); err != nil {
			slog.Error("Couldn't select database when getting key info", "db", dbNum)
			continue
		}
		/*
			first pipeline (batch) all the TYPE & MEMORY USAGE calls and ship them to the redis instance
			everything else is dependent on the TYPE of the key
		*/

		for _, keyName := range arrayOfKeys {
			slog.Debug("c.Send() TYPE", "key", keyName)
			if err := c.Send("TYPE", keyName); err != nil {
				slog.Error("Failed to send TYPE command", "error", err)
				return
			}
			slog.Debug("c.Send() MEMORY USAGE", "key", keyName)
			if err := c.Send("MEMORY", "USAGE", keyName); err != nil {
				slog.Error("Failed to send MEMORY USAGE command", "error", err)
				return
			}
		}

		slog.Debug("c.Flush()")
		if err := c.Flush(); err != nil {
			slog.Error("FLUSH err", "error", err)
			return
		}

		// throwaway Receive() call for the response of the SELECT() call
		if _, err := redis.String(c.Receive()); err != nil {
			slog.Error("Failed to receive response", "error", err)
			continue
		}

		/*
			populate "keyTypes" with the batched TYPE responses from the redis instance
			and collect MEMORY USAGE responses and immediately emmit that metric
		*/
		keyTypes := make([]string, len(arrayOfKeys))
		for idx, keyName := range arrayOfKeys {
			var err error
			keyTypes[idx], err = redis.String(c.Receive())
			if err != nil {
				slog.Error("key - Receive err", "key", keyName, "error", err)
				continue
			}
			memUsageInBytes, err := redis.Int64(c.Receive())
			if err != nil {
				// slog.Error(fmt.Sprintf("key: [%s] - memUsageInBytes Receive() err: %s", keyName, err)
				continue
			}

			e.registerConstMetricGauge(ch,
				"key_memory_usage_bytes",
				float64(memUsageInBytes),
				dbLabel,
				keyName)
		}

		/*
			now that we have the types for all the keys we can gather information about
			each key like size & length and value (redis cmd used is dependent on TYPE)
		*/
		e.getKeyInfoPipelined(ch, c, dbLabel, arrayOfKeys, keyTypes)
	}
}

func (e *Exporter) getKeyInfoPipelined(ch chan<- prometheus.Metric, c redis.Conn, dbLabel string, arrayOfKeys []string, keyTypes []string) {
	for idx, keyName := range arrayOfKeys {
		keyType := keyTypes[idx]
		switch keyType {
		case "none":
			continue

		case "string":
			slog.Debug("c.Send() PFCOUNT", "key", keyName)
			if err := c.Send("PFCOUNT", keyName); err != nil {
				slog.Error("PFCOUNT err", "error", err)
				return
			}

			slog.Debug("c.Send() STRLEN", "key", keyName)
			if err := c.Send("STRLEN", keyName); err != nil {
				slog.Error("STRLEN err", "error", err)
				return
			}

			slog.Debug("c.Send() GET", "key", keyName)
			if err := c.Send("GET", keyName); err != nil {
				slog.Error("GET err", "error", err)
				return
			}

		case "list":
			slog.Debug("c.Send() LLEN", "key", keyName)
			if err := c.Send("LLEN", keyName); err != nil {
				slog.Error("LLEN err", "error", err)
				return
			}

		case "set":
			slog.Debug("c.Send() SCARD", "key", keyName)
			if err := c.Send("SCARD", keyName); err != nil {
				slog.Error("SCARD err", "error", err)
				return
			}
		case "zset":
			slog.Debug("c.Send() ZCARD", "key", keyName)
			if err := c.Send("ZCARD", keyName); err != nil {
				slog.Error("ZCARD err", "error", err)
				return
			}

		case "hash":
			slog.Debug("c.Send() HLEN", "key", keyName)
			if err := c.Send("HLEN", keyName); err != nil {
				slog.Error("HLEN err", "error", err)
				return
			}

		case "stream":
			slog.Debug("c.Send() XLEN", "key", keyName)
			if err := c.Send("XLEN", keyName); err != nil {
				slog.Error("XLEN err", "error", err)
				return
			}
		default:
			slog.Error("unknown type for key", "type", keyType, "key", keyName)
			continue
		}
	}

	slog.Debug("c.Flush()")
	if err := c.Flush(); err != nil {
		slog.Error("Failed to flush commands", "error", err)
		return
	}

	for idx, keyName := range arrayOfKeys {
		keyType := keyTypes[idx]

		var err error
		var size int64
		var strVal string

		switch keyType {
		case "none":
			slog.Debug("Key not found, skipping", "key", keyName)

		case "string":
			hllSize, hllErr := redis.Int64(c.Receive())
			strSize, strErr := redis.Int64(c.Receive())

			var strValErr error
			if strVal, strValErr = redis.String(c.Receive()); strValErr != nil {
				slog.Error("Failed to receive GET response", "key", keyName, "error", strValErr)
			}

			slog.Debug("Done with c.Receive() x 3")

			if hllErr == nil {
				// hyperloglog
				size = hllSize

				// "TYPE" reports hll as string
				// this will prevent treating the result as a string by the caller (e.g. call GET)
				keyType = "HLL"
			} else if strErr == nil {
				// not hll so possibly a string?
				size = strSize
				keyType = "string"
			} else {
				continue
			}

		case "hash", "list", "set", "stream", "zset":
			size, err = redis.Int64(c.Receive())
		default:
			err = fmt.Errorf("unknown type: %v for key: %v", keyType, keyName)
		}

		if err != nil {
			slog.Error("Failed to get key info", "error", err)
			continue
		}

		if keyType == "string" && !e.options.DisableExportingKeyValues && strVal != "" {
			if val, err := strconv.ParseFloat(strVal, 64); err == nil {
				// Only record value metric if value is float-y
				e.registerConstMetricGauge(ch, "key_value", val, dbLabel, keyName)
			} else {
				// if it's not float-y then we'll record the value as a string label
				e.registerConstMetricGauge(ch, "key_value_as_string", 1.0, dbLabel, keyName, strVal)
			}
		}

		e.registerConstMetricGauge(ch, "key_size", float64(size), dbLabel, keyName)
	}
}

func (e *Exporter) extractCheckKeyMetricsNotPipelined(ch chan<- prometheus.Metric, c redis.Conn, allKeys []dbKeyPair) {
	// Cluster mode only has one db
	// no need to run `SELECT" but got to set it to "0" in the loop because it's used as the label
	for _, k := range allKeys {
		k.db = "0"

		keyType, err := redis.String(doRedisCmd(c, "TYPE", k.key))
		if err != nil {
			slog.Error("TYPE err", "error", keyType)
			continue
		}

		if memUsageInBytes, err := redis.Int64(doRedisCmd(c, "MEMORY", "USAGE", k.key)); err == nil {
			e.registerConstMetricGauge(ch, "key_memory_usage_bytes", float64(memUsageInBytes), "db"+k.db, k.key)
		} else {
			slog.Error("MEMORY USAGE err", "key", k.key, "error", err)
		}

		dbLabel := "db" + k.db
		e.getKeyInfo(ch, c, dbLabel, keyType, k.key)
	}
}

func (e *Exporter) extractCountKeysMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	cntKeys, err := parseKeyArg(e.options.CountKeys)
	if err != nil {
		slog.Error("Couldn't parse given count keys", "error", err)
		return
	}

	for _, k := range cntKeys {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			slog.Error("Couldn't select database when getting stream info", "db", k.db)
			continue
		}
		cnt, err := getKeysCount(c, k.key, e.options.CheckKeysBatchSize)
		if err != nil {
			slog.Error("couldn't get key count", "key", k.key, "error", err)
			continue
		}
		dbLabel := "db" + k.db
		e.registerConstMetricGauge(ch, "keys_count", float64(cnt), dbLabel, k.key)
	}
}

func getKeysCount(c redis.Conn, pattern string, count int64) (int, error) {
	keysCount := 0

	keys, err := scanKeys(c, pattern, count)
	if err != nil {
		return keysCount, fmt.Errorf("error retrieving '%s' keys err: %s", pattern, err)
	}
	keysCount = len(keys)

	return keysCount, nil
}

// Regexp pattern to check if given key contains any
// glob-style pattern symbol.
//
// https://redis.io/commands/scan#the-match-option
var globPattern = regexp.MustCompile(`[\?\*\[\]\^]+`)

// getKeysFromPatterns does a SCAN for a key if the key contains pattern characters
func getKeysFromPatterns(c redis.Conn, keys []dbKeyPair, count int64) (expandedKeys []dbKeyPair, err error) {
	expandedKeys = []dbKeyPair{}
	for _, k := range keys {
		if globPattern.MatchString(k.key) {
			if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
				return expandedKeys, err
			}
			keyNames, err := redis.Strings(scanKeys(c, k.key, count))
			if err != nil {
				slog.Error("error with SCAN for pattern", "pattern", k.key, "error", err)
				continue
			}

			for _, keyName := range keyNames {
				expandedKeys = append(expandedKeys, dbKeyPair{db: k.db, key: keyName})
			}
		} else {
			expandedKeys = append(expandedKeys, k)
		}
	}

	return expandedKeys, err
}

// parseKeyArgs splits a command-line supplied argument into a slice of dbKeyPairs.
func parseKeyArg(keysArgString string) (keys []dbKeyPair, err error) {
	if keysArgString == "" {
		slog.Debug("Got empty key arguments, skipping parsing")
		return keys, err
	}
	for _, k := range strings.Split(keysArgString, ",") {
		var db string
		var key string
		if k == "" {
			continue
		}
		frags := strings.Split(k, "=")
		switch len(frags) {
		case 1:
			db = "0"
			key, err = url.QueryUnescape(strings.TrimSpace(frags[0]))
		case 2:
			db = strings.ReplaceAll(strings.TrimSpace(frags[0]), "db", "")
			key, err = url.QueryUnescape(strings.TrimSpace(frags[1]))
		default:
			return keys, fmt.Errorf("invalid key list argument: %s", k)
		}
		if err != nil {
			return keys, fmt.Errorf("couldn't parse db/key string: %s", k)
		}

		// We want to guarantee at the top level that invalid values
		// will not fall into the final Redis call.
		if db == "" || key == "" {
			slog.Error("Empty value parsed in pair, skipping", "db", db, "key", key)
			continue
		}

		number, err := strconv.Atoi(db)
		if err != nil || number < 0 {
			return keys, fmt.Errorf("invalid database index for db \"%s\": %s", db, err)
		}

		keys = append(keys, dbKeyPair{db, key})
	}
	return keys, err
}

// scanForKeys returns a list of keys matching `pattern` by using `SCAN`, which is safer for production systems than using `KEYS`.
// This function was adapted from: https://github.com/reisinger/examples-redigo
func scanKeys(c redis.Conn, pattern string, count int64) (keys []interface{}, err error) {
	if pattern == "" {
		return keys, fmt.Errorf("pattern shouldn't be empty")
	}

	iter := 0
	for {
		arr, err := redis.Values(doRedisCmd(c, "SCAN", iter, "MATCH", pattern, "COUNT", count))
		if err != nil {
			return keys, fmt.Errorf("error retrieving '%s' keys err: %s", pattern, err)
		}
		if len(arr) != 2 {
			return keys, fmt.Errorf("invalid response from SCAN for pattern: %s", pattern)
		}

		k, _ := redis.Values(arr[1], nil)
		keys = append(keys, k...)

		if iter, _ = redis.Int(arr[0], nil); iter == 0 {
			break
		}
	}

	return keys, nil
}
