package exporter

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type dbKeyPair struct {
	db  string
	key string
}

func getStringInfoNotPipelined(c redis.Conn, key string) (strVal string, keyType string, size int64, err error) {
	if strVal, err = redis.String(doRedisCmd(c, "GET", key)); err != nil {
		log.Errorf("GET %s err: %s", key, err)
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
		log.Debugf("Key '%s' not found when trying to get type and size: using default '0.0'", keyName)
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
		log.Errorf("getKeyInfo() err: %s", err)
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
	log.Debugf("keys: %#v", keys)

	singleKeys, err := parseKeyArg(e.options.CheckSingleKeys)
	if err != nil {
		return fmt.Errorf("couldn't parse check-single-keys: %w", err)
	}
	log.Debugf("e.singleKeys: %#v", singleKeys)

	allKeys := append([]dbKeyPair{}, singleKeys...)

	log.Debugf("e.keys: %#v", keys)

	if scannedKeys, err := getKeysFromPatterns(c, keys, e.options.CheckKeysBatchSize); err == nil {
		allKeys = append(allKeys, scannedKeys...)
	} else {
		log.Errorf("Error expanding key patterns: %#v", err)
	}

	log.Debugf("allKeys: %#v", allKeys)

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

		log.Debugf("c.Send() SELECT [%s]", dbNum)
		if err := c.Send("SELECT", dbNum); err != nil {
			log.Errorf("Couldn't select database [%s] when getting key info.", dbNum)
			continue
		}
		/*
			first pipeline (batch) all the TYPE & MEMORY USAGE calls and ship them to the redis instance
			everything else is dependent on the TYPE of the key
		*/

		for _, keyName := range arrayOfKeys {
			log.Debugf("c.Send() TYPE [%v]", keyName)
			if err := c.Send("TYPE", keyName); err != nil {
				log.Errorf("c.Send() TYPE err: %s", err)
				return
			}
			log.Debugf("c.Send() MEMORY USAGE [%v]", keyName)
			if err := c.Send("MEMORY", "USAGE", keyName); err != nil {
				log.Errorf("c.Send() MEMORY USAGE err: %s", err)
				return
			}
		}

		log.Debugf("c.Flush()")
		if err := c.Flush(); err != nil {
			log.Errorf("FLUSH err: %s", err)
			return
		}

		// throwaway Receive() call for the response of the SELECT() call
		if _, err := redis.String(c.Receive()); err != nil {
			log.Errorf("Receive() err: %s", err)
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
				log.Errorf("key: [%s] - Receive err: %s", keyName, err)
				continue
			}
			memUsageInBytes, err := redis.Int64(c.Receive())
			if err != nil {
				// log.Errorf("key: [%s] - memUsageInBytes Receive() err: %s", keyName, err)
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
			log.Debugf("c.Send() PFCOUNT  args: [%v]", keyName)
			if err := c.Send("PFCOUNT", keyName); err != nil {
				log.Errorf("PFCOUNT err: %s", err)
				return
			}

			log.Debugf("c.Send() STRLEN  args: [%v]", keyName)
			if err := c.Send("STRLEN", keyName); err != nil {
				log.Errorf("STRLEN err: %s", err)
				return
			}

			log.Debugf("c.Send() GET  args: [%v]", keyName)
			if err := c.Send("GET", keyName); err != nil {
				log.Errorf("GET err: %s", err)
				return
			}

		case "list":
			log.Debugf("c.Send() LLEN  args: [%v]", keyName)
			if err := c.Send("LLEN", keyName); err != nil {
				log.Errorf("LLEN err: %s", err)
				return
			}

		case "set":
			log.Debugf("c.Send() SCARD  args: [%v]", keyName)
			if err := c.Send("SCARD", keyName); err != nil {
				log.Errorf("SCARD err: %s", err)
				return
			}
		case "zset":
			log.Debugf("c.Send() ZCARD  args: [%v]", keyName)
			if err := c.Send("ZCARD", keyName); err != nil {
				log.Errorf("ZCARD err: %s", err)
				return
			}

		case "hash":
			log.Debugf("c.Send() HLEN  args: [%v]", keyName)
			if err := c.Send("HLEN", keyName); err != nil {
				log.Errorf("HLEN err: %s", err)
				return
			}

		case "stream":
			log.Debugf("c.Send() XLEN  args: [%v]", keyName)
			if err := c.Send("XLEN", keyName); err != nil {
				log.Errorf("XLEN err: %s", err)
				return
			}
		default:
			log.Errorf("unknown type: %v for key: %v", keyType, keyName)
			continue
		}
	}

	log.Debugf("c.Flush()")
	if err := c.Flush(); err != nil {
		log.Errorf("Flush() err: %s", err)
		return
	}

	for idx, keyName := range arrayOfKeys {
		keyType := keyTypes[idx]

		var err error
		var size int64
		var strVal string

		switch keyType {
		case "none":
			log.Debugf("Key '%s' not found, skipping", keyName)

		case "string":
			hllSize, hllErr := redis.Int64(c.Receive())
			strSize, strErr := redis.Int64(c.Receive())

			var strValErr error
			if strVal, strValErr = redis.String(c.Receive()); strValErr != nil {
				log.Errorf("c.Receive() for GET %s err: %s", keyName, strValErr)
			}

			log.Debugf("Done with c.Receive() x 3")

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
			log.Errorf("getKeyInfo() err: %s", err)
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
			log.Errorf("TYPE err: %s", keyType)
			continue
		}

		if memUsageInBytes, err := redis.Int64(doRedisCmd(c, "MEMORY", "USAGE", k.key)); err == nil {
			e.registerConstMetricGauge(ch, "key_memory_usage_bytes", float64(memUsageInBytes), "db"+k.db, k.key)
		} else {
			log.Errorf("MEMORY USAGE %s err: %s", k.key, err)
		}

		dbLabel := "db" + k.db
		e.getKeyInfo(ch, c, dbLabel, keyType, k.key)
	}
}

func (e *Exporter) extractCountKeysMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	cntKeys, err := parseKeyArg(e.options.CountKeys)
	if err != nil {
		log.Errorf("Couldn't parse given count keys: %s", err)
		return
	}

	for _, k := range cntKeys {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			log.Errorf("Couldn't select database '%s' when getting stream info", k.db)
			continue
		}
		cnt, err := getKeysCount(c, k.key, e.options.CheckKeysBatchSize)
		if err != nil {
			log.Errorf("couldn't get key count for '%s', err: %s", k.key, err)
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
				log.Errorf("error with SCAN for pattern: %#v err: %s", k.key, err)
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
		log.Debugf("parseKeyArg(): Got empty key arguments, parsing skipped")
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
			log.Errorf("parseKeyArg(): Empty value parsed in pair '%s=%s', skip", db, key)
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
