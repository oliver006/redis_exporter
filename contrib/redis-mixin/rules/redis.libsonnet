{
  prometheusRules+:: {
    groups+: [
      {
        name: 'redis.rules',
        rules: [
          {
            record: 'redis_memory_fragmentation_ratio',
            expr: 'redis_memory_used_rss_bytes / redis_memory_used_bytes',
          },
        ],
      },
    ],
  },
}
