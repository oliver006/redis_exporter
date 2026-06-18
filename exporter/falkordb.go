package exporter

import (
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type graphMemoryResult struct {
	Graph                  string
	TotalGraphSzMB         int64
	LabelMatricesSzMB      int64
	RelationMatricesSzMB   int64
	NodeBlockSzMB          int64
	NodeAttrsByLabel       map[string]int64
	UnlabeledNodeAttrsSzMB int64
	EdgeBlockSzMB          int64
	EdgeAttrsByType        map[string]int64
	IndicesSzMB            int64
}

const defaultMaxFalkorDBGraphMemoryGraphs int64 = 10000

func (e *Exporter) extractFalkorDBMetrics(ch chan<- prometheus.Metric, c redis.Conn, role string) {
	graphList, err := redis.Values(doRedisCmd(c, "GRAPH.LIST"))
	if err != nil {
		log.Errorf("extractFalkorDBMetrics() err: %s", err)
		return
	}

	graphCount := len(graphList)
	e.registerConstMetricGauge(ch, "falkordb_total_graph_count", float64(graphCount))

	if !e.options.InclFalkorDBGraphMemory {
		return
	}

	if role == InstanceRoleSlave {
		log.Infof("skipping GRAPH.MEMORY metrics for replica, role: %s", role)
		return
	}

	e.extractFalkorDBGraphMemoryMetrics(ch, c, graphList)
}

// extractFalkorDBGraphMemoryMetrics collects GRAPH.MEMORY USAGE for each graph.
// Note: The cache lives on the Exporter instance. When using the /scrape endpoint
// (multi-target pattern), a fresh Exporter is created per request, so the cache
// will not persist across scrapes in that mode.
func (e *Exporter) extractFalkorDBGraphMemoryMetrics(ch chan<- prometheus.Metric, c redis.Conn, graphList []interface{}) {
	graphList = e.limitFalkorDBGraphMemoryGraphs(graphList)

	ttl := e.options.FalkorDBGraphMemoryCacheTTL
	cacheEnabled := ttl > 0

	// Use cached results if still valid and graph list hasn't changed.
	if cacheEnabled && e.graphMemoryCache != nil && time.Since(e.graphMemoryCacheTime) < ttl && graphListMatches(e.graphMemoryCache, graphList) {
		e.emitGraphMemoryMetrics(ch, e.graphMemoryCache)
		return
	}
	if !cacheEnabled {
		e.graphMemoryCache = nil
		e.graphMemoryCacheTime = time.Time{}
	}

	var results []graphMemoryResult
	if cacheEnabled {
		results = make([]graphMemoryResult, 0, len(graphList))
	}

	for _, g := range graphList {
		graphName, err := redis.String(g, nil)
		if err != nil {
			log.Warnf("extractFalkorDBGraphMemoryMetrics() couldn't parse graph name: %s", err)
			continue
		}
		if !cacheEnabled {
			if err := e.fetchAndEmitGraphMemory(ch, c, graphName); err != nil {
				log.Warnf("extractFalkorDBGraphMemoryMetrics() GRAPH.MEMORY USAGE %s err: %s", graphName, err)
			}
			continue
		}

		result, err := e.fetchGraphMemory(c, graphName)
		if err != nil {
			log.Warnf("extractFalkorDBGraphMemoryMetrics() GRAPH.MEMORY USAGE %s err: %s", graphName, err)
			continue
		}

		e.emitGraphMemoryMetric(ch, result)
		if cacheEnabled {
			results = append(results, result)
		}
	}

	if cacheEnabled {
		e.graphMemoryCache = results
		e.graphMemoryCacheTime = time.Now()
	}
}

func (e *Exporter) limitFalkorDBGraphMemoryGraphs(graphList []interface{}) []interface{} {
	maxGraphs := e.options.MaxFalkorDBGraphMemoryGraphs
	if maxGraphs == 0 {
		maxGraphs = defaultMaxFalkorDBGraphMemoryGraphs
	}
	if maxGraphs < 0 || int64(len(graphList)) <= maxGraphs {
		return graphList
	}

	log.Warnf("extractFalkorDBGraphMemoryMetrics() limiting GRAPH.MEMORY scrape to %d of %d graphs", maxGraphs, len(graphList))
	return graphList[:maxGraphs]
}

func (e *Exporter) fetchGraphMemory(c redis.Conn, graphName string) (graphMemoryResult, error) {
	vals, err := redis.Values(doRedisCmd(c, "GRAPH.MEMORY", "USAGE", graphName))
	if err != nil {
		return graphMemoryResult{}, err
	}

	result := graphMemoryResult{Graph: graphName}

	for i := 0; i+1 < len(vals); i += 2 {
		key, err := redis.String(vals[i], nil)
		if err != nil {
			continue
		}

		switch key {
		case "total_graph_sz_mb":
			result.TotalGraphSzMB, err = redis.Int64(vals[i+1], nil)
		case "label_matrices_sz_mb":
			result.LabelMatricesSzMB, err = redis.Int64(vals[i+1], nil)
		case "relation_matrices_sz_mb":
			result.RelationMatricesSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_node_block_sz_mb":
			result.NodeBlockSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_node_attributes_by_label_sz_mb":
			if !e.options.ExcludeFalkorDBGraphMemoryAttrs {
				result.NodeAttrsByLabel = parseFlatMap(vals[i+1])
			}
		case "amortized_unlabeled_nodes_attributes_sz_mb":
			result.UnlabeledNodeAttrsSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_edge_block_sz_mb":
			result.EdgeBlockSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_edge_attributes_by_type_sz_mb":
			if !e.options.ExcludeFalkorDBGraphMemoryAttrs {
				result.EdgeAttrsByType = parseFlatMap(vals[i+1])
			}
		case "indices_sz_mb":
			result.IndicesSzMB, err = redis.Int64(vals[i+1], nil)
		}
		if err != nil {
			log.Debugf("fetchGraphMemory() couldn't parse value for key %q in graph %q: %s", key, graphName, err)
		}
	}

	return result, nil
}

func (e *Exporter) fetchAndEmitGraphMemory(ch chan<- prometheus.Metric, c redis.Conn, graphName string) error {
	vals, err := redis.Values(doRedisCmd(c, "GRAPH.MEMORY", "USAGE", graphName))
	if err != nil {
		return err
	}

	var totalGraphSzMB int64
	var labelMatricesSzMB int64
	var relationMatricesSzMB int64
	var nodeBlockSzMB int64
	var unlabeledNodeAttrsSzMB int64
	var edgeBlockSzMB int64
	var indicesSzMB int64

	for i := 0; i+1 < len(vals); i += 2 {
		key, err := redis.String(vals[i], nil)
		if err != nil {
			continue
		}

		switch key {
		case "total_graph_sz_mb":
			totalGraphSzMB, err = redis.Int64(vals[i+1], nil)
		case "label_matrices_sz_mb":
			labelMatricesSzMB, err = redis.Int64(vals[i+1], nil)
		case "relation_matrices_sz_mb":
			relationMatricesSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_node_block_sz_mb":
			nodeBlockSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_node_attributes_by_label_sz_mb":
			if e.options.ExcludeFalkorDBGraphMemoryAttrs {
				continue
			}
			e.emitFlatMapGraphMemoryMetrics(ch, "falkordb_graph_node_attributes_mb", graphName, vals[i+1])
		case "amortized_unlabeled_nodes_attributes_sz_mb":
			unlabeledNodeAttrsSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_edge_block_sz_mb":
			edgeBlockSzMB, err = redis.Int64(vals[i+1], nil)
		case "amortized_edge_attributes_by_type_sz_mb":
			if e.options.ExcludeFalkorDBGraphMemoryAttrs {
				continue
			}
			e.emitFlatMapGraphMemoryMetrics(ch, "falkordb_graph_edge_attributes_mb", graphName, vals[i+1])
		case "indices_sz_mb":
			indicesSzMB, err = redis.Int64(vals[i+1], nil)
		}
		if err != nil {
			log.Debugf("fetchAndEmitGraphMemory() couldn't parse value for key %q in graph %q: %s", key, graphName, err)
		}
	}

	e.registerConstMetricGauge(ch, "falkordb_graph_memory_total_mb", float64(totalGraphSzMB), graphName)
	e.registerConstMetricGauge(ch, "falkordb_graph_label_matrices_mb", float64(labelMatricesSzMB), graphName)
	e.registerConstMetricGauge(ch, "falkordb_graph_relation_matrices_mb", float64(relationMatricesSzMB), graphName)
	e.registerConstMetricGauge(ch, "falkordb_graph_node_block_mb", float64(nodeBlockSzMB), graphName)
	e.registerConstMetricGauge(ch, "falkordb_graph_unlabeled_node_attributes_mb", float64(unlabeledNodeAttrsSzMB), graphName)
	e.registerConstMetricGauge(ch, "falkordb_graph_edge_block_mb", float64(edgeBlockSzMB), graphName)
	e.registerConstMetricGauge(ch, "falkordb_graph_indices_mb", float64(indicesSzMB), graphName)

	return nil
}

func (e *Exporter) emitFlatMapGraphMemoryMetrics(ch chan<- prometheus.Metric, metric string, graphName string, val interface{}) {
	items, err := redis.Values(val, nil)
	if err != nil {
		return
	}
	for i := 0; i+1 < len(items); i += 2 {
		name, err := redis.String(items[i], nil)
		if err != nil {
			continue
		}
		v, err := redis.Int64(items[i+1], nil)
		if err != nil {
			continue
		}
		e.registerConstMetricGauge(ch, metric, float64(v), graphName, name)
	}
}

// graphListMatches returns true if the cached results correspond to the same set of graphs.
func graphListMatches(cached []graphMemoryResult, graphList []interface{}) bool {
	if len(cached) != len(graphList) {
		return false
	}
	cachedNames := make(map[string]bool, len(cached))
	for _, r := range cached {
		cachedNames[r.Graph] = true
	}
	for _, g := range graphList {
		name, err := redis.String(g, nil)
		if err != nil || !cachedNames[name] {
			return false
		}
	}
	return true
}

func parseFlatMap(val interface{}) map[string]int64 {
	items, err := redis.Values(val, nil)
	if err != nil {
		return nil
	}
	m := make(map[string]int64, len(items)/2)
	for i := 0; i+1 < len(items); i += 2 {
		name, err := redis.String(items[i], nil)
		if err != nil {
			continue
		}
		v, err := redis.Int64(items[i+1], nil)
		if err != nil {
			continue
		}
		m[name] = v
	}
	return m
}

func (e *Exporter) emitGraphMemoryMetrics(ch chan<- prometheus.Metric, results []graphMemoryResult) {
	for _, r := range results {
		e.emitGraphMemoryMetric(ch, r)
	}
}

func (e *Exporter) emitGraphMemoryMetric(ch chan<- prometheus.Metric, r graphMemoryResult) {
	e.registerConstMetricGauge(ch, "falkordb_graph_memory_total_mb", float64(r.TotalGraphSzMB), r.Graph)
	e.registerConstMetricGauge(ch, "falkordb_graph_label_matrices_mb", float64(r.LabelMatricesSzMB), r.Graph)
	e.registerConstMetricGauge(ch, "falkordb_graph_relation_matrices_mb", float64(r.RelationMatricesSzMB), r.Graph)
	e.registerConstMetricGauge(ch, "falkordb_graph_node_block_mb", float64(r.NodeBlockSzMB), r.Graph)
	e.registerConstMetricGauge(ch, "falkordb_graph_unlabeled_node_attributes_mb", float64(r.UnlabeledNodeAttrsSzMB), r.Graph)
	e.registerConstMetricGauge(ch, "falkordb_graph_edge_block_mb", float64(r.EdgeBlockSzMB), r.Graph)
	e.registerConstMetricGauge(ch, "falkordb_graph_indices_mb", float64(r.IndicesSzMB), r.Graph)

	if e.options.ExcludeFalkorDBGraphMemoryAttrs {
		return
	}

	for label, val := range r.NodeAttrsByLabel {
		e.registerConstMetricGauge(ch, "falkordb_graph_node_attributes_mb", float64(val), r.Graph, label)
	}
	for relType, val := range r.EdgeAttrsByType {
		e.registerConstMetricGauge(ch, "falkordb_graph_edge_attributes_mb", float64(val), r.Graph, relType)
	}
}
