package exporter

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type keyGroupMetrics struct {
	keyGroup    string
	count       int64
	memoryUsage int64
}

type overflowedKeyGroupMetrics struct {
	topMemoryUsageKeyGroups   []*keyGroupMetrics
	overflowKeyGroupAggregate keyGroupMetrics
	keyGroupsCount            int64
}

type keyGroupsScrapeResult struct {
	duration          time.Duration
	metrics           []map[string]*keyGroupMetrics
	overflowedMetrics []*overflowedKeyGroupMetrics
}

func (e *Exporter) extractKeyGroupMetrics(ch chan<- prometheus.Metric, c redis.Conn, dbCount int) {
	allDbKeyGroupMetrics := e.gatherKeyGroupsMetricsForAllDatabases(c, dbCount)
	if allDbKeyGroupMetrics == nil {
		return
	}
	for db, dbKeyGroupMetrics := range allDbKeyGroupMetrics.metrics {
		dbLabel := fmt.Sprintf("db%d", db)
		registerKeyGroupMetrics := func(metrics *keyGroupMetrics) {
			e.registerConstMetricGauge(
				ch,
				"key_group_count",
				float64(metrics.count),
				dbLabel,
				metrics.keyGroup,
			)
			e.registerConstMetricGauge(
				ch,
				"key_group_memory_usage_bytes",
				float64(metrics.memoryUsage),
				dbLabel,
				metrics.keyGroup,
			)
		}
		if allDbKeyGroupMetrics.overflowedMetrics[db] != nil {
			overflowedMetrics := allDbKeyGroupMetrics.overflowedMetrics[db]
			for _, metrics := range overflowedMetrics.topMemoryUsageKeyGroups {
				registerKeyGroupMetrics(metrics)
			}
			registerKeyGroupMetrics(&overflowedMetrics.overflowKeyGroupAggregate)
			e.registerConstMetricGauge(ch, "number_of_distinct_key_groups", float64(overflowedMetrics.keyGroupsCount), dbLabel)
		} else if dbKeyGroupMetrics != nil {
			for _, metrics := range dbKeyGroupMetrics {
				registerKeyGroupMetrics(metrics)
			}
			e.registerConstMetricGauge(ch, "number_of_distinct_key_groups", float64(len(dbKeyGroupMetrics)), dbLabel)
		}
	}
	e.registerConstMetricGauge(ch, "last_key_groups_scrape_duration_milliseconds", float64(allDbKeyGroupMetrics.duration.Milliseconds()))
}

func (e *Exporter) gatherKeyGroupsMetricsForAllDatabases(c redis.Conn, dbCount int) *keyGroupsScrapeResult {
	start := time.Now()
	allMetrics := &keyGroupsScrapeResult{
		metrics:           make([]map[string]*keyGroupMetrics, dbCount),
		overflowedMetrics: make([]*overflowedKeyGroupMetrics, dbCount),
	}
	defer func() {
		allMetrics.duration = time.Since(start)
	}()
	if strings.TrimSpace(e.options.CheckKeyGroups) == "" {
		return allMetrics
	}
	keyGroups, err := csv.NewReader(
		strings.NewReader(e.options.CheckKeyGroups),
	).Read()
	if err != nil {
		log.Errorf("Failed to parse key groups as csv: %s", err)
		return allMetrics
	}
	for i, v := range keyGroups {
		keyGroups[i] = strings.TrimSpace(v)
	}

	keyGroupsNoEmptyStrings := make([]string, 0)
	for _, v := range keyGroups {
		if len(v) > 0 {
			keyGroupsNoEmptyStrings = append(keyGroupsNoEmptyStrings, v)
		}
	}
	if len(keyGroupsNoEmptyStrings) == 0 {
		return allMetrics
	}
	for db := range dbCount {
		if _, err := selectRedisDatabase(c, fmt.Sprint(db)); err != nil {
			log.Errorf("Couldn't select database %d when getting key info.", db)
			continue
		}
		var allGroups map[string]*keyGroupMetrics
		var err error
		if scanner, ok := c.(keyScanner); ok && scanner.scanCommand() == "CLUSTERSCAN" {
			allGroups, err = gatherClusterKeyGroupMetrics(c, e.options.CheckKeysBatchSize, keyGroupsNoEmptyStrings)
		} else {
			allGroups, err = gatherKeyGroupMetrics(c, e.options.CheckKeysBatchSize, keyGroupsNoEmptyStrings)
		}
		if err != nil {
			log.Error(err)
			continue
		}
		allMetrics.metrics[db] = allGroups
		if int64(len(allGroups)) > e.options.MaxDistinctKeyGroups {
			metricsSlice := make([]*keyGroupMetrics, 0, len(allGroups))
			for _, v := range allGroups {
				metricsSlice = append(metricsSlice, v)
			}
			sort.Slice(metricsSlice, func(i, j int) bool {
				if metricsSlice[i].memoryUsage == metricsSlice[j].memoryUsage {
					if metricsSlice[i].count == metricsSlice[j].count {
						return metricsSlice[i].keyGroup < metricsSlice[j].keyGroup
					}
					return metricsSlice[i].count < metricsSlice[j].count
				}
				return metricsSlice[i].memoryUsage > metricsSlice[j].memoryUsage
			})
			var overflowedCount, overflowedMemoryUsage int64
			for _, v := range metricsSlice[e.options.MaxDistinctKeyGroups:] {
				overflowedCount += v.count
				overflowedMemoryUsage += v.memoryUsage
			}
			allMetrics.overflowedMetrics[db] = &overflowedKeyGroupMetrics{
				topMemoryUsageKeyGroups: metricsSlice[:e.options.MaxDistinctKeyGroups],
				overflowKeyGroupAggregate: keyGroupMetrics{
					keyGroup:    "overflow",
					count:       overflowedCount,
					memoryUsage: overflowedMemoryUsage,
				},
				keyGroupsCount: int64(len(allGroups)),
			}
		}
	}
	return allMetrics
}

const keyGroupScript = `
local result = {}
local groups = {}
local cursor = 0
local keys = KEYS
local pattern_start = 1
if #KEYS == 0 then
  local batch = redis.call("SCAN", ARGV[1], "COUNT", ARGV[2])
  cursor = batch[1]
  keys = batch[2]
  pattern_start = 3
end
for i=pattern_start,#ARGV do
  local status, err = pcall(string.find, " ", ARGV[i])
  if not status then
    error(err .. ARGV[i])
  end
end
for _,key in ipairs(keys) do
  local usage = 0
  local reply = redis.pcall("MEMORY", "USAGE", key)
  if type(reply) == "number" then
    usage = reply
  end
  local group = nil
  for i=pattern_start,#ARGV do
    local key_match_result = {string.find(key, ARGV[i])}
    if key_match_result[1] ~= nil then
      group = table.concat({unpack(key_match_result, 3, #key_match_result)}, "")
      break
    end
  end
  if group == nil then
    group = "unclassified"
  end
  local value = groups[group]
  if value == nil then
    groups[group] = {1, usage}
  else
    groups[group] = {value[1] + 1, value[2] + usage}
  end
end
for group,value in pairs(groups) do
  result[#result+1] = {group, value[1], value[2]}
end
return {cursor, result}`

func gatherKeyGroupMetrics(c redis.Conn, batchSize int64, keyGroups []string) (map[string]*keyGroupMetrics, error) {
	allGroups := make(map[string]*keyGroupMetrics)
	keysAndArgs := []any{0, batchSize}
	for _, keyGroup := range keyGroups {
		keysAndArgs = append(keysAndArgs, keyGroup)
	}

	script := redis.NewScript(0, keyGroupScript)

	for {
		arr, err := redis.Values(script.Do(c, keysAndArgs...))
		if err != nil {
			return nil, err
		}

		if len(arr) != 2 {
			return nil, fmt.Errorf("invalid response from key group metrics lua script for groups: %s", strings.Join(keyGroups, ", "))
		}

		groups, err := redis.Values(arr[1], nil)
		if err != nil {
			return nil, err
		}
		if err := mergeKeyGroupMetrics(allGroups, groups); err != nil {
			return nil, err
		}
		cursor, err := redis.Int(arr[0], nil)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor from key group metrics lua script: %w", err)
		}
		keysAndArgs[0] = cursor
		if cursor == 0 {
			break
		}
	}
	return allGroups, nil
}

func gatherClusterKeyGroupMetrics(c redis.Conn, batchSize int64, keyGroups []string) (map[string]*keyGroupMetrics, error) {
	allGroups := make(map[string]*keyGroupMetrics)
	err := scanKeyBatches(c, "*", batchSize, func(batch []any) error {
		keys, err := redis.Strings(batch, nil)
		if err != nil {
			return err
		}
		for _, keysInSlot := range redisc.SplitBySlot(keys...) {
			script := redis.NewScript(len(keysInSlot), keyGroupScript)
			args := redis.Args{}.AddFlat(keysInSlot).AddFlat(keyGroups)
			result, err := redis.Values(script.Do(c, args...))
			if err != nil {
				return err
			}
			if len(result) != 2 {
				return fmt.Errorf("invalid response from cluster key group metrics lua script")
			}
			groups, err := redis.Values(result[1], nil)
			if err != nil {
				return err
			}
			if err := mergeKeyGroupMetrics(allGroups, groups); err != nil {
				return err
			}
		}
		return nil
	})
	return allGroups, err
}

func mergeKeyGroupMetrics(allGroups map[string]*keyGroupMetrics, groups []any) error {
	for _, group := range groups {
		metrics, err := redis.Values(group, nil)
		if err != nil || len(metrics) != 3 {
			return fmt.Errorf("invalid key group metrics response")
		}
		name, err := redis.String(metrics[0], nil)
		if err != nil {
			return fmt.Errorf("invalid key group name: %w", err)
		}
		count, err := redis.Int64(metrics[1], nil)
		if err != nil {
			return fmt.Errorf("invalid key group count: %w", err)
		}
		memoryUsage, err := redis.Int64(metrics[2], nil)
		if err != nil {
			return fmt.Errorf("invalid key group memory usage: %w", err)
		}

		if current, ok := allGroups[name]; ok {
			current.count += count
			current.memoryUsage += memoryUsage
		} else {
			allGroups[name] = &keyGroupMetrics{
				keyGroup:    name,
				count:       count,
				memoryUsage: memoryUsage,
			}
		}
	}
	return nil
}
