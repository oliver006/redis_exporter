{
  prometheusAlerts+:: {
    groups+: [
      {
        name: 'redis',
        rules: [
          {
            alert: 'RedisDown',
            expr: 'redis_up == 0',
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
            expr: 'redis_memory_used_bytes / redis_total_system_memory_bytes * 100 > 90',
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
            expr: 'redis_connected_clients > 100',
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Redis too many connections (instance {{ $labels.instance }})',
              description: 'Redis instance has too many connections\n  VALUE = {{ $value }}\n  LABELS: {{ $labels }}',
            },
          },
        ],
      },
    ],
  },
}
