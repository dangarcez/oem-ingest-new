package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"oem-ingest-new/internal/transform"

	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

const (
	defaultServiceName = "oemAPIService"
	defaultScopeName   = "oem.metrics.collector"
	defaultHTTPTimeout = 30 * time.Second
)

// MetricsExporterOptions controls optional dependencies of MetricsExporter.
type MetricsExporterOptions struct {
	HTTPClient  *http.Client
	ServiceName string
	ScopeName   string
	Observer    MetricsObserver
	Logger      Logger
}

// MetricsExportResult summarizes one export attempt.
type MetricsExportResult struct {
	Datapoints   int
	PayloadBytes int
	StatusCode   int
	Duration     time.Duration
	Skipped      bool
}

// MetricsObserver receives batch-level export statistics.
type MetricsObserver interface {
	RecordMetricsExportSuccess(datapointsExported, payloadBytes uint64, duration time.Duration)
	RecordMetricsExportFailure(payloadBytes uint64, duration time.Duration)
}

// LogsObserver receives batch-level log export statistics.
type LogsObserver interface {
	RecordLogsExportSuccess(logsExported, payloadBytes uint64, duration time.Duration)
	RecordLogsExportFailure(payloadBytes uint64, duration time.Duration)
}

// Logger is intentionally compatible with slog.Logger.
type Logger interface {
	InfoContext(context.Context, string, ...any)
	WarnContext(context.Context, string, ...any)
}

// MetricsExporter buffers normalized numeric metrics and sends them as OTLP
// HTTP/protobuf. Buffered datapoints are cleared only after a 2xx response.
type MetricsExporter struct {
	endpoint    string
	httpClient  *http.Client
	serviceName string
	scopeName   string
	observer    MetricsObserver
	logger      Logger

	mu      sync.Mutex
	pending []transform.MetricPoint

	exportMu sync.Mutex
}

// NewMetricsExporter creates an OTLP metrics exporter using baseURL as
// OTEL_EXPORT_URL. The /v1/metrics path is appended by this constructor.
func NewMetricsExporter(baseURL string, opts MetricsExporterOptions) (*MetricsExporter, error) {
	endpoint, err := metricsEndpoint(baseURL)
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
		scopeName = defaultScopeName
	}

	return &MetricsExporter{
		endpoint:    endpoint,
		httpClient:  httpClient,
		serviceName: serviceName,
		scopeName:   scopeName,
		observer:    opts.Observer,
		logger:      opts.Logger,
	}, nil
}

// Endpoint returns the full OTLP metrics URL used by the exporter.
func (e *MetricsExporter) Endpoint() string {
	if e == nil {
		return ""
	}
	return e.endpoint
}

// Add appends datapoints to the pending buffer. Invalid points with blank names
// or non-finite values are ignored.
func (e *MetricsExporter) Add(points ...transform.MetricPoint) {
	if e == nil || len(points) == 0 {
		return
	}

	filtered := make([]transform.MetricPoint, 0, len(points))
	for _, point := range points {
		point.Name = strings.ToLower(strings.TrimSpace(point.Name))
		if point.Name == "" || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			continue
		}
		point.Attributes = point.Attributes.Clone()
		filtered = append(filtered, point)
	}
	if len(filtered) == 0 {
		return
	}

	e.mu.Lock()
	e.pending = append(e.pending, filtered...)
	e.mu.Unlock()
}

// Pending returns the number of datapoints currently waiting for a successful
// export. It is intended for tests and operational wiring.
func (e *MetricsExporter) Pending() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

// Export sends all datapoints collected since the last successful export. A
// failed POST leaves the buffer intact so the next call can retry the same data.
func (e *MetricsExporter) Export(ctx context.Context) (MetricsExportResult, error) {
	if e == nil {
		return MetricsExportResult{Skipped: true}, nil
	}

	e.exportMu.Lock()
	defer e.exportMu.Unlock()

	batch := e.snapshot()
	if len(batch) == 0 {
		return MetricsExportResult{Skipped: true}, nil
	}

	started := time.Now()
	payload, err := e.buildPayload(batch, time.Now())
	if err != nil {
		result := MetricsExportResult{Datapoints: len(batch), Duration: time.Since(started)}
		e.recordExport(ctx, result, err)
		return result, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		result := MetricsExportResult{Datapoints: len(batch), PayloadBytes: len(payload), Duration: time.Since(started)}
		e.recordExport(ctx, result, err)
		return result, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		result := MetricsExportResult{Datapoints: len(batch), PayloadBytes: len(payload), Duration: time.Since(started)}
		e.recordExport(ctx, result, err)
		return result, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	result := MetricsExportResult{
		Datapoints:   len(batch),
		PayloadBytes: len(payload),
		StatusCode:   resp.StatusCode,
		Duration:     time.Since(started),
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		err := fmt.Errorf("exportar metricas OTLP: status HTTP %d", resp.StatusCode)
		e.recordExport(ctx, result, err)
		return result, err
	}

	e.discardExported(len(batch))
	e.recordExport(ctx, result, nil)
	return result, nil
}

func (e *MetricsExporter) recordExport(ctx context.Context, result MetricsExportResult, err error) {
	if result.Skipped {
		return
	}
	if e.observer != nil {
		if err != nil {
			e.observer.RecordMetricsExportFailure(uint64(result.PayloadBytes), result.Duration)
		} else {
			e.observer.RecordMetricsExportSuccess(uint64(result.Datapoints), uint64(result.PayloadBytes), result.Duration)
		}
	}
	if e.logger == nil {
		return
	}
	args := []any{
		"signal", "metrics",
		"datapoints", result.Datapoints,
		"payload_bytes", result.PayloadBytes,
		"duration", result.Duration,
	}
	if result.StatusCode != 0 {
		args = append(args, "status_code", result.StatusCode)
	}
	if err != nil {
		args = append(args, "error_kind", exportErrorKind(result.StatusCode, result.PayloadBytes))
		e.logger.WarnContext(ctx, "falha ao exportar batch OTLP", args...)
		return
	}
	e.logger.InfoContext(ctx, "batch OTLP exportado", args...)
}

func exportErrorKind(statusCode, payloadBytes int) string {
	if statusCode != 0 {
		return "http_status"
	}
	if payloadBytes == 0 {
		return "payload_or_request"
	}
	return "transport"
}

func (e *MetricsExporter) snapshot() []transform.MetricPoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pending) == 0 {
		return nil
	}
	out := make([]transform.MetricPoint, len(e.pending))
	copy(out, e.pending)
	return out
}

func (e *MetricsExporter) discardExported(count int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if count >= len(e.pending) {
		e.pending = nil
		return
	}
	remaining := make([]transform.MetricPoint, len(e.pending)-count)
	copy(remaining, e.pending[count:])
	e.pending = remaining
}

func (e *MetricsExporter) buildPayload(points []transform.MetricPoint, fallbackTimestamp time.Time) ([]byte, error) {
	if len(points) == 0 {
		return nil, errors.New("nenhum datapoint para exportar")
	}

	request := &collectormetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{{
					Key:   "service.name",
					Value: stringValue(e.serviceName),
				}},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Scope:   &commonpb.InstrumentationScope{Name: e.scopeName},
				Metrics: buildMetrics(points, fallbackTimestamp),
			}},
		}},
	}
	return proto.Marshal(request)
}

func buildMetrics(points []transform.MetricPoint, fallbackTimestamp time.Time) []*metricspb.Metric {
	metricByName := make(map[string]*metricspb.Metric)
	order := make([]string, 0)
	for _, point := range points {
		name := strings.ToLower(strings.TrimSpace(point.Name))
		if name == "" {
			continue
		}
		metric := metricByName[name]
		if metric == nil {
			metric = &metricspb.Metric{
				Name: name,
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{}},
			}
			metricByName[name] = metric
			order = append(order, name)
		}
		gauge := metric.GetGauge()
		gauge.DataPoints = append(gauge.DataPoints, numberDataPoint(point, fallbackTimestamp))
	}

	metrics := make([]*metricspb.Metric, 0, len(order))
	for _, name := range order {
		metrics = append(metrics, metricByName[name])
	}
	return metrics
}

func numberDataPoint(point transform.MetricPoint, fallbackTimestamp time.Time) *metricspb.NumberDataPoint {
	timestamp := point.Timestamp
	if timestamp.IsZero() {
		timestamp = fallbackTimestamp
	}
	return &metricspb.NumberDataPoint{
		TimeUnixNano: uint64(timestamp.UnixNano()),
		Attributes:   attributes(point.Attributes),
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: point.Value},
	}
}

func attributes(attrs transform.Attributes) []*commonpb.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]*commonpb.KeyValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, &commonpb.KeyValue{
			Key:   key,
			Value: anyValue(attrs[key]),
		})
	}
	return out
}

func anyValue(value any) *commonpb.AnyValue {
	switch v := value.(type) {
	case bool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}
	case int:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(v)}}
	case int8:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(v)}}
	case int16:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(v)}}
	case int32:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(v)}}
	case int64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}
	case uint:
		return uintValue(uint64(v))
	case uint8:
		return uintValue(uint64(v))
	case uint16:
		return uintValue(uint64(v))
	case uint32:
		return uintValue(uint64(v))
	case uint64:
		return uintValue(v)
	case float32:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: float64(v)}}
	case float64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v}}
	case []byte:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: append([]byte(nil), v...)}}
	}
	if value, ok := jsonNumberValue(value); ok {
		return value
	}
	if value, ok := mapAnyValue(value); ok {
		return value
	}
	if value, ok := arrayAnyValue(value); ok {
		return value
	}
	if value, ok := structAnyValue(value); ok {
		return value
	}
	return stringValue(fmt.Sprint(value))
}

func jsonNumberValue(value any) (*commonpb.AnyValue, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return nil, false
	}
	if i, err := number.Int64(); err == nil {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: i}}, true
	}
	if f, err := number.Float64(); err == nil {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: f}}, true
	}
	return stringValue(number.String()), true
}

func mapAnyValue(value any) (*commonpb.AnyValue, bool) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	keys := make([]string, 0, rv.Len())
	for _, key := range rv.MapKeys() {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)

	values := make([]*commonpb.KeyValue, 0, len(keys))
	for _, key := range keys {
		mapKey := reflect.ValueOf(key).Convert(rv.Type().Key())
		values = append(values, &commonpb.KeyValue{
			Key:   key,
			Value: anyValue(rv.MapIndex(mapKey).Interface()),
		})
	}
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{
		KvlistValue: &commonpb.KeyValueList{Values: values},
	}}, true
}

func arrayAnyValue(value any) (*commonpb.AnyValue, bool) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, false
	}
	values := make([]*commonpb.AnyValue, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		values = append(values, anyValue(rv.Index(i).Interface()))
	}
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{
		ArrayValue: &commonpb.ArrayValue{Values: values},
	}}, true
}

func structAnyValue(value any) (*commonpb.AnyValue, bool) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return nil, false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil, false
	}
	return mapAnyValue(fields)
}

func stringValue(value string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}
}

func uintValue(value uint64) *commonpb.AnyValue {
	if value <= math.MaxInt64 {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(value)}}
	}
	return stringValue(fmt.Sprint(value))
}

func metricsEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", errors.New("OTEL_EXPORT_URL deve ser informado para exportar metricas")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("OTEL_EXPORT_URL invalido %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("OTEL_EXPORT_URL invalido %q: informe scheme e host", baseURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/metrics"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
