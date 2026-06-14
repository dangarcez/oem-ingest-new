package exporter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"oem-ingest-new/internal/transform"

	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestMetricsExporterPostsOTLPAndClearsBuffer(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exporter, err := NewMetricsExporter(server.URL+"/otel/", MetricsExporterOptions{})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	if exporter.Endpoint() != server.URL+"/otel/v1/metrics" {
		t.Fatalf("Endpoint() = %q, want /otel/v1/metrics", exporter.Endpoint())
	}

	collectedAt := time.Unix(1700000000, 123)
	exporter.Add(
		transform.MetricPoint{
			Name:      "OEM_Response_Status",
			Value:     2,
			Timestamp: collectedAt,
			Attributes: transform.Attributes{
				"target_name": "db1",
				"attempt":     1,
				"active":      true,
			},
		},
		transform.MetricPoint{
			Name:      "oem_response_status",
			Value:     3.5,
			Timestamp: collectedAt.Add(time.Second),
			Attributes: transform.Attributes{
				"target_name": "db2",
			},
		},
	)

	result, err := exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if result.Datapoints != 2 || result.StatusCode != http.StatusNoContent || result.PayloadBytes == 0 || result.Skipped {
		t.Fatalf("Export() result = %#v, want 2 datapoints, 204 and payload", result)
	}
	if exporter.Pending() != 0 {
		t.Fatalf("Pending() = %d, want 0 after successful export", exporter.Pending())
	}

	requests := recorder.requests()
	if len(requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(requests))
	}
	if requests[0].path != "/otel/v1/metrics" {
		t.Fatalf("request path = %q, want /otel/v1/metrics", requests[0].path)
	}
	if requests[0].contentType != "application/x-protobuf" {
		t.Fatalf("Content-Type = %q, want application/x-protobuf", requests[0].contentType)
	}

	payload := decodeMetricsPayload(t, requests[0].body)
	resourceMetrics := payload.ResourceMetrics[0]
	if got := stringAttr(resourceMetrics.Resource.Attributes, "service.name"); got != "oemAPIService" {
		t.Fatalf("service.name = %q, want oemAPIService", got)
	}
	scopeMetrics := resourceMetrics.ScopeMetrics[0]
	if scopeMetrics.Scope.Name != "oem.metrics.collector" {
		t.Fatalf("scope name = %q, want legacy scope", scopeMetrics.Scope.Name)
	}
	metric := findMetric(t, scopeMetrics.Metrics, "oem_response_status")
	if metric.GetGauge() == nil {
		t.Fatal("metric gauge is nil")
	}
	if len(metric.GetGauge().DataPoints) != 2 {
		t.Fatalf("datapoints len = %d, want 2", len(metric.GetGauge().DataPoints))
	}
	first := metric.GetGauge().DataPoints[0]
	if first.TimeUnixNano != uint64(collectedAt.UnixNano()) {
		t.Fatalf("TimeUnixNano = %d, want %d", first.TimeUnixNano, collectedAt.UnixNano())
	}
	if first.GetAsDouble() != 2 {
		t.Fatalf("first value = %v, want 2", first.GetAsDouble())
	}
	if got := stringAttr(first.Attributes, "target_name"); got != "db1" {
		t.Fatalf("target_name = %q, want db1", got)
	}
	if got := intAttr(first.Attributes, "attempt"); got != 1 {
		t.Fatalf("attempt = %d, want 1", got)
	}
	if got := boolAttr(first.Attributes, "active"); !got {
		t.Fatal("active = false, want true")
	}

	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("second Export() error = %v", err)
	}
	if !result.Skipped {
		t.Fatalf("second Export() result = %#v, want skipped without stale metrics", result)
	}
	if got := len(recorder.requests()); got != 1 {
		t.Fatalf("requests after second export = %d, want still 1", got)
	}
}

func TestMetricsExporterRetainsBufferAfterFailureAndRetries(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		if len(recorder.requests()) == 1 {
			http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter, err := NewMetricsExporter(server.URL, MetricsExporterOptions{})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	exporter.Add(transform.MetricPoint{
		Name:      "oem_load_value",
		Value:     9,
		Timestamp: time.Unix(1700000100, 0),
		Attributes: transform.Attributes{
			"target_name": "host01",
		},
	})

	result, err := exporter.Export(context.Background())
	if err == nil {
		t.Fatal("first Export() error = nil, want HTTP failure")
	}
	if result.StatusCode != http.StatusServiceUnavailable || result.Datapoints != 1 {
		t.Fatalf("first Export() result = %#v, want 503 with one datapoint", result)
	}
	if exporter.Pending() != 1 {
		t.Fatalf("Pending() = %d, want retained datapoint after failure", exporter.Pending())
	}

	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("retry Export() error = %v", err)
	}
	if result.StatusCode != http.StatusOK || result.Datapoints != 1 {
		t.Fatalf("retry Export() result = %#v, want 200 with retained datapoint", result)
	}
	if exporter.Pending() != 0 {
		t.Fatalf("Pending() = %d, want cleared after retry success", exporter.Pending())
	}

	requests := recorder.requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want failure plus retry", len(requests))
	}
	payload := decodeMetricsPayload(t, requests[1].body)
	metric := findMetric(t, payload.ResourceMetrics[0].ScopeMetrics[0].Metrics, "oem_load_value")
	if len(metric.GetGauge().DataPoints) != 1 || metric.GetGauge().DataPoints[0].GetAsDouble() != 9 {
		t.Fatalf("retry payload datapoints = %#v, want retained value 9", metric.GetGauge().DataPoints)
	}

	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("third Export() error = %v", err)
	}
	if !result.Skipped {
		t.Fatalf("third Export() result = %#v, want skipped after retry success", result)
	}
	if got := len(recorder.requests()); got != 2 {
		t.Fatalf("requests after cleared buffer = %d, want still 2", got)
	}
}

func TestNewMetricsExporterRequiresValidBaseURL(t *testing.T) {
	if _, err := NewMetricsExporter("", MetricsExporterOptions{}); err == nil {
		t.Fatal("NewMetricsExporter(empty) error = nil, want validation error")
	}
	if _, err := NewMetricsExporter("localhost:4318", MetricsExporterOptions{}); err == nil {
		t.Fatal("NewMetricsExporter(no scheme) error = nil, want validation error")
	}
}

type recordedRequest struct {
	path        string
	contentType string
	body        []byte
}

type requestRecorder struct {
	mu           sync.Mutex
	requestsList []recordedRequest
}

func (r *requestRecorder) record(t *testing.T, req *http.Request) {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll(request body) error = %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestsList = append(r.requestsList, recordedRequest{
		path:        req.URL.Path,
		contentType: req.Header.Get("Content-Type"),
		body:        body,
	})
}

func (r *requestRecorder) requests() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedRequest, len(r.requestsList))
	copy(out, r.requestsList)
	return out
}

func decodeMetricsPayload(t *testing.T, body []byte) *collectormetricspb.ExportMetricsServiceRequest {
	t.Helper()
	var payload collectormetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &payload); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if len(payload.ResourceMetrics) != 1 {
		t.Fatalf("ResourceMetrics len = %d, want 1", len(payload.ResourceMetrics))
	}
	if len(payload.ResourceMetrics[0].ScopeMetrics) != 1 {
		t.Fatalf("ScopeMetrics len = %d, want 1", len(payload.ResourceMetrics[0].ScopeMetrics))
	}
	return &payload
}

func findMetric(t *testing.T, metrics []*metricspb.Metric, name string) *metricspb.Metric {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("metric %q not found in %#v", name, metrics)
	return nil
}

func stringAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.GetStringValue()
		}
	}
	return ""
}

func intAttr(attrs []*commonpb.KeyValue, key string) int64 {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.GetIntValue()
		}
	}
	return 0
}

func boolAttr(attrs []*commonpb.KeyValue, key string) bool {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.GetBoolValue()
		}
	}
	return false
}
