{
  grafanaDashboards: {
    'redis-overview.json': (import 'dashboards/redis-overview.json'),
  },

  // Helper function to ensure that we don't override other rules, by forcing
  // the patching of the groups list, and not the overall rules object.
  local importRules(rules) = {
    groups+: std.native('parseYaml')(rules)[0].groups,
  },

  prometheusRules+: importRules(importstr 'rules/redis.yaml'),

  prometheusAlerts+: importRules(importstr 'alerts/redis.yaml'),
}
