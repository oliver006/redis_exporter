package exporter

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

func TestFalkorDB(t *testing.T) {
	if os.Getenv("TEST_FALKORDB_URI") == "" {
		t.Skipf("TEST_FALKORDB_URI not set - skipping")
	}

	tsts := []struct {
		addr                string
		isFalkorDB          bool
		wantFalkorDBMetrics bool
	}{
		{addr: os.Getenv("TEST_FALKORDB_URI"), isFalkorDB: true, wantFalkorDBMetrics: true},
		{addr: os.Getenv("TEST_FALKORDB_URI"), isFalkorDB: false, wantFalkorDBMetrics: false},
	}

	for _, tst := range tsts {
		e, err := NewRedisExporter(tst.addr, Options{Namespace: "test", IsFalkorDB: tst.isFalkorDB})
		if err != nil {
			t.Fatalf("NewRedisExporter() err: %s", err)
		}

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		wantedMetrics := map[string]bool{
			"falkordb_total_graph_count": false,
		}

		for m := range chM {
			for want := range wantedMetrics {
				if strings.Contains(m.Desc().String(), want) {
					wantedMetrics[want] = true
				}
			}
		}

		if tst.wantFalkorDBMetrics {
			for want, found := range wantedMetrics {
				if !found {
					t.Errorf("%s was *not* found in falkordb metrics but expected", want)
				}
			}
		} else if !tst.wantFalkorDBMetrics {
			for want, found := range wantedMetrics {
				if found {
					t.Errorf("%s was *found* in falkordb metrics but *not* expected", want)
				}
			}
		}
	}
}

func TestFalkorDBGraphMemory(t *testing.T) {
	if os.Getenv("TEST_FALKORDB_URI") == "" {
		t.Skipf("TEST_FALKORDB_URI not set - skipping")
	}

	addr := os.Getenv("TEST_FALKORDB_URI")

	// Seed a minimal graph fixture to ensure deterministic metrics
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("redis.DialURL() err: %s", err)
	}
	defer c.Close()

	const testGraph = "_exporter_test_graph"
	_, err = c.Do("GRAPH.QUERY", testGraph, "CREATE (:Node{val:1})-[:EDGE]->(:Node{val:2})")
	if err != nil {
		t.Skipf("GRAPH.QUERY not supported, skipping: %s", err)
	}
	t.Cleanup(func() {
		c.Do("GRAPH.DELETE", testGraph)
	})

	e, err := NewRedisExporter(addr, Options{
		Namespace:               "test",
		IsFalkorDB:              true,
		InclFalkorDBGraphMemory: true,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	wantedMetrics := map[string]bool{
		"falkordb_graph_memory_total_mb":              false,
		"falkordb_graph_label_matrices_mb":            false,
		"falkordb_graph_relation_matrices_mb":         false,
		"falkordb_graph_node_block_mb":                false,
		"falkordb_graph_unlabeled_node_attributes_mb": false,
		"falkordb_graph_edge_block_mb":                false,
		"falkordb_graph_indices_mb":                   false,
	}

	for m := range chM {
		for want := range wantedMetrics {
			if strings.Contains(m.Desc().String(), want) {
				wantedMetrics[want] = true
			}
		}
	}

	for want, found := range wantedMetrics {
		if !found {
			t.Errorf("%s was *not* found in falkordb graph memory metrics but expected", want)
		}
	}
}

func TestParseFlatMap(t *testing.T) {
	// Simulate the flat array structure: ["Airport", 35, "City", 12]
	input := []interface{}{
		[]byte("Airport"), int64(35),
		[]byte("City"), int64(12),
	}

	result := parseFlatMap(input)

	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result["Airport"] != 35 {
		t.Errorf("expected Airport=35, got %d", result["Airport"])
	}
	if result["City"] != 12 {
		t.Errorf("expected City=12, got %d", result["City"])
	}
}

func TestParseFlatMapEmpty(t *testing.T) {
	result := parseFlatMap([]interface{}{})
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestParseFlatMapNil(t *testing.T) {
	result := parseFlatMap(nil)
	if len(result) != 0 {
		t.Errorf("expected empty map for nil input, got %d entries", len(result))
	}
}

func TestParseFlatMapInvalidEntries(t *testing.T) {
	// Mix valid and invalid entries: non-string key, non-int64 value, odd length
	input := []interface{}{
		int64(999), int64(10), // key is not a string -> skip
		[]byte("Valid"), "not_an_int64", // value is not int64 -> skip
		[]byte("Good"), int64(42), // valid
		[]byte("Lonely"), // odd entry at end -> loop stops before out-of-bounds
	}

	result := parseFlatMap(input)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(result), result)
	}
	if result["Good"] != 42 {
		t.Errorf("expected Good=42, got %d", result["Good"])
	}
}

func TestEmitGraphMemoryMetrics(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:               "test",
		IsFalkorDB:              true,
		InclFalkorDBGraphMemory: true,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	results := []graphMemoryResult{
		{
			Graph:                  "flights",
			TotalGraphSzMB:         1086,
			LabelMatricesSzMB:      96,
			RelationMatricesSzMB:   64,
			NodeBlockSzMB:          120,
			NodeAttrsByLabel:       map[string]int64{"Airport": 35, "City": 12},
			UnlabeledNodeAttrsSzMB: 0,
			EdgeBlockSzMB:          54,
			EdgeAttrsByType:        map[string]int64{"ROUTE": 68},
			IndicesSzMB:            752,
		},
	}

	chM := make(chan prometheus.Metric, 100)
	e.emitGraphMemoryMetrics(chM, results)
	close(chM)

	wantedMetrics := map[string]bool{
		"falkordb_graph_memory_total_mb":              false,
		"falkordb_graph_label_matrices_mb":            false,
		"falkordb_graph_relation_matrices_mb":         false,
		"falkordb_graph_node_block_mb":                false,
		"falkordb_graph_node_attributes_mb":           false,
		"falkordb_graph_unlabeled_node_attributes_mb": false,
		"falkordb_graph_edge_block_mb":                false,
		"falkordb_graph_edge_attributes_mb":           false,
		"falkordb_graph_indices_mb":                   false,
	}

	for m := range chM {
		for want := range wantedMetrics {
			if strings.Contains(m.Desc().String(), want) {
				wantedMetrics[want] = true
			}
		}
	}

	for want, found := range wantedMetrics {
		if !found {
			t.Errorf("%s was *not* found in emitted metrics but expected", want)
		}
	}
}

func TestEmitGraphMemoryMetricsExcludeAttrs(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:                       "test",
		IsFalkorDB:                      true,
		InclFalkorDBGraphMemory:         true,
		ExcludeFalkorDBGraphMemoryAttrs: true,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	results := []graphMemoryResult{
		{
			Graph:            "flights",
			TotalGraphSzMB:   1086,
			NodeAttrsByLabel: map[string]int64{"Airport": 35},
			EdgeAttrsByType:  map[string]int64{"ROUTE": 68},
		},
	}

	chM := make(chan prometheus.Metric, 100)
	e.emitGraphMemoryMetrics(chM, results)
	close(chM)

	foundTotal := false
	for m := range chM {
		desc := m.Desc().String()
		if strings.Contains(desc, "falkordb_graph_memory_total_mb") {
			foundTotal = true
		}
		if strings.Contains(desc, "falkordb_graph_node_attributes_mb") || strings.Contains(desc, "falkordb_graph_edge_attributes_mb") {
			t.Fatalf("did not expect attribute graph memory metric when attrs are excluded: %s", desc)
		}
	}
	if !foundTotal {
		t.Error("expected total graph memory metric")
	}
}

func TestGraphListMatches(t *testing.T) {
	tests := []struct {
		name      string
		cached    []graphMemoryResult
		graphList []interface{}
		want      bool
	}{
		{
			name:      "matching single graph",
			cached:    []graphMemoryResult{{Graph: "flights"}},
			graphList: []interface{}{[]byte("flights")},
			want:      true,
		},
		{
			name:      "matching multiple graphs",
			cached:    []graphMemoryResult{{Graph: "flights"}, {Graph: "social"}},
			graphList: []interface{}{[]byte("flights"), []byte("social")},
			want:      true,
		},
		{
			name:      "different lengths - cached longer",
			cached:    []graphMemoryResult{{Graph: "flights"}, {Graph: "social"}},
			graphList: []interface{}{[]byte("flights")},
			want:      false,
		},
		{
			name:      "different lengths - graphList longer",
			cached:    []graphMemoryResult{{Graph: "flights"}},
			graphList: []interface{}{[]byte("flights"), []byte("social")},
			want:      false,
		},
		{
			name:      "same length but different names",
			cached:    []graphMemoryResult{{Graph: "flights"}},
			graphList: []interface{}{[]byte("social")},
			want:      false,
		},
		{
			name:      "empty both",
			cached:    []graphMemoryResult{},
			graphList: []interface{}{},
			want:      true,
		},
		{
			name:      "nil cached",
			cached:    nil,
			graphList: []interface{}{},
			want:      true,
		},
		{
			name:      "invalid graphList entry",
			cached:    []graphMemoryResult{{Graph: "flights"}},
			graphList: []interface{}{int64(123)},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphListMatches(tt.cached, tt.graphList)
			if got != tt.want {
				t.Errorf("graphListMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGraphMemoryCacheBehavior(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:                   "test",
		IsFalkorDB:                  true,
		InclFalkorDBGraphMemory:     true,
		FalkorDBGraphMemoryCacheTTL: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	// Pre-populate cache
	cachedResults := []graphMemoryResult{
		{
			Graph:            "cached_graph",
			TotalGraphSzMB:   500,
			NodeAttrsByLabel: map[string]int64{},
			EdgeAttrsByType:  map[string]int64{},
		},
	}
	e.graphMemoryCache = cachedResults
	e.graphMemoryCacheTime = time.Now()

	// Emit from cache when graphList matches
	graphList := []interface{}{[]byte("cached_graph")}
	chM := make(chan prometheus.Metric, 50)
	e.extractFalkorDBGraphMemoryMetrics(chM, nil, graphList)
	close(chM)

	found := false
	for m := range chM {
		if strings.Contains(m.Desc().String(), "falkordb_graph_memory_total_mb") {
			found = true
		}
	}
	if !found {
		t.Error("expected cached metrics to be emitted")
	}
}

func TestGraphMemoryCacheInvalidatedOnGraphListChange(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:                   "test",
		IsFalkorDB:                  true,
		InclFalkorDBGraphMemory:     true,
		FalkorDBGraphMemoryCacheTTL: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	// Pre-populate cache with one graph
	e.graphMemoryCache = []graphMemoryResult{
		{Graph: "old_graph", TotalGraphSzMB: 100, NodeAttrsByLabel: map[string]int64{}, EdgeAttrsByType: map[string]int64{}},
	}
	e.graphMemoryCacheTime = time.Now()

	// Verify cache is NOT served when graph list differs
	differentGraphList := []interface{}{[]byte("new_graph")}
	if graphListMatches(e.graphMemoryCache, differentGraphList) {
		t.Error("graphListMatches should return false for different graph lists")
	}

	// Verify cache IS served when graph list matches
	matchingGraphList := []interface{}{[]byte("old_graph")}
	if !graphListMatches(e.graphMemoryCache, matchingGraphList) {
		t.Error("graphListMatches should return true for matching graph lists")
	}

	// Test actual cache hit path
	chM := make(chan prometheus.Metric, 50)
	e.extractFalkorDBGraphMemoryMetrics(chM, nil, matchingGraphList)
	close(chM)

	found := false
	for m := range chM {
		if strings.Contains(m.Desc().String(), "falkordb_graph_memory_total_mb") {
			found = true
		}
	}
	if !found {
		t.Error("expected cached metrics to be emitted when graph list matches")
	}
}

func TestGraphMemoryCacheDisabledClearsCachedResults(t *testing.T) {
	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:               "test",
		IsFalkorDB:              true,
		InclFalkorDBGraphMemory: true,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	e.graphMemoryCache = []graphMemoryResult{
		{Graph: "old_graph", TotalGraphSzMB: 100, NodeAttrsByLabel: map[string]int64{}, EdgeAttrsByType: map[string]int64{}},
	}
	e.graphMemoryCacheTime = time.Now()

	chM := make(chan prometheus.Metric, 50)
	e.extractFalkorDBGraphMemoryMetrics(chM, nil, nil)
	close(chM)

	if e.graphMemoryCache != nil {
		t.Error("expected disabled cache to clear cached graph memory results")
	}
	if !e.graphMemoryCacheTime.IsZero() {
		t.Error("expected disabled cache to clear cached graph memory timestamp")
	}
	for m := range chM {
		if strings.Contains(m.Desc().String(), "falkordb_graph_memory_total_mb") {
			t.Error("expected disabled cache not to emit stale cached graph memory metrics")
		}
	}
}

func TestGraphMemoryMaxGraphsLimit(t *testing.T) {
	const graphCount = 5
	const maxGraphs = 2

	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:                    "test",
		IsFalkorDB:                   true,
		InclFalkorDBGraphMemory:      true,
		MaxFalkorDBGraphMemoryGraphs: maxGraphs,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	graphList := make([]interface{}, graphCount)
	for i := range graphList {
		graphList[i] = []byte(fmt.Sprintf("graph_%05d", i))
	}

	metricCount := collectGraphMemoryMetrics(e, newFakeFalkorDBMemoryConn(0), graphList)
	expectedMetricCount := maxGraphs * 7
	if metricCount != expectedMetricCount {
		t.Fatalf("emitted %d metrics, expected %d", metricCount, expectedMetricCount)
	}
}

func TestGraphMemoryCacheDisabledStressMemoryCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping FalkorDB graph memory stress test in short mode")
	}

	const (
		graphCount            = 5000
		attrCount             = 24
		iterations            = 3
		retainedHeapAllowance = 12 * 1024 * 1024
		plateauAllowance      = 4 * 1024 * 1024
	)

	e, err := NewRedisExporter("redis://localhost:6379", Options{
		Namespace:               "test",
		IsFalkorDB:              true,
		InclFalkorDBGraphMemory: true,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	graphList := make([]interface{}, graphCount)
	for i := range graphList {
		graphList[i] = []byte(fmt.Sprintf("graph_%05d", i))
	}
	c := newFakeFalkorDBMemoryConn(attrCount)

	baseline := retainedHeapAlloc()
	var ceiling uint64
	for i := 0; i < iterations; i++ {
		metricCount := collectGraphMemoryMetrics(e, c, graphList)
		expectedMetricCount := graphCount * (7 + attrCount*2)
		if metricCount != expectedMetricCount {
			t.Fatalf("iteration %d emitted %d metrics, expected %d", i, metricCount, expectedMetricCount)
		}
		if e.graphMemoryCache != nil {
			t.Fatalf("iteration %d retained %d cached graph memory results with cache disabled", i, len(e.graphMemoryCache))
		}

		after := retainedHeapAlloc()
		if after > baseline+retainedHeapAllowance {
			t.Fatalf("iteration %d retained heap grew by %d bytes, expected <= %d bytes", i, after-baseline, retainedHeapAllowance)
		}
		if i == 0 {
			ceiling = after
			continue
		}
		if after > ceiling+plateauAllowance {
			t.Fatalf("iteration %d retained heap exceeded ceiling by %d bytes, expected <= %d bytes", i, after-ceiling, plateauAllowance)
		}
	}
	t.Logf("retained heap stayed below ceiling: baseline=%d ceiling=%d allowance=%d", baseline, ceiling, retainedHeapAllowance)
}

func BenchmarkGraphMemoryCacheDisabledAllocations(b *testing.B) {
	const graphCount = 1000
	tests := []struct {
		name         string
		attrCount    int
		excludeAttrs bool
	}{
		{name: "attrs_0", attrCount: 0},
		{name: "attrs_1", attrCount: 1},
		{name: "attrs_8", attrCount: 8},
		{name: "attrs_24", attrCount: 24},
		{name: "attrs_24_excluded", attrCount: 24, excludeAttrs: true},
	}

	graphList := make([]interface{}, graphCount)
	for i := range graphList {
		graphList[i] = []byte(fmt.Sprintf("graph_%05d", i))
	}

	for _, tt := range tests {
		b.Run(fmt.Sprintf("graphs_%d_%s", graphCount, tt.name), func(b *testing.B) {
			e, err := NewRedisExporter("redis://localhost:6379", Options{
				Namespace:                       "test",
				IsFalkorDB:                      true,
				InclFalkorDBGraphMemory:         true,
				ExcludeFalkorDBGraphMemoryAttrs: tt.excludeAttrs,
			})
			if err != nil {
				b.Fatalf("NewRedisExporter() err: %s", err)
			}
			c := newFakeFalkorDBMemoryConn(tt.attrCount)
			expectedMetricCount := graphCount * 7
			if !tt.excludeAttrs {
				expectedMetricCount = graphCount * (7 + tt.attrCount*2)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				metricCount := collectGraphMemoryMetrics(e, c, graphList)
				if metricCount != expectedMetricCount {
					b.Fatalf("emitted %d metrics, expected %d", metricCount, expectedMetricCount)
				}
			}
		})
	}
}

func collectGraphMemoryMetrics(e *Exporter, c redis.Conn, graphList []interface{}) int {
	chM := make(chan prometheus.Metric, 1024)
	done := make(chan int, 1)
	go func() {
		count := 0
		for range chM {
			count++
		}
		done <- count
	}()

	e.extractFalkorDBGraphMemoryMetrics(chM, c, graphList)
	close(chM)
	return <-done
}

func retainedHeapAlloc() uint64 {
	runtime.GC()
	runtime.GC()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	return memStats.HeapAlloc
}

type fakeFalkorDBMemoryConn struct {
	nodeAttrs []interface{}
	edgeAttrs []interface{}
}

func newFakeFalkorDBMemoryConn(attrCount int) *fakeFalkorDBMemoryConn {
	c := &fakeFalkorDBMemoryConn{
		nodeAttrs: make([]interface{}, 0, attrCount*2),
		edgeAttrs: make([]interface{}, 0, attrCount*2),
	}
	for i := 0; i < attrCount; i++ {
		c.nodeAttrs = append(c.nodeAttrs, []byte(fmt.Sprintf("Label_%02d", i)), int64(i+1))
		c.edgeAttrs = append(c.edgeAttrs, []byte(fmt.Sprintf("Relation_%02d", i)), int64(i+1))
	}
	return c
}

func (c *fakeFalkorDBMemoryConn) Close() error { return nil }

func (c *fakeFalkorDBMemoryConn) Err() error { return nil }

func (c *fakeFalkorDBMemoryConn) Do(commandName string, args ...interface{}) (interface{}, error) {
	if commandName != "GRAPH.MEMORY" || len(args) != 2 || args[0] != "USAGE" {
		return nil, fmt.Errorf("unexpected command %s %v", commandName, args)
	}

	return []interface{}{
		[]byte("total_graph_sz_mb"), int64(1086),
		[]byte("label_matrices_sz_mb"), int64(96),
		[]byte("relation_matrices_sz_mb"), int64(64),
		[]byte("amortized_node_block_sz_mb"), int64(120),
		[]byte("amortized_node_attributes_by_label_sz_mb"), c.nodeAttrs,
		[]byte("amortized_unlabeled_nodes_attributes_sz_mb"), int64(0),
		[]byte("amortized_edge_block_sz_mb"), int64(54),
		[]byte("amortized_edge_attributes_by_type_sz_mb"), c.edgeAttrs,
		[]byte("indices_sz_mb"), int64(752),
	}, nil
}

func (c *fakeFalkorDBMemoryConn) Send(commandName string, args ...interface{}) error { return nil }

func (c *fakeFalkorDBMemoryConn) Flush() error { return nil }

func (c *fakeFalkorDBMemoryConn) Receive() (interface{}, error) { return nil, nil }

// fakeFalkorDBFullConn handles both GRAPH.LIST and GRAPH.MEMORY commands.
type fakeFalkorDBFullConn struct {
	graphs    []interface{}
	memoryErr error
}

func (c *fakeFalkorDBFullConn) Close() error { return nil }

func (c *fakeFalkorDBFullConn) Err() error { return nil }

func (c *fakeFalkorDBFullConn) Do(commandName string, args ...interface{}) (interface{}, error) {
	if commandName == "GRAPH.LIST" {
		return c.graphs, nil
	}
	if commandName == "GRAPH.MEMORY" {
		if c.memoryErr != nil {
			return nil, c.memoryErr
		}
		return []interface{}{
			[]byte("total_graph_sz_mb"), int64(100),
		}, nil
	}
	return nil, fmt.Errorf("unexpected command %s %v", commandName, args)
}

func (c *fakeFalkorDBFullConn) Send(commandName string, args ...interface{}) error { return nil }

func (c *fakeFalkorDBFullConn) Flush() error { return nil }

func (c *fakeFalkorDBFullConn) Receive() (interface{}, error) { return nil, nil }

func TestExtractFalkorDBMetricsSkipsMemoryForReplica(t *testing.T) {
	for _, tst := range []struct {
		name                    string
		role                    string
		wantGraphMemoryMetrics  bool
	}{
		{name: "master_emits_graph_memory", role: "master", wantGraphMemoryMetrics: true},
		{name: "slave_skips_graph_memory", role: InstanceRoleSlave, wantGraphMemoryMetrics: false},
	} {
		t.Run(tst.name, func(t *testing.T) {
			e, err := NewRedisExporter("redis://localhost:6379", Options{
				Namespace:               "test",
				IsFalkorDB:              true,
				InclFalkorDBGraphMemory: true,
			})
			if err != nil {
				t.Fatalf("NewRedisExporter() err: %s", err)
			}

			c := &fakeFalkorDBFullConn{
				graphs: []interface{}{[]byte("test_graph")},
			}

			chM := make(chan prometheus.Metric, 50)
			e.extractFalkorDBMetrics(chM, c, tst.role)
			close(chM)

			foundGraphCount := false
			foundGraphMemory := false
			for m := range chM {
				desc := m.Desc().String()
				if strings.Contains(desc, "falkordb_total_graph_count") {
					foundGraphCount = true
				}
				if strings.Contains(desc, "falkordb_graph_memory_total_mb") {
					foundGraphMemory = true
				}
			}

			if !foundGraphCount {
				t.Error("expected falkordb_total_graph_count metric to be emitted")
			}
			if tst.wantGraphMemoryMetrics && !foundGraphMemory {
				t.Error("expected falkordb_graph_memory_total_mb metric to be emitted for master, but it was not")
			}
			if !tst.wantGraphMemoryMetrics && foundGraphMemory {
				t.Error("expected falkordb_graph_memory_total_mb metric to be skipped for replica, but it was emitted")
			}
		})
	}
}
