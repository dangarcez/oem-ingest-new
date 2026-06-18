package exporter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"oem-ingest-new/internal/selfmetrics"
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

func TestAnyValuePreservesStructuredAttributes(t *testing.T) {
	value := anyValue(map[string]any{
		"escalationLevel": map[string]any{
			"displayName": "None",
			"name":        "NONE",
		},
		"links": map[string]any{
			"self": struct {
				Href string `json:"href"`
			}{Href: "/em/api/incidents/INC-1"},
		},
		"suppressionStatus": map[string]any{
			"isSuppressed":  false,
			"suppressUntil": "NONE",
		},
		"targets": []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			TypeName        string `json:"typeName"`
			TypeDisplayName string `json:"typeDisplayName"`
		}{{
			ID:              "target-1",
			Name:            "db1",
			TypeName:        "oracle_database",
			TypeDisplayName: "Database Instance",
		}},
	})

	root := value.GetKvlistValue()
	if root == nil {
		t.Fatalf("structured attribute encoded as %T, want kvlist", value.Value)
	}
	escalation := attrValue(root.Values, "escalationLevel").GetKvlistValue()
	if escalation == nil {
		t.Fatalf("escalationLevel encoded as %#v, want kvlist", attrValue(root.Values, "escalationLevel"))
	}
	if got := attrValue(escalation.Values, "displayName").GetStringValue(); got != "None" {
		t.Fatalf("escalationLevel.displayName = %q, want None", got)
	}
	if got := attrValue(escalation.Values, "name").GetStringValue(); got != "NONE" {
		t.Fatalf("escalationLevel.name = %q, want NONE", got)
	}

	links := attrValue(root.Values, "links").GetKvlistValue()
	self := attrValue(links.Values, "self").GetKvlistValue()
	if got := attrValue(self.Values, "href").GetStringValue(); got != "/em/api/incidents/INC-1" {
		t.Fatalf("links.self.href = %q, want incident self link", got)
	}

	suppression := attrValue(root.Values, "suppressionStatus").GetKvlistValue()
	if got := attrValue(suppression.Values, "isSuppressed").GetBoolValue(); got {
		t.Fatalf("suppressionStatus.isSuppressed = %v, want false", got)
	}

	targets := attrValue(root.Values, "targets").GetArrayValue()
	if targets == nil || len(targets.Values) != 1 {
		t.Fatalf("targets encoded as %#v, want one-element array", attrValue(root.Values, "targets"))
	}
	target := targets.Values[0].GetKvlistValue()
	if target == nil {
		t.Fatalf("targets[0] encoded as %#v, want kvlist", targets.Values[0])
	}
	if got := attrValue(target.Values, "id").GetStringValue(); got != "target-1" {
		t.Fatalf("targets[0].id = %q, want target-1", got)
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

func TestMetricsExporterRecordsBatchObservability(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	registry := selfmetrics.NewRegistry()
	logger := &exportRecordingLogger{}
	exporter, err := NewMetricsExporter(server.URL, MetricsExporterOptions{
		Observer: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	exporter.Add(
		transform.MetricPoint{
			Name:  "oem_response_status",
			Value: 2,
			Attributes: transform.Attributes{
				"target_name": "db1",
			},
		},
		transform.MetricPoint{
			Name:  "oem_load_value",
			Value: 0.5,
			Attributes: transform.Attributes{
				"target_name": "host01",
			},
		},
	)

	result, err := exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if result.Duration <= 0 {
		t.Fatalf("Export() duration = %s, want recorded duration", result.Duration)
	}

	stats := registry.SnapshotStats()
	if stats.DatapointsExportedTotal != 2 {
		t.Fatalf("DatapointsExportedTotal = %d, want 2", stats.DatapointsExportedTotal)
	}
	if stats.ExportPayloadBytes != uint64(result.PayloadBytes) {
		t.Fatalf("ExportPayloadBytes = %d, want %d", stats.ExportPayloadBytes, result.PayloadBytes)
	}
	if stats.ExportDurationSeconds <= 0 {
		t.Fatalf("ExportDurationSeconds = %v, want positive duration", stats.ExportDurationSeconds)
	}

	infos := logger.infosSnapshot()
	if len(infos) != 1 {
		t.Fatalf("info logs len = %d, want one batch summary", len(infos))
	}
	if !infos[0].contains("metrics") || !infos[0].contains("datapoints") || !infos[0].contains("payload_bytes") || !infos[0].contains("duration") {
		t.Fatalf("info log missing batch fields: %#v", infos[0])
	}
	if infos[0].contains("oem_response_status") || infos[0].contains("target_name") || infos[0].contains("db1") {
		t.Fatalf("info log contains per-datapoint details: %#v", infos[0])
	}
	if len(logger.warnsSnapshot()) != 0 {
		t.Fatalf("warn logs = %#v, want none on success", logger.warnsSnapshot())
	}
}

func TestMetricsExporterRecordsFailureObservability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	registry := selfmetrics.NewRegistry()
	logger := &exportRecordingLogger{}
	exporter, err := NewMetricsExporter(server.URL, MetricsExporterOptions{
		Observer: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	exporter.Add(transform.MetricPoint{Name: "oem_load_value", Value: 9})

	result, err := exporter.Export(context.Background())
	if err == nil {
		t.Fatal("Export() error = nil, want failure")
	}
	if result.Duration <= 0 || result.PayloadBytes == 0 {
		t.Fatalf("Export() result = %#v, want duration and payload on failure", result)
	}

	stats := registry.SnapshotStats()
	if stats.DatapointsExportedTotal != 0 {
		t.Fatalf("DatapointsExportedTotal = %d, want no successful datapoints", stats.DatapointsExportedTotal)
	}
	if stats.ExportFailuresTotal != 1 {
		t.Fatalf("ExportFailuresTotal = %d, want 1", stats.ExportFailuresTotal)
	}
	if stats.ExportPayloadBytes != uint64(result.PayloadBytes) {
		t.Fatalf("ExportPayloadBytes = %d, want %d", stats.ExportPayloadBytes, result.PayloadBytes)
	}
	if stats.ExportDurationSeconds <= 0 {
		t.Fatalf("ExportDurationSeconds = %v, want positive duration", stats.ExportDurationSeconds)
	}

	warns := logger.warnsSnapshot()
	if len(warns) != 1 {
		t.Fatalf("warn logs len = %d, want one failure summary", len(warns))
	}
	if !warns[0].contains("http_status") || warns[0].contains("oem_load_value") {
		t.Fatalf("warn log should summarize failure without datapoint details: %#v", warns[0])
	}
}

func TestMetricsExporterRetainsBufferAfterTransportErrorAndRetries(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("collector connection failed")
			}
			_, _ = io.Copy(io.Discard, req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}),
	}

	exporter, err := NewMetricsExporter("http://collector.example:4318", MetricsExporterOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	exporter.Add(transform.MetricPoint{Name: "oem_retry_value", Value: 11})

	result, err := exporter.Export(context.Background())
	if err == nil {
		t.Fatal("first Export() error = nil, want transport failure")
	}
	if result.Datapoints != 1 || result.PayloadBytes == 0 || result.StatusCode != 0 {
		t.Fatalf("first Export() result = %#v, want retained failed transport attempt", result)
	}
	if exporter.Pending() != 1 {
		t.Fatalf("Pending() = %d, want retained datapoint after transport failure", exporter.Pending())
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
	if attempts != 2 {
		t.Fatalf("attempts = %d, want failure plus retry", attempts)
	}
}

func TestMetricsExporterClonesAttributesWhenBuffering(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter, err := NewMetricsExporter(server.URL, MetricsExporterOptions{})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	attrs := transform.Attributes{"target_name": "db1"}
	exporter.Add(transform.MetricPoint{
		Name:       "oem_response_status",
		Value:      2,
		Attributes: attrs,
	})
	attrs["target_name"] = "mutated"
	attrs["leaked"] = "true"

	if _, err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	requests := recorder.requests()
	if len(requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(requests))
	}
	payload := decodeMetricsPayload(t, requests[0].body)
	metric := findMetric(t, payload.ResourceMetrics[0].ScopeMetrics[0].Metrics, "oem_response_status")
	datapoint := metric.GetGauge().DataPoints[0]
	if got := stringAttr(datapoint.Attributes, "target_name"); got != "db1" {
		t.Fatalf("target_name = %q, want original db1", got)
	}
	if got := stringAttr(datapoint.Attributes, "leaked"); got != "" {
		t.Fatalf("leaked attr = %q, want attribute mutation isolated from buffer", got)
	}
}

func TestMetricsExporterKeepsConcurrentAddsForNextCycle(t *testing.T) {
	var recorder requestRecorder
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	releasePost := func() { releaseOnce.Do(func() { close(release) }) }
	defer releasePost()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		if len(recorder.requests()) == 1 {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter, err := NewMetricsExporter(server.URL, MetricsExporterOptions{})
	if err != nil {
		t.Fatalf("NewMetricsExporter() error = %v", err)
	}
	exporter.Add(transform.MetricPoint{Name: "oem_live_value", Value: 1})

	errCh := make(chan error, 1)
	go func() {
		_, err := exporter.Export(context.Background())
		errCh <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first export request")
	}
	exporter.Add(transform.MetricPoint{Name: "oem_live_value", Value: 2})
	releasePost()

	if err := <-errCh; err != nil {
		t.Fatalf("first Export() error = %v", err)
	}
	if exporter.Pending() != 1 {
		t.Fatalf("Pending() = %d, want concurrent add retained for next export", exporter.Pending())
	}
	if _, err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("second Export() error = %v", err)
	}
	if exporter.Pending() != 0 {
		t.Fatalf("Pending() = %d, want cleared after second export", exporter.Pending())
	}

	requests := recorder.requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want two export cycles", len(requests))
	}
	firstPayload := decodeMetricsPayload(t, requests[0].body)
	firstMetric := findMetric(t, firstPayload.ResourceMetrics[0].ScopeMetrics[0].Metrics, "oem_live_value")
	if got := firstMetric.GetGauge().DataPoints[0].GetAsDouble(); got != 1 {
		t.Fatalf("first export value = %v, want 1", got)
	}
	secondPayload := decodeMetricsPayload(t, requests[1].body)
	secondMetric := findMetric(t, secondPayload.ResourceMetrics[0].ScopeMetrics[0].Metrics, "oem_live_value")
	if got := secondMetric.GetGauge().DataPoints[0].GetAsDouble(); got != 2 {
		t.Fatalf("second export value = %v, want concurrent datapoint 2", got)
	}
}

func TestNewMetricsExporterRequiresValidBaseURL(t *testing.T) {
	if _, err := NewMetricsExporter("", MetricsExporterOptions{}); err == nil {
		t.Fatal("NewMetricsExporter(empty) error = nil, want validation error")
	}
	if _, err := NewMetricsExporter("localhost:4318", MetricsExporterOptions{}); err == nil {
		t.Fatal("NewMetricsExporter(no scheme) error = nil, want validation error")
	}
	exporter, err := NewMetricsExporter("http://collector.example:4318", MetricsExporterOptions{})
	if err != nil {
		t.Fatalf("NewMetricsExporter(valid) error = %v", err)
	}
	if exporter.httpClient.Timeout != defaultHTTPTimeout {
		t.Fatalf("default HTTP timeout = %s, want %s", exporter.httpClient.Timeout, defaultHTTPTimeout)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

type exportLogEntry struct {
	msg  string
	args []any
}

func (e exportLogEntry) contains(value string) bool {
	if strings.Contains(e.msg, value) {
		return true
	}
	for _, arg := range e.args {
		if strings.Contains(fmt.Sprint(arg), value) {
			return true
		}
	}
	return false
}

type exportRecordingLogger struct {
	mu    sync.Mutex
	infos []exportLogEntry
	warns []exportLogEntry
}

func (l *exportRecordingLogger) InfoContext(_ context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, exportLogEntry{msg: msg, args: append([]any(nil), args...)})
}

func (l *exportRecordingLogger) WarnContext(_ context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, exportLogEntry{msg: msg, args: append([]any(nil), args...)})
}

func (l *exportRecordingLogger) infosSnapshot() []exportLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]exportLogEntry, len(l.infos))
	copy(out, l.infos)
	return out
}

func (l *exportRecordingLogger) warnsSnapshot() []exportLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]exportLogEntry, len(l.warns))
	copy(out, l.warns)
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
	return attrValue(attrs, key).GetStringValue()
}

func attrValue(attrs []*commonpb.KeyValue, key string) *commonpb.AnyValue {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	return &commonpb.AnyValue{}
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
