package exporter

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestParseClientListString(t *testing.T) {
	tsts := map[string][]string{
		"id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex":    []string{"", "6321", "6320", "N", "0", "0", "setex", "127.0.0.1", "63508"},
		"id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client": []string{"foo", "5", "0", "N", "1", "0", "client", "127.0.0.1", "64958"},
	}

	for k, v := range tsts {
		lbls, ok := parseClientListString(k)
		mismatch := false
		for idx, l := range lbls {
			if l != v[idx] {
				mismatch = true
				break
			}
		}
		if !ok || mismatch {
			t.Errorf("TestParseClientListString( %s ) error. Given: %s Wanted: %s", k, lbls, v)
		}
	}
}

func TestExportClientList(t *testing.T) {
	for _, isExportClientList := range []bool{true, false} {
		e := getTestExporterWithOptions(Options{
			Namespace: "test", Registry: prometheus.NewRegistry(),
			ExportClientList: isExportClientList,
		})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		found := false
		for m := range chM {
			if strings.Contains(m.Desc().String(), "connected_clients_details") {
				found = true
			}
		}

		if isExportClientList && !found {
			t.Errorf("connected_clients_details was *not* found in isExportClientList metrics but expected")
		} else if !isExportClientList && found {
			t.Errorf("connected_clients_details was *found* in isExportClientList metrics but *not* expected")
		}
	}
}

func TestExportClientListInclPort(t *testing.T) {
	for _, inclPort := range []bool{true, false} {
		e := getTestExporterWithOptions(Options{
			Namespace: "test", Registry: prometheus.NewRegistry(),
			ExportClientList:      true,
			ExportClientsInclPort: inclPort,
		})

		chM := make(chan prometheus.Metric)
		go func() {
			e.Collect(chM)
			close(chM)
		}()

		found := false
		for m := range chM {
			desc := m.Desc().String()
			if strings.Contains(desc, "connected_clients_details") {
				if strings.Contains(desc, "port") {
					found = true
				}
			}
		}

		if inclPort && !found {
			t.Errorf(`connected_clients_details did *not* include "port" in isExportClientList metrics but was expected`)
		} else if !inclPort && found {
			t.Errorf(`connected_clients_details did *include* "port" in isExportClientList metrics but was *not* expected`)
		}
	}
}
