// File metadata (manifest mirror):
// Purpose: Exercises the `telemetry_test` package behavior through TestExporterNewValidatesEndpointRatioAndIdentity, TestExporterNewJoinsCollectorPathsAndFallsBackInstanceIdentity, TestExporterRecordRejectsIncompleteEventsAndNilReceivers, TestExporterRecordProjectsStatusParentageAndSanitizedMetadata, TestExporterRecordRespectsRatioBoundary; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-11
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package telemetry_test

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/execution"
	"contractaudit/internal/telemetry"
)

func TestExporterValidatesEndpointRatioAndIdentity(t *testing.T) {
	for _, endpoint := range []string{
		"collector.example", "ftp://collector.example", "http://operator:secret@collector.example",
		"http://collector.example?tenant=1", "http://collector.example#fragment",
		"http:///collector", "https://collector.example/%zz",
	} {
		exporter, err := telemetry.New(telemetry.Config{Endpoint: endpoint, Service: "audit.worker", SampleRatio: 1})
		if err == nil || exporter != nil {
			t.Fatalf("invalid endpoint %q accepted: %v", endpoint, err)
		}
	}
	for _, ratio := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.01, 1.01} {
		exporter, err := telemetry.New(telemetry.Config{Endpoint: "http://collector.example", Service: "audit.worker", SampleRatio: ratio})
		if err == nil || exporter != nil {
			t.Fatalf("invalid sampling ratio %v accepted: %v", ratio, err)
		}
	}
	for _, service := range []string{"", "audit worker", strings.Repeat("a", 129)} {
		exporter, err := telemetry.New(telemetry.Config{Endpoint: "http://collector.example", Service: service, SampleRatio: 1})
		if err == nil || exporter != nil {
			t.Fatalf("invalid service identity %q accepted: %v", service, err)
		}
	}
	exporter, err := telemetry.New(telemetry.Config{Endpoint: "   ", Service: "audit.worker", SampleRatio: 1})
	if err != nil || exporter != nil {
		t.Fatalf("empty endpoint must disable the exporter: %v %v", exporter, err)
	}
}

func TestExporterNewDoesNotConnectAndRejectsIncompleteEvents(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeOTLPAck(w, r.URL.Path)
	}))
	defer server.Close()
	exporter, err := telemetry.New(telemetry.Config{
		Endpoint: server.URL + "/collector/", Service: "audit.worker",
		InstanceID: "invalid instance!", SampleRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("New made a network request at startup")
	}
	duration, negative := int64(5), int64(-1)
	for _, event := range []execution.Event{
		{SpanID: "span", At: time.Unix(1780000000, 0)},
		{SpanID: "span", At: time.Unix(1780000000, 0), DurationMS: &negative},
		{SpanID: "span", DurationMS: &duration},
		{At: time.Unix(1780000000, 0), DurationMS: &duration},
	} {
		exporter.Record("run", "attempt", event)
	}
	exporter.Close()
	if calls.Load() != 0 || exporter.Stats().Exported != 0 {
		t.Fatalf("incomplete events reached the collector: calls=%d stats=%+v", calls.Load(), exporter.Stats())
	}
}

func TestExporterNilAndClosedReceiverSemantics(t *testing.T) {
	var nilExporter *telemetry.Exporter
	nilExporter.Record("run", "attempt", completedEvent("span", "attempt"))
	if stats := nilExporter.Stats(); stats != (telemetry.Stats{}) {
		t.Fatalf("nil exporter stats %+v", stats)
	}
	nilExporter.Close()

	exporter, err := telemetry.New(telemetry.Config{Endpoint: "http://collector.invalid", Service: "audit.worker", SampleRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	exporter.Close()
	exporter.Record("run", "attempt", completedEvent("span", "attempt"))
	if stats := exporter.Stats(); stats.Dropped != 1 {
		t.Fatalf("record after close was not counted: %+v", stats)
	}
}
