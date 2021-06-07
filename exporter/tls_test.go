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

		// negative tests
		{"nonexisting_client_files", Options{
			ClientCertFile: "/tmp2/0123456789.tmp",
			ClientKeyFile:  "/tmp2/0123456789.tmp"}, false},
		{"nonexisting_ca_file", Options{
			CaCertFile: "/tmp2/012345679.tmp"}, false},
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
