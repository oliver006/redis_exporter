{
  prometheusAlerts+:: {
    groups+: [
      {
        name: 'redis',
        rules: [
          {
            alert: 'RedisDown',
            expr: 'redis_up{%(redisExporterSelector)s} == 0' % $._config,
            'for': '5m',
            labels: {
              severity: 'critical',
            },
            annotations: {
              summary: 'Redis down (instance {{ $labels.instance }})',
              description: 'Redis instance is down\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
          {
            alert: 'RedisOutOfMemory',
            expr: 'redis_memory_used_bytes{%(redisExporterSelector)s} / redis_total_system_memory_bytes{%(redisExporterSelector)s} * 100 > 90' % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Redis out of memory (instance {{ $labels.instance }})',
              description: 'Redis is running out of memory (> 90%)\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
          {
            alert: 'RedisTooManyConnections',
            expr: 'redis_connected_clients{%(redisExporterSelector)s} > %(redisConnectionsThreshold)s' % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Redis too many connections (instance {{ $labels.instance }})',
              description: 'Redis instance has too many connections\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
          {
            alert: 'RedisClusterSlotFail',
            expr: 'redis_cluster_slots_fail{%(redisExporterSelector)s} > 0' % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Number of hash slots mapping to a node in FAIL state (instance {{ $labels.instance }})',
              description: 'Redis cluster has slots fail\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
          {
            alert: 'RedisClusterSlotPfail',
            expr: 'redis_cluster_slots_pfail{%(redisExporterSelector)s} > 0' % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Number of hash slots mapping to a node in PFAIL state (instance {{ $labels.instance }})',
              description: 'Redis cluster has slots pfail\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
          {
            alert: 'RedisClusterStateNotOk',
            expr: 'redis_cluster_state{%(redisExporterSelector)s} == 0' % $._config,
            'for': '5m',
            labels: {
              severity: 'critical',
            },
            annotations: {
              summary: 'Redis cluster state is not ok (instance {{ $labels.instance }})',
              description: 'Redis cluster is not ok\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
        ],
      },
    ],
  },
}
