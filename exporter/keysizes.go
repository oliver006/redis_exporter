package exporter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var keysizesFieldRE = regexp.MustCompile(`^db([0-9]+)_distrib_([a-z]+)_(items|sizes)$`)

// Binary prefixes used by the KEYSIZES label array in redis/src/server.c
// (expSizeLabels): every label is a power of two, so "1K" means 1024.
var keysizesBucketSuffixes = map[byte]uint64{
	'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30,
	'T': 1 << 40, 'P': 1 << 50, 'E': 1 << 60,
}

// parseKeysizesBucket converts a KEYSIZES histogram bucket label like "64",
// "1K" or "2M" to its numeric value. Labels beyond the uint64 range are
// rejected rather than silently wrapped.
func parseKeysizesBucket(label string) (uint64, error) {
	mult := uint64(1)
	if len(label) > 0 {
		if m, ok := keysizesBucketSuffixes[label[len(label)-1]]; ok {
			mult = m
			label = label[:len(label)-1]
		}
	}
	n, err := strconv.ParseUint(label, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid keysizes bucket label %q: %w", label, err)
	}
	if n != 0 && n > (^uint64(0))/mult {
		return 0, fmt.Errorf("keysizes bucket label %q overflows uint64", label)
	}
	return n * mult, nil
}

func (e *Exporter) handleMetricsKeysizes(ch chan<- prometheus.Metric, fieldKey, fieldValue string) {
	/*
		Format ("# Keysizes" section, Redis 8.0+), buckets elided for brevity:
			db0_distrib_strings_sizes:0=7336,1=343127,64=480263,1K=52279,8M=1
			db0_distrib_hashes_items:1=4834,16=257,16K=3

		Each entry counts the keys whose value size (for "sizes", in bytes) or
		element count (for "items") falls into the power-of-two range starting
		at the bucket label, i.e. [bucket, bucket*2). The special bucket "0"
		counts zero-length values. (See updateKeysizesHist() in redis/src/db.c:
		bin = floor(log2(len)) + 1, label = 2^(bin-1).)
	*/
	matches := keysizesFieldRE.FindStringSubmatch(fieldKey)
	if matches == nil {
		return
	}
	dbName := "db" + matches[1]
	keyType := matches[2]
	measure := matches[3]

	for field := range strings.SplitSeq(fieldValue, ",") {
		if field == "" {
			continue
		}
		label, value, ok := strings.Cut(field, "=")
		if !ok {
			log.Debugf("invalid keysizes field %q", field)
			continue
		}
		bucket, err := parseKeysizesBucket(label)
		if err != nil {
			log.Debugf("couldn't parse %s, err: %s", field, err)
			continue
		}
		count, err := strconv.ParseFloat(value, 64)
		if err != nil {
			log.Debugf("couldn't parse %s, err: %s", field, err)
			continue
		}
		e.registerConstMetricGauge(ch, "keysizes_key_count", count,
			dbName, keyType, measure, strconv.FormatUint(bucket, 10))
	}
}
