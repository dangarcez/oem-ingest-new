package exporter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"oem-ingest-new/internal/transform"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

const defaultLogsScopeName = "oem.logs.collector"

// LogsExporterOptions controls optional dependencies of LogsExporter.
type LogsExporterOptions struct {
	HTTPClient  *http.Client
	ServiceName string
	ScopeName   string
}

// LogsExportResult summarizes one log export attempt.
type LogsExportResult struct {
	Logs         int
	PayloadBytes int
	StatusCode   int
	Skipped      bool
}

// LogsExporter buffers normalized textual OEM metrics and sends them as OTLP
// logs over HTTP/protobuf. Values are enqueued only when the text changes for a
// series, except records marked Continuous, which are always enqueued.
type LogsExporter struct {
	endpoint    string
	httpClient  *http.Client
	serviceName string
	scopeName   string

	mu         sync.Mutex
	pending    []transform.LogRecord
	lastValues map[string]string

	exportMu sync.Mutex
}

// NewLogsExporter creates an OTLP logs exporter using baseURL as
// OTEL_EXPORT_URL. The /v1/logs path is appended by this constructor.
func NewLogsExporter(baseURL string, opts LogsExporterOptions) (*LogsExporter, error) {
	endpoint, err := logsEndpoint(baseURL)
	if err != nil {
		return nil, err
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = defaultServiceName
	}
	scopeName := strings.TrimSpace(opts.ScopeName)
	if scopeName == "" {
		scopeName = defaultLogsScopeName
	}

	return &LogsExporter{
		endpoint:    endpoint,
		httpClient:  httpClient,
		serviceName: serviceName,
		scopeName:   scopeName,
		lastValues:  make(map[string]string),
	}, nil
}

// Endpoint returns the full OTLP logs URL used by the exporter.
func (e *LogsExporter) Endpoint() string {
	if e == nil {
		return ""
	}
	return e.endpoint
}

// Add appends exportable textual logs to the pending buffer. Non-continuous
// records whose value is unchanged for the same metric/target/series are
// ignored, mirroring the legacy logger behavior.
func (e *LogsExporter) Add(records ...transform.LogRecord) {
	if e == nil || len(records) == 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastValues == nil {
		e.lastValues = make(map[string]string)
	}

	for _, record := range records {
		record = normalizeLogRecord(record)
		if record.MetricName == "" {
			continue
		}
		key := logSeriesKey(record)
		if last, ok := e.lastValues[key]; ok && last == record.Body && !record.Continuous {
			continue
		}
		e.pending = append(e.pending, record)
		e.lastValues[key] = record.Body
	}
}

// Pending returns the number of logs currently waiting for a successful export.
// It is intended for tests and operational wiring.
func (e *LogsExporter) Pending() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

// Export sends all pending logs. A failed POST leaves the buffer intact so the
// next call can retry the same records.
func (e *LogsExporter) Export(ctx context.Context) (LogsExportResult, error) {
	if e == nil {
		return LogsExportResult{Skipped: true}, nil
	}

	e.exportMu.Lock()
	defer e.exportMu.Unlock()

	batch := e.snapshot()
	if len(batch) == 0 {
		return LogsExportResult{Skipped: true}, nil
	}

	payload, err := e.buildPayload(batch, time.Now())
	if err != nil {
		return LogsExportResult{Logs: len(batch)}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return LogsExportResult{Logs: len(batch), PayloadBytes: len(payload)}, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return LogsExportResult{Logs: len(batch), PayloadBytes: len(payload)}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	result := LogsExportResult{
		Logs:         len(batch),
		PayloadBytes: len(payload),
		StatusCode:   resp.StatusCode,
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		return result, fmt.Errorf("exportar logs OTLP para %s: status HTTP %d", e.endpoint, resp.StatusCode)
	}

	e.discardExported(len(batch))
	return result, nil
}

func (e *LogsExporter) snapshot() []transform.LogRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pending) == 0 {
		return nil
	}
	out := make([]transform.LogRecord, len(e.pending))
	copy(out, e.pending)
	return out
}

func (e *LogsExporter) discardExported(count int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if count >= len(e.pending) {
		e.pending = nil
		return
	}
	remaining := make([]transform.LogRecord, len(e.pending)-count)
	copy(remaining, e.pending[count:])
	e.pending = remaining
}

func (e *LogsExporter) buildPayload(records []transform.LogRecord, fallbackTimestamp time.Time) ([]byte, error) {
	if len(records) == 0 {
		return nil, errors.New("nenhum log para exportar")
	}

	request := &collectorlogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{{
					Key:   "service.name",
					Value: stringValue(e.serviceName),
				}},
			},
			ScopeLogs: []*logspb.ScopeLogs{{
				Scope:      &commonpb.InstrumentationScope{Name: e.scopeName},
				LogRecords: buildLogRecords(records, fallbackTimestamp),
			}},
		}},
	}
	return proto.Marshal(request)
}

func buildLogRecords(records []transform.LogRecord, fallbackTimestamp time.Time) []*logspb.LogRecord {
	out := make([]*logspb.LogRecord, 0, len(records))
	observed := uint64(fallbackTimestamp.UnixNano())
	for _, record := range records {
		timestamp := record.Timestamp
		if timestamp.IsZero() {
			timestamp = fallbackTimestamp
		}
		out = append(out, &logspb.LogRecord{
			TimeUnixNano:         uint64(timestamp.UnixNano()),
			ObservedTimeUnixNano: observed,
			SeverityNumber:       logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
			SeverityText:         "INFO",
			Body:                 stringValue(record.Body),
			Attributes:           attributes(record.Attributes),
		})
	}
	return out
}

func normalizeLogRecord(record transform.LogRecord) transform.LogRecord {
	metricName := strings.ToLower(strings.TrimSpace(record.MetricName))
	if metricName == "" && len(record.Attributes) > 0 {
		if value, ok := record.Attributes["metric"]; ok {
			metricName = strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		}
	}

	record.MetricName = metricName
	record.TargetID = strings.TrimSpace(record.TargetID)
	if strings.TrimSpace(record.SeriesID) == "" {
		record.SeriesID = record.TargetID
	}
	record.Attributes = record.Attributes.Clone()
	if metricName != "" {
		record.Attributes["metric"] = metricName
	}
	return record
}

func logSeriesKey(record transform.LogRecord) string {
	return record.MetricName + "\x00" + record.TargetID + "\x00" + record.SeriesID
}

func logsEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", errors.New("OTEL_EXPORT_URL deve ser informado para exportar logs")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("OTEL_EXPORT_URL invalido %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("OTEL_EXPORT_URL invalido %q: informe scheme e host", baseURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/logs"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
