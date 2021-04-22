package exporter

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
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
	for db := 0; db < dbCount; db++ {
		if _, err := doRedisCmd(c, "SELECT", db); err != nil {
			log.Errorf("Couldn't select database %d when getting key info.", db)
			continue
		}
		allGroups, err := gatherKeyGroupMetrics(c, e.options.CheckKeysBatchSize, keyGroupsNoEmptyStrings)
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

func gatherKeyGroupMetrics(c redis.Conn, batchSize int64, keyGroups []string) (map[string]*keyGroupMetrics, error) {
	allGroups := make(map[string]*keyGroupMetrics)
	keysAndArgs := []interface{}{0, batchSize}
	for _, keyGroup := range keyGroups {
		keysAndArgs = append(keysAndArgs, keyGroup)
	}

	script := redis.NewScript(
		0,
		`
local result = {}
local batch = redis.call("SCAN", ARGV[1], "COUNT", ARGV[2])
local groups = {}
local usage = 0
local group_index = 0
local group = nil
local value = {}
local key_match_result = {}
local status = false
local err = nil
for i=3,#ARGV do
  status, err = pcall(string.find, " ", ARGV[i])
  if not status then
    error(err .. ARGV[i])
  end
end
for i,key in ipairs(batch[2]) do
  usage = redis.call("MEMORY", "USAGE", key)
  group = nil
  for i=3,#ARGV do
    key_match_result = {string.find(key, ARGV[i])}
    if key_match_result[1] ~= nil then
      group = table.concat({unpack(key_match_result, 3,  #key_match_result)},  "")
      break
    end
  end
  if group == nil then
     group = "unclassified"
  end
  value = groups[group]
  if value == nil then
     groups[group] = {1, usage}
  else
     groups[group] = {value[1] + 1, value[2] + usage}
  end
end
for group,value in pairs(groups) do
  result[#result+1] = {group, value[1], value[2]}
end
return {batch[1], result}`,
	)

	for {
		arr, err := redis.Values(script.Do(c, keysAndArgs...))
		if err != nil {
			return nil, err
		}

		if len(arr) != 2 {
			return nil, fmt.Errorf("invalid response from key group metrics lua script for groups: %s", strings.Join(keyGroups, ", "))
		}

		groups, _ := redis.Values(arr[1], nil)

		for _, group := range groups {
			metricsArr, _ := redis.Values(group, nil)
			name, _ := redis.String(metricsArr[0], nil)
			count, _ := redis.Int64(metricsArr[1], nil)
			memoryUsage, _ := redis.Int64(metricsArr[2], nil)

			if currentMetrics, ok := allGroups[name]; ok {
				currentMetrics.count += count
				currentMetrics.memoryUsage += memoryUsage
			} else {
				allGroups[name] = &keyGroupMetrics{
					keyGroup:    name,
					count:       count,
					memoryUsage: memoryUsage,
				}
			}

		}
		if keysAndArgs[0], _ = redis.Int(arr[0], nil); keysAndArgs[0].(int) == 0 {
			break
		}
	}
	return allGroups, nil
}
