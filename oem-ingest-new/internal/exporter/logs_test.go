package exporter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"oem-ingest-new/internal/selfmetrics"
	"oem-ingest-new/internal/transform"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestLogsExporterPostsOTLPAndSkipsUnchangedValues(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exporter, err := NewLogsExporter(server.URL+"/otel/", LogsExporterOptions{})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	if exporter.Endpoint() != server.URL+"/otel/v1/logs" {
		t.Fatalf("Endpoint() = %q, want /otel/v1/logs", exporter.Endpoint())
	}

	collectedAt := time.Unix(1700000000, 123)
	record := transform.LogRecord{
		MetricName: "OEM_File_Status",
		TargetID:   "target-1",
		SeriesID:   "target-1\x00/",
		Body:       "Mounted",
		Timestamp:  collectedAt,
		Attributes: transform.Attributes{
			"target_name": "host01",
			"attempt":     1,
			"active":      true,
		},
	}
	exporter.Add(record, record)

	result, err := exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if result.Logs != 1 || result.StatusCode != http.StatusNoContent || result.PayloadBytes == 0 || result.Skipped {
		t.Fatalf("Export() result = %#v, want one log, 204 and payload", result)
	}
	if exporter.Pending() != 0 {
		t.Fatalf("Pending() = %d, want 0 after successful export", exporter.Pending())
	}

	requests := recorder.requests()
	if len(requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(requests))
	}
	if requests[0].path != "/otel/v1/logs" {
		t.Fatalf("request path = %q, want /otel/v1/logs", requests[0].path)
	}
	if requests[0].contentType != "application/x-protobuf" {
		t.Fatalf("Content-Type = %q, want application/x-protobuf", requests[0].contentType)
	}

	payload := decodeLogsPayload(t, requests[0].body)
	resourceLogs := payload.ResourceLogs[0]
	if got := stringAttr(resourceLogs.Resource.Attributes, "service.name"); got != "oemAPIService" {
		t.Fatalf("service.name = %q, want oemAPIService", got)
	}
	scopeLogs := resourceLogs.ScopeLogs[0]
	if scopeLogs.Scope.Name != "oem.logs.collector" {
		t.Fatalf("scope name = %q, want log scope", scopeLogs.Scope.Name)
	}
	if len(scopeLogs.LogRecords) != 1 {
		t.Fatalf("log records len = %d, want 1", len(scopeLogs.LogRecords))
	}
	logRecord := scopeLogs.LogRecords[0]
	if logRecord.TimeUnixNano != uint64(collectedAt.UnixNano()) {
		t.Fatalf("TimeUnixNano = %d, want %d", logRecord.TimeUnixNano, collectedAt.UnixNano())
	}
	if logRecord.ObservedTimeUnixNano == 0 {
		t.Fatal("ObservedTimeUnixNano = 0, want exporter observation timestamp")
	}
	if logRecord.SeverityNumber != logspb.SeverityNumber_SEVERITY_NUMBER_INFO || logRecord.SeverityText != "INFO" {
		t.Fatalf("severity = %s/%q, want INFO", logRecord.SeverityNumber, logRecord.SeverityText)
	}
	if got := logRecord.Body.GetStringValue(); got != "Mounted" {
		t.Fatalf("body = %q, want Mounted", got)
	}
	if got := stringAttr(logRecord.Attributes, "metric"); got != "oem_file_status" {
		t.Fatalf("metric attr = %q, want lowercase metric name", got)
	}
	if got := stringAttr(logRecord.Attributes, "target_name"); got != "host01" {
		t.Fatalf("target_name = %q, want host01", got)
	}
	if got := intAttr(logRecord.Attributes, "attempt"); got != 1 {
		t.Fatalf("attempt = %d, want 1", got)
	}
	if got := boolAttr(logRecord.Attributes, "active"); !got {
		t.Fatal("active = false, want true")
	}

	exporter.Add(record)
	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("second Export() error = %v", err)
	}
	if !result.Skipped {
		t.Fatalf("second Export() result = %#v, want skipped for unchanged value", result)
	}
	if got := len(recorder.requests()); got != 1 {
		t.Fatalf("requests after unchanged value = %d, want still 1", got)
	}

	record.Body = "Unmounted"
	exporter.Add(record)
	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("changed Export() error = %v", err)
	}
	if result.Logs != 1 || result.StatusCode != http.StatusNoContent {
		t.Fatalf("changed Export() result = %#v, want one changed log", result)
	}
	requests = recorder.requests()
	if len(requests) != 2 {
		t.Fatalf("requests after changed value = %d, want 2", len(requests))
	}
	changedPayload := decodeLogsPayload(t, requests[1].body)
	changedLog := changedPayload.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if got := changedLog.Body.GetStringValue(); got != "Unmounted" {
		t.Fatalf("changed body = %q, want Unmounted", got)
	}
}

func TestLogsExporterAlwaysSendsContinuousValues(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter, err := NewLogsExporter(server.URL, LogsExporterOptions{})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	record := transform.LogRecord{
		MetricName: "oem_str_service_status",
		TargetID:   "target-1",
		SeriesID:   "target-1\x00svc",
		Body:       "ativo",
		Continuous: true,
		Attributes: transform.Attributes{"target_name": "db1"},
	}

	exporter.Add(record)
	if _, err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("first Export() error = %v", err)
	}
	exporter.Add(record)
	if _, err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("second Export() error = %v", err)
	}

	requests := recorder.requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want 2 continuous exports", len(requests))
	}
	for i, req := range requests {
		payload := decodeLogsPayload(t, req.body)
		logs := payload.ResourceLogs[0].ScopeLogs[0].LogRecords
		if len(logs) != 1 || logs[0].Body.GetStringValue() != "ativo" {
			t.Fatalf("request %d logs = %#v, want one ativo log", i, logs)
		}
	}
}

func TestLogsExporterRecordsBatchObservability(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	registry := selfmetrics.NewRegistry()
	logger := &exportRecordingLogger{}
	exporter, err := NewLogsExporter(server.URL, LogsExporterOptions{
		Observer: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	exporter.Add(
		transform.LogRecord{
			MetricName: "oem_filesystem_status",
			TargetID:   "target-1",
			SeriesID:   "target-1\x00/",
			Body:       "Mounted",
			Attributes: transform.Attributes{"target_name": "host01"},
		},
		transform.LogRecord{
			MetricName: "oem_listener_status",
			TargetID:   "target-2",
			SeriesID:   "target-2\x00listener",
			Body:       "OPEN",
			Attributes: transform.Attributes{"target_name": "listener01"},
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
	if stats.LogsExportedTotal != 2 {
		t.Fatalf("LogsExportedTotal = %d, want 2", stats.LogsExportedTotal)
	}
	if stats.DatapointsExportedTotal != 0 {
		t.Fatalf("DatapointsExportedTotal = %d, want log export not counted as datapoints", stats.DatapointsExportedTotal)
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
	if !infos[0].contains("logs") || !infos[0].contains("payload_bytes") || !infos[0].contains("duration") {
		t.Fatalf("info log missing batch fields: %#v", infos[0])
	}
	if infos[0].contains("Mounted") || infos[0].contains("OPEN") || infos[0].contains("target_name") {
		t.Fatalf("info log contains per-log details: %#v", infos[0])
	}
	if len(logger.warnsSnapshot()) != 0 {
		t.Fatalf("warn logs = %#v, want none on success", logger.warnsSnapshot())
	}
}

func TestLogsExporterRecordsFailureObservability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	registry := selfmetrics.NewRegistry()
	logger := &exportRecordingLogger{}
	exporter, err := NewLogsExporter(server.URL, LogsExporterOptions{
		Observer: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	exporter.Add(transform.LogRecord{
		MetricName: "oem_connection_status",
		TargetID:   "target-1",
		SeriesID:   "target-1\x00connection",
		Body:       "password expired",
		Attributes: transform.Attributes{"target_name": "db1"},
	})

	result, err := exporter.Export(context.Background())
	if err == nil {
		t.Fatal("Export() error = nil, want failure")
	}
	if result.Duration <= 0 || result.PayloadBytes == 0 {
		t.Fatalf("Export() result = %#v, want duration and payload on failure", result)
	}

	stats := registry.SnapshotStats()
	if stats.LogsExportedTotal != 0 {
		t.Fatalf("LogsExportedTotal = %d, want no successful logs", stats.LogsExportedTotal)
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
	if !warns[0].contains("http_status") || warns[0].contains("password expired") || warns[0].contains("target_name") {
		t.Fatalf("warn log should summarize failure without log details: %#v", warns[0])
	}
}

func TestLogsExporterRetainsBufferAfterFailureAndRetries(t *testing.T) {
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

	registry := selfmetrics.NewRegistry()
	exporter, err := NewLogsExporter(server.URL, LogsExporterOptions{Observer: registry})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	exporter.Add(transform.LogRecord{
		MetricName: "oem_response_status",
		TargetID:   "target-1",
		SeriesID:   "target-1",
		Body:       "Down",
	})

	result, err := exporter.Export(context.Background())
	if err == nil {
		t.Fatal("first Export() error = nil, want HTTP failure")
	}
	if result.StatusCode != http.StatusServiceUnavailable || result.Logs != 1 {
		t.Fatalf("first Export() result = %#v, want 503 with one log", result)
	}
	if exporter.Pending() != 1 {
		t.Fatalf("Pending() = %d, want retained log after failure", exporter.Pending())
	}
	if stats := registry.SnapshotStats(); stats.ExportFailuresTotal != 1 || stats.LogsExportedTotal != 0 {
		t.Fatalf("stats after failed export = %#v, want one failure and no successful logs", stats)
	}

	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("retry Export() error = %v", err)
	}
	if result.StatusCode != http.StatusOK || result.Logs != 1 {
		t.Fatalf("retry Export() result = %#v, want 200 with retained log", result)
	}
	if exporter.Pending() != 0 {
		t.Fatalf("Pending() = %d, want cleared after retry success", exporter.Pending())
	}
	if stats := registry.SnapshotStats(); stats.ExportFailuresTotal != 1 || stats.LogsExportedTotal != 1 {
		t.Fatalf("stats after retry = %#v, want one failure and one exported log", stats)
	}

	requests := recorder.requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want failure plus retry", len(requests))
	}
	payload := decodeLogsPayload(t, requests[1].body)
	logRecords := payload.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(logRecords) != 1 || logRecords[0].Body.GetStringValue() != "Down" {
		t.Fatalf("retry payload logs = %#v, want retained body Down", logRecords)
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

func TestLogsExporterRetainsBufferAfterTransportErrorAndRetries(t *testing.T) {
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

	exporter, err := NewLogsExporter("http://collector.example:4318", LogsExporterOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	exporter.Add(transform.LogRecord{MetricName: "oem_retry_status", TargetID: "target-1", SeriesID: "target-1", Body: "OPEN"})

	result, err := exporter.Export(context.Background())
	if err == nil {
		t.Fatal("first Export() error = nil, want transport failure")
	}
	if result.Logs != 1 || result.PayloadBytes == 0 || result.StatusCode != 0 {
		t.Fatalf("first Export() result = %#v, want retained failed transport attempt", result)
	}
	if exporter.Pending() != 1 {
		t.Fatalf("Pending() = %d, want retained log after transport failure", exporter.Pending())
	}

	result, err = exporter.Export(context.Background())
	if err != nil {
		t.Fatalf("retry Export() error = %v", err)
	}
	if result.StatusCode != http.StatusOK || result.Logs != 1 {
		t.Fatalf("retry Export() result = %#v, want 200 with retained log", result)
	}
	if exporter.Pending() != 0 {
		t.Fatalf("Pending() = %d, want cleared after retry success", exporter.Pending())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want failure plus retry", attempts)
	}
}

func TestLogsExporterClonesAttributesWhenBuffering(t *testing.T) {
	var recorder requestRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter, err := NewLogsExporter(server.URL, LogsExporterOptions{})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	attrs := transform.Attributes{"target_name": "db1"}
	exporter.Add(transform.LogRecord{
		MetricName: "oem_response_status",
		TargetID:   "target-1",
		SeriesID:   "target-1",
		Body:       "Up",
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
	payload := decodeLogsPayload(t, requests[0].body)
	logRecord := payload.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if got := stringAttr(logRecord.Attributes, "target_name"); got != "db1" {
		t.Fatalf("target_name = %q, want original db1", got)
	}
	if got := stringAttr(logRecord.Attributes, "leaked"); got != "" {
		t.Fatalf("leaked attr = %q, want attribute mutation isolated from buffer", got)
	}
}

func TestLogsExporterKeepsConcurrentAddsForNextCycle(t *testing.T) {
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

	exporter, err := NewLogsExporter(server.URL, LogsExporterOptions{})
	if err != nil {
		t.Fatalf("NewLogsExporter() error = %v", err)
	}
	exporter.Add(transform.LogRecord{
		MetricName: "oem_live_status",
		TargetID:   "target-1",
		SeriesID:   "target-1\x00filesystem",
		Body:       "Mounted",
	})

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
	exporter.Add(transform.LogRecord{
		MetricName: "oem_live_status",
		TargetID:   "target-1",
		SeriesID:   "target-1\x00filesystem",
		Body:       "Unmounted",
	})
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
	firstPayload := decodeLogsPayload(t, requests[0].body)
	firstLog := firstPayload.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if got := firstLog.Body.GetStringValue(); got != "Mounted" {
		t.Fatalf("first export body = %q, want Mounted", got)
	}
	secondPayload := decodeLogsPayload(t, requests[1].body)
	secondLog := secondPayload.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if got := secondLog.Body.GetStringValue(); got != "Unmounted" {
		t.Fatalf("second export body = %q, want concurrent log Unmounted", got)
	}
}

func TestNewLogsExporterRequiresValidBaseURL(t *testing.T) {
	if _, err := NewLogsExporter("", LogsExporterOptions{}); err == nil {
		t.Fatal("NewLogsExporter(empty) error = nil, want validation error")
	}
	if _, err := NewLogsExporter("localhost:4318", LogsExporterOptions{}); err == nil {
		t.Fatal("NewLogsExporter(no scheme) error = nil, want validation error")
	}
	exporter, err := NewLogsExporter("http://collector.example:4318", LogsExporterOptions{})
	if err != nil {
		t.Fatalf("NewLogsExporter(valid) error = %v", err)
	}
	if exporter.httpClient.Timeout != defaultHTTPTimeout {
		t.Fatalf("default HTTP timeout = %s, want %s", exporter.httpClient.Timeout, defaultHTTPTimeout)
	}
}

func decodeLogsPayload(t *testing.T, body []byte) *collectorlogspb.ExportLogsServiceRequest {
	t.Helper()
	var payload collectorlogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &payload); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if len(payload.ResourceLogs) != 1 {
		t.Fatalf("ResourceLogs len = %d, want 1", len(payload.ResourceLogs))
	}
	if len(payload.ResourceLogs[0].ScopeLogs) != 1 {
		t.Fatalf("ScopeLogs len = %d, want 1", len(payload.ResourceLogs[0].ScopeLogs))
	}
	return &payload
}
