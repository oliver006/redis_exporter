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
	db, key string
}

type keyInfo struct {
	size    float64
	keyType string
}

var errKeyTypeNotFound = fmt.Errorf("key not found")

// getKeyInfo takes a key and returns the type, and the size or length of the value stored at that key.
func getKeyInfo(c redis.Conn, key string) (info keyInfo, err error) {
	if info.keyType, err = redis.String(doRedisCmd(c, "TYPE", key)); err != nil {
		return info, err
	}

	switch info.keyType {
	case "none":
		return info, errKeyTypeNotFound
	case "string":
		// Use PFCOUNT first because STRLEN on HyperLogLog strings returns the wrong length
		// while PFCOUNT only works on HLL strings and returns an error on regular strings.
		if size, err := redis.Int64(doRedisCmd(c, "PFCOUNT", key)); err == nil {
			// hyperloglog
			info.size = float64(size)
		} else if size, err := redis.Int64(doRedisCmd(c, "STRLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "list":
		if size, err := redis.Int64(doRedisCmd(c, "LLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "set":
		if size, err := redis.Int64(doRedisCmd(c, "SCARD", key)); err == nil {
			info.size = float64(size)
		}
	case "zset":
		if size, err := redis.Int64(doRedisCmd(c, "ZCARD", key)); err == nil {
			info.size = float64(size)
		}
	case "hash":
		if size, err := redis.Int64(doRedisCmd(c, "HLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "stream":
		if size, err := redis.Int64(doRedisCmd(c, "XLEN", key)); err == nil {
			info.size = float64(size)
		}
	default:
		err = fmt.Errorf("unknown type: %v for key: %v", info.keyType, key)
	}

	return info, err
}

func (e *Exporter) extractCheckKeyMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	keys, err := parseKeyArg(e.options.CheckKeys)
	if err != nil {
		log.Errorf("Couldn't parse check-keys: %#v", err)
		return
	}
	log.Debugf("keys: %#v", keys)

	singleKeys, err := parseKeyArg(e.options.CheckSingleKeys)
	if err != nil {
		log.Errorf("Couldn't parse check-single-keys: %#v", err)
		return
	}
	log.Debugf("e.singleKeys: %#v", singleKeys)

	allKeys := append([]dbKeyPair{}, singleKeys...)

	log.Debugf("e.keys: %#v", keys)
	scannedKeys, err := getKeysFromPatterns(c, keys, e.options.CheckKeysBatchSize)
	if err != nil {
		log.Errorf("Error expanding key patterns: %#v", err)
	} else {
		allKeys = append(allKeys, scannedKeys...)
	}

	log.Debugf("allKeys: %#v", allKeys)
	for _, k := range allKeys {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			log.Errorf("Couldn't select database %#v when getting key info.", k.db)
			continue
		}

		dbLabel := "db" + k.db
		info, err := getKeyInfo(c, k.key)
		switch err {
		case errKeyTypeNotFound:
			log.Debugf("Key '%s' not found when trying to get type and size: using default '0.0'", k.key)
			e.registerConstMetricGauge(ch, "key_size", 0.0, dbLabel, k.key)
		case nil:
			e.registerConstMetricGauge(ch, "key_size", info.size, dbLabel, k.key)

			// Only run on single value strings
			if info.keyType == "string" {
				// Only record value metric if value is float-y
				if val, err := redis.Float64(doRedisCmd(c, "GET", k.key)); err == nil {
					e.registerConstMetricGauge(ch, "key_value", val, dbLabel, k.key)
				}
			}
		default:
			log.Error(err)
		}
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
			db = strings.Replace(strings.TrimSpace(frags[0]), "db", "", -1)
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
			return keys, fmt.Errorf("Invalid database index for db \"%s\": %s", db, err)
		}

		keys = append(keys, dbKeyPair{db, key})
	}
	return keys, err
}

// scanForKeys returns a list of keys matching `pattern` by using `SCAN`, which is safer for production systems than using `KEYS`.
// This function was adapted from: https://github.com/reisinger/examples-redigo
func scanKeys(c redis.Conn, pattern string, count int64) (keys []interface{}, err error) {
	if pattern == "" {
		return keys, fmt.Errorf("Pattern shouldn't be empty")
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
