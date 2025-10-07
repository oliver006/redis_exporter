package exporter

import (
	"regexp"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// All fields of the streamInfo struct must be exported
// because of redis.ScanStruct (reflect) limitations
type searchIndexInfo struct {
	IndexName                string  `redis:"index_name"`
	NumDocs                  int64   `redis:"num_docs"`
	MaxDocId                 int64   `redis:"max_doc_id"`
	NumTerms                 int64   `redis:"num_terms"`
	NumRecords               int64   `redis:"num_records"`
	InvertedSizeMb           float64 `redis:"inverted_sz_mb"`
	TotalInvertedIndexBlocks int64   `redis:"total_inverted_index_blocks"`
	VectorIndexSizeMb        float64 `redis:"vector_index_sz_mb"`
	OffsetVectorsSizeMb      float64 `redis:"offset_vectors_sz_mb"`
	DocTableSizeMb           float64 `redis:"doc_table_size_mb"`
	SortableValuesSizeMb     float64 `redis:"sortable_values_size_mb"`
	KeyTableSizeMb           float64 `redis:"key_table_size_mb"`
	TagOverheadSizeMb        float64 `redis:"tag_overhead_sz_mb"`
	TextOverheadSizeMb       float64 `redis:"text_overhead_sz_mb"`
	TotalIndexMemorySizeMb   float64 `redis:"total_index_memory_sz_mb"`
	GeoshapesSizeMb          float64 `redis:"geoshapes_sz_mb"`
	RecordsPerDocAvg         float64 `redis:"records_per_doc_avg"`
	BytesPerRecordAvg        float64 `redis:"bytes_per_record_avg"`
	OffsetsPerTermAvg        float64 `redis:"offsets_per_term_avg"`
	OffsetBitsPerRecordAvg   float64 `redis:"offset_bits_per_record_avg"`
	Indexing                 int64   `redis:"indexing"`
	PercentIndexed           float64 `redis:"percent_indexed"`
	HashIndexingFailures     int64   `redis:"hash_indexing_failures"`
	NumberOfUses             int64   `redis:"number_of_uses"`
	Cleaning                 int64   `redis:"cleaning"`
}

func (e *Exporter) extractSearchIndexesMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	var searchIndexes []string
	allSearchIndexes, err := redis.Strings(doRedisCmd(c, "FT._LIST"))
	if err != nil {
		log.Errorf("extractSearchIndexesMetrics() err: %s", err)
		return
	}

	// Get indexes list based on check-search-indexes regex
	checkIndexRegex := regexp.MustCompile(e.options.CheckSearchIndexes)
	for _, index := range allSearchIndexes {
		if checkIndexRegex.MatchString(index) {
			searchIndexes = append(searchIndexes, index)
		}
	}

	for _, index := range searchIndexes {
		values, err := redis.Values(doRedisCmd(c, "FT.INFO", index))
		if err != nil {
			log.Errorf("extractSearchIndexesMetrics() err: %s", err)
			return
		}

		// Scan slice to struct
		var indexInfo searchIndexInfo
		if err := redis.ScanStruct(values, &indexInfo); err != nil {
			log.Errorf("Couldn't scan search index '%s': %s", index, err)
			continue
		}
		// Register search index metrics
		e.registerConstMetricGauge(ch, "search_index_num_docs", float64(indexInfo.NumDocs), indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_max_doc_id", float64(indexInfo.MaxDocId), indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_num_terms", float64(indexInfo.NumTerms), indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_num_records", float64(indexInfo.NumRecords), indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_inverted_size_bytes", indexInfo.InvertedSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_total_inverted_index_blocks", float64(indexInfo.TotalInvertedIndexBlocks), indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_vector_index_size_bytes", indexInfo.VectorIndexSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_offset_vectors_size_bytes", indexInfo.OffsetVectorsSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_doc_table_size_bytes", indexInfo.DocTableSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_sortable_values_size_bytes", indexInfo.SortableValuesSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_key_table_size_bytes", indexInfo.KeyTableSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_tag_overhead_size_bytes", indexInfo.TagOverheadSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_text_overhead_size_bytes", indexInfo.TextOverheadSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_total_index_memory_size_bytes", indexInfo.TotalIndexMemorySizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_geoshapes_size_bytes", indexInfo.GeoshapesSizeMb*1048576, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_records_per_doc_avg", indexInfo.RecordsPerDocAvg, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_bytes_per_record_avg", indexInfo.BytesPerRecordAvg, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_offsets_per_term_avg", indexInfo.OffsetsPerTermAvg, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_offset_bits_per_record_avg", indexInfo.OffsetBitsPerRecordAvg, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_indexing", float64(indexInfo.Indexing), indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_percent_indexed", indexInfo.PercentIndexed, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_hash_indexing_failures", float64(indexInfo.HashIndexingFailures), indexInfo.IndexName)
		e.registerConstMetric(ch, "search_index_number_of_uses_total", float64(indexInfo.NumberOfUses), prometheus.CounterValue, indexInfo.IndexName)
		e.registerConstMetricGauge(ch, "search_index_cleaning", float64(indexInfo.Cleaning), indexInfo.IndexName)
	}
}
