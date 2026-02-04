# Redis Exporter - Project Summary

## Overview
Prometheus exporter for Redis, Sentinel, Cluster, Dragonfly, Valkey, and Tile38. Written in Go using `gomodule/redigo` for Redis and `prometheus/client_golang` for metrics.

## Architecture

**Entry point:** `main.go` → creates `Exporter` struct → registers with Prometheus → starts HTTP server.

**Scrape flow:** HTTP request → `Collect()` → `scrapeRedisHost()` → connect → `CONFIG GET` → `INFO ALL` → feature-specific commands → register metrics.

**Key struct:** `Exporter` implements `prometheus.Collector` (Describe/Collect) and `http.Handler`.

## Exporter Directory Structure

| File | Purpose |
|------|---------|
| `exporter.go` | Core: Exporter struct, Options, Collect(), scrapeRedisHost(), metric registration |
| `redis.go` | Connection helpers: connectToRedis(), connectToRedisCluster(), configureOptions() |
| `info.go` | INFO ALL parsing: extractInfoMetrics(), extractConfigMetrics(), commandstats, latencystats |
| `keys.go` | Key inspection: check-keys, check-single-keys, count-keys, pipelined batch operations |
| `streams.go` | XINFO STREAM/GROUPS/CONSUMERS metrics |
| `key_groups.go` | SCAN-based key grouping with memory analysis |
| `clients.go` | CLIENT LIST parsing with IPv4/IPv6 support |
| `sentinels.go` | Sentinel master/slave/sentinel metrics, ckquorum |
| `metrics.go` | Metric description helpers, counter/gauge map definitions |
| `http.go` | HTTP handlers: /metrics, /scrape, /health, /-/reload, /discover-cluster-nodes |
| `tls.go` | TLS config: client certs, CA loading, skip verification |
| `pwd_file.go` | JSON password file loading (URI→password map) |
| `nodes.go` | Cluster node discovery from CLUSTER INFO |
| `modules.go` | Redis module metrics from INFO MODULES |
| `search_indexes.go` | RediSearch FT._LIST / FT.INFO metrics |
| `slowlog.go` | SLOWLOG GET metrics |
| `latency.go` | LATENCY LATEST and LATENCY HISTOGRAM |
| `lua.go` | Custom Lua script execution via EVAL |
| `tile38.go` | Tile38-specific metrics |

## Key Configuration (Options struct)

- **Auth:** User/Password, PasswordMap, RedisPwdFile, HTTP BasicAuth (bcrypt support)
- **TLS:** ClientCert/Key, CACert, SkipTLSVerification
- **Metric toggles:** InclConfigMetrics, InclModulesMetrics, InclSearchIndexesMetrics, InclSystemMetrics, ExportClientList
- **Key inspection:** CheckKeys, CheckSingleKeys, CheckStreams, CountKeys, CheckKeyGroups (with MaxDistinctKeyGroups)
- **Performance:** CheckKeysBatchSize (pipelining), ConnectionTimeouts, SkipCheckKeysForRoleMaster
- **DB type:** IsCluster, IsTile38; Valkey/Dragonfly auto-detected via URL scheme

## HTTP Endpoints

- `/metrics` - Standard Prometheus scrape
- `/scrape?target=<uri>` - Dynamic single-target scraping (supports query param overrides)
- `/health` - Health check
- `/-/reload` - Reload password file
- `/discover-cluster-nodes` - JSON cluster node list for Prometheus relabeling

## Notable Design Decisions

- **Pipelining:** Batched key operations for non-cluster; sequential for cluster mode
- **Role-aware:** Detects master/slave from INFO; can skip key checks on master
- **Keyspace parsing:** Supports 4-6 fields for Redis/Dragonfly/Valkey compatibility
- **URL normalization:** `valkey://` → `redis://`, `valkeys://` → `rediss://`
- **Error resilience:** Individual metric errors don't abort the scrape
- **Metric registration:** Two-phase (descriptions at init, values at scrape) with dynamic metrics for command stats, key names, etc.

## Testing

19 test files. Integration tests use `TEST_REDIS_URI` env var for live Redis instances. Covers parsing, HTTP handlers, cluster, sentinel, streams, key groups, search indexes, and client list (IPv6, RESP versions).

## Build & Run

```bash
go build .
./redis_exporter --redis.addr=redis://localhost:6379
```

Key flags: `--redis.addr`, `--redis.password`, `--check-keys`, `--check-streams`, `--is-cluster`, `--tls-client-cert-file`, `--lua-script`, `--namespace`
