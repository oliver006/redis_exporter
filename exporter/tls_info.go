package exporter

import "github.com/prometheus/client_golang/prometheus"

func (e *Exporter) handleMetricsTLS(ch chan<- prometheus.Metric, fieldKey, fieldValue string) bool {
	var certificate string
	switch fieldKey {
	case "tls_server_cert_serial":
		certificate = "server"
	case "tls_client_cert_serial":
		certificate = "client"
	case "tls_ca_cert_serial":
		certificate = "ca"
	default:
		return false
	}

	e.registerConstMetricGauge(ch, "tls_certificate_info", 1, certificate, fieldValue)
	return true
}
