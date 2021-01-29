package exporter

import (
	"fmt"
	"net/url"
	"regexp"
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

// scanForKeys returns a list of keys matching `pattern` by using `SCAN`, which is safer for production systems than using `KEYS`.
// This function was adapted from: https://github.com/reisinger/examples-redigo
func scanForKeys(c redis.Conn, pattern string) ([]string, error) {
	iter := 0
	keys := []string{}

	for {
		arr, err := redis.Values(doRedisCmd(c, "SCAN", iter, "MATCH", pattern))
		if err != nil {
			return keys, fmt.Errorf("error retrieving '%s' keys err: %s", pattern, err)
		}
		if len(arr) != 2 {
			return keys, fmt.Errorf("invalid response from SCAN for pattern: %s", pattern)
		}

		k, _ := redis.Strings(arr[1], nil)
		keys = append(keys, k...)

		if iter, _ = redis.Int(arr[0], nil); iter == 0 {
			break
		}
	}

	return keys, nil
}

// getKeysFromPatterns does a SCAN for a key if the key contains pattern characters
func getKeysFromPatterns(c redis.Conn, keys []dbKeyPair) (expandedKeys []dbKeyPair, err error) {
	expandedKeys = []dbKeyPair{}
	for _, k := range keys {
		if regexp.MustCompile(`[\?\*\[\]\^]+`).MatchString(k.key) {
			if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
				return expandedKeys, err
			}
			keyNames, err := scanForKeys(c, k.key)
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
	scannedKeys, err := getKeysFromPatterns(c, keys)
	if err != nil {
		log.Errorf("Error expanding key patterns: %#v", err)
	} else {
		allKeys = append(allKeys, scannedKeys...)
	}

	log.Debugf("allKeys: %#v", allKeys)
	for _, k := range allKeys {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			log.Debugf("Couldn't select database %#v when getting key info.", k.db)
			continue
		}

		info, err := getKeyInfo(c, k.key)
		if err != nil {
			switch err {
			case errKeyTypeNotFound:
				log.Debugf("Key '%s' not found when trying to get type and size.", k.key)
			default:
				log.Error(err)
			}
			continue
		}
		dbLabel := "db" + k.db
		e.registerConstMetricGauge(ch, "key_size", info.size, dbLabel, k.key)

		// Only record value metric if value is float-y
		if val, err := redis.Float64(doRedisCmd(c, "GET", k.key)); err == nil {
			e.registerConstMetricGauge(ch, "key_value", val, dbLabel, k.key)
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
			log.Debugf("Couldn't select database '%s' when getting stream info", k.db)
			continue
		}
		cnt, err := scanForKeyCount(c, k.key)
		if err != nil {
			log.Errorf("couldn't get key count for '%s', err: %s", k.key, err)
			continue
		}
		dbLabel := "db" + k.db
		e.registerConstMetricGauge(ch, "keys_count", float64(cnt), dbLabel, k.key)
	}
}

func scanForKeyCount(c redis.Conn, pattern string) (int, error) {
	iter := 0
	keys := 0

	for {
		arr, err := redis.Values(doRedisCmd(c, "SCAN", iter, "MATCH", pattern))
		if err != nil {
			return keys, fmt.Errorf("error retrieving '%s' keys err: %s", pattern, err)
		}
		if len(arr) != 2 {
			return keys, fmt.Errorf("invalid response from SCAN for pattern: %s", pattern)
		}

		k, _ := redis.Values(arr[1], nil)
		keys += len(k)

		if iter, _ = redis.Int(arr[0], nil); iter == 0 {
			break
		}
	}

	return keys, nil
}

// splitKeyArgs splits a command-line supplied argument into a slice of dbKeyPairs.
func parseKeyArg(keysArgString string) (keys []dbKeyPair, err error) {
	if keysArgString == "" {
		return keys, err
	}
	for _, k := range strings.Split(keysArgString, ",") {
		db := "0"
		key := ""
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

		keys = append(keys, dbKeyPair{db, key})
	}
	return keys, err
}
