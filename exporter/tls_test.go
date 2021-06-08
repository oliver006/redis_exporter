package exporter

import (
	"testing"
)

func TestCreateClientTLSConfig(t *testing.T) {
	for _, test := range []struct {
		name          string
		options       Options
		expectSuccess bool
	}{
		// positive tests
		{"no_options", Options{}, true},
		{"skip_verificaton", Options{
			SkipTLSVerification: true}, true},
		{"load_client_keypair", Options{
			ClientCertFile: "../contrib/tls/redis.crt",
			ClientKeyFile:  "../contrib/tls/redis.key"}, true},
		{"load_ca_cert", Options{
			CaCertFile: "../contrib/tls/ca.crt"}, true},

		// negative tests
		{"nonexisting_client_files", Options{
			ClientCertFile: "/nonexisting/file",
			ClientKeyFile:  "/nonexisting/file"}, false},
		{"nonexisting_ca_file", Options{
			CaCertFile: "/nonexisting/file"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := getTestExporterWithOptions(test.options)

			_, err := e.CreateClientTLSConfig()
			if test.expectSuccess && err != nil {
				t.Errorf("Expected success for test: %s, got err: %s", test.name, err)
				return
			}
		})
	}
}

func TestGetServerCertificateFunc(t *testing.T) {
	// positive test
	_, err := GetServerCertificateFunc("../contrib/tls/ca.crt", "../contrib/tls/ca.key")(nil)
	if err != nil {
		t.Errorf("GetServerCertificateFunc() err: %s", err)
	}

	// negative test
	_, err = GetServerCertificateFunc("/nonexisting/file", "/nonexisting/file")(nil)
	if err == nil {
		t.Errorf("Expected GetServerCertificateFunc() to fail")
	}
}
