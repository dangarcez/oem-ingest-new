package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"oem-ingest-new/internal/app"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

const (
	exampleRACID      = "240D79C7320E221DE06400144FFBE115"
	exampleDatabaseID = "3107639B0159F1634DCC581E477241AA"
	exampleIncidentID = "INC-LEGACY-1"
)

func TestRuntimeIntegrationWithHTTPMockAndExampleConfigs(t *testing.T) {
	snapshot, outputText := runRuntimeIntegrationWithHTTPMock(t)
	if len(snapshot.decodeErrors) > 0 {
		t.Fatalf("failed to decode OTLP payloads: %v", snapshot.decodeErrors)
	}
	if snapshot.metricsPosts == 0 {
		t.Fatal("expected at least one POST to /v1/metrics")
	}
	if snapshot.logsPosts == 0 {
		t.Fatal("expected at least one POST to /v1/logs")
	}
	if snapshot.metricDatapoints == 0 {
		t.Fatal("expected exported metrics payload to contain datapoints")
	}
	if snapshot.logRecords == 0 {
		t.Fatal("expected exported logs payload to contain records")
	}
	assertObserved(t, snapshot.metricServiceNames, "oemAPIService", "metrics resource service.name")
	assertObserved(t, snapshot.logServiceNames, "oemAPIService", "logs resource service.name")
	for _, name := range []string{
		"oem_availability_status",
		"oem_service_performance_dbtime_delta",
		"oem_response_status",
		"oem_instance_throughput_callspersec",
		"oem_monitor_stus",
		"oem_monitor_response",
		"oem_service_status",
		"oem_collector_targets_configured",
	} {
		assertObserved(t, snapshot.metricNames, name, "metric name")
	}
	assertMetricAttributes(t, snapshot, "oem_instance_throughput_callspersec", map[string]string{
		"_instance":       "occp40bc3",
		"target_name":     "occp40bc_occp40bc3",
		"target_type":     "oracle_database",
		"oracle_database": "occp40bc_occp40bc3",
	})
	assertMetricAttributes(t, snapshot, "oem_service_status", map[string]string{
		"name_":       "svc_app",
		"dbname":      "occp40bc",
		"target_name": "occp40bc",
		"target_type": "rac_database",
	})
	for _, name := range []string{
		"oem_response_databasestatus",
		"oem_service_performance_status",
		"oem_str_service_status",
	} {
		assertLogMetric(t, snapshot, name)
	}
	assertLogAttributes(t, snapshot, "oem_str_service_status", map[string]string{
		"name_":  "svc_app",
		"dbname": "occp40bc",
	}, "ativo")
	for _, path := range []string{
		"/em/api/targets/" + exampleRACID + "/metricGroups/Availability/latestData",
		"/em/api/targets/" + exampleRACID + "/metricGroups/service_performance/latestData",
		"/em/api/targets/" + exampleDatabaseID + "/metricGroups/Response/latestData",
		"/em/api/targets/" + exampleDatabaseID + "/metricGroups/instance_throughput/latestData",
	} {
		if snapshot.latestData[path] == 0 {
			t.Fatalf("expected at least one latestData call to %s; got %#v", path, snapshot.latestData)
		}
	}
	if !strings.Contains(outputText, "coleta iniciada com 4 jobs") {
		t.Fatalf("expected startup message for example configs, got %q", outputText)
	}
}

func TestLegacyCompatibilityComparisonWithHTTPMockAndExampleConfigs(t *testing.T) {
	snapshot, _ := runRuntimeIntegrationWithHTTPMock(t)
	if len(snapshot.decodeErrors) > 0 {
		t.Fatalf("failed to decode OTLP payloads: %v", snapshot.decodeErrors)
	}

	assertObserved(t, snapshot.metricServiceNames, "oemAPIService", "metrics resource service.name")
	assertObserved(t, snapshot.logServiceNames, "oemAPIService", "logs resource service.name")
	assertObservedKeysLowercase(t, snapshot.metricNames, "metric names")
	assertObservedLogMetricsLowercase(t, snapshot.logMetrics)

	for _, name := range []string{
		"oem_availability_status",
		"oem_service_performance_dbtime_delta",
		"oem_response_status",
		"oem_instance_throughput_callspersec",
		"oem_monitor_stus",
		"oem_monitor_response",
		"oem_service_status",
	} {
		assertObserved(t, snapshot.metricNames, name, "legacy-compatible metric name")
	}

	assertMetricAttributes(t, snapshot, "oem_availability_status", map[string]string{
		"target_name": "occp40bc",
		"target_type": "rac_database",
		"sistema":     "siapx",
		"torre":       "cartoes",
	})
	assertMetricAttributes(t, snapshot, "oem_instance_throughput_callspersec", map[string]string{
		"_instance":    "occp40bc3",
		"target_name":  "occp40bc_occp40bc3",
		"target_type":  "oracle_database",
		"machine_name": "cadecrk01cl01vm03",
		"sistema":      "siapx",
		"torre":        "cartoes",
	})
	assertMetricAttributes(t, snapshot, "oem_service_status", map[string]string{
		"name_":       "svc_app",
		"dbname":      "occp40bc",
		"target_name": "occp40bc",
		"target_type": "rac_database",
		"sistema":     "siapx",
	})

	assertLogObservation(t, snapshot, "oem_response_databasestatus", "ACTIVE", "INFO", map[string]string{
		"metric":      "oem_response_databasestatus",
		"target_name": "occp40bc_occp40bc3",
		"target_type": "oracle_database",
	})
	assertLogObservation(t, snapshot, "oem_service_performance_status", "Up", "INFO", map[string]string{
		"metric": "oem_service_performance_status",
		"name_":  "svc_app",
		"dbname": "occp40bc",
	})
	assertLogObservation(t, snapshot, "oem_str_service_status", "ativo", "INFO", map[string]string{
		"metric": "oem_str_service_status",
		"name_":  "svc_app",
		"dbname": "occp40bc",
	})
	incidentLog := assertLogObservation(t, snapshot, "oem_incident", "Database target is down", "WARN", map[string]string{
		"metric":      "oem_incident",
		"id":          exampleIncidentID,
		"timeCreated": "2026-06-14T09:30:15.123Z",
		"timeUpdated": "2026-06-14T09:45:16.789Z",
		"target_id":   exampleRACID,
		"target_name": "occp40bc",
		"target_type": "rac_database",
		"priority":    "High",
	})
	assertLogTimestamp(t, incidentLog, "2026-06-14T09:30:15.123456Z", "incident log timestamp")
}

func runRuntimeIntegrationWithHTTPMock(t *testing.T) (integrationSnapshot, string) {
	t.Helper()
	mock := newIntegrationMock()
	server := httptest.NewServer(http.HandlerFunc(mock.handle))
	defer server.Close()

	tmp := t.TempDir()
	root := projectRoot(t)
	targetsPath := copyExampleConfig(t, root, tmp, "configTargets.example.yaml", map[string]string{
		"http://localhost:8008": server.URL,
	})
	metricsPath := copyExampleConfig(t, root, tmp, "configMetrics.example.yaml", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	mock.setCancel(cancel)

	var output bytes.Buffer
	err := app.Run(ctx, app.Options{
		Output: &output,
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":               targetsPath,
			"OEM_CONFIG_METRICS":               metricsPath,
			"OEM_USER":                         "user",
			"OEM_PASSWORD":                     "secret",
			"OTEL_EXPORT_URL":                  server.URL,
			"OEM_EXPORT_INTERVAL_SECONDS":      "1",
			"OEM_HTTP_MAX_RETRIES":             "0",
			"OEM_HTTP_TIMEOUT_SECONDS":         "2",
			"OEM_HTTP_CONNECT_TIMEOUT_SECONDS": "2",
		}),
		Logger: testLogger{t: t},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	snapshot := mock.snapshot()
	return snapshot, output.String()
}

type integrationMock struct {
	mu                 sync.Mutex
	cancel             context.CancelFunc
	metricsPosts       int
	logsPosts          int
	metricDatapoints   int
	logRecords         int
	latestData         map[string]int
	metricServiceNames map[string]int
	logServiceNames    map[string]int
	metricNames        map[string]int
	metricAttributes   map[string][]map[string]string
	logMetrics         map[string][]logObservation
	decodeErrors       []string
}

type integrationSnapshot struct {
	metricsPosts       int
	logsPosts          int
	metricDatapoints   int
	logRecords         int
	latestData         map[string]int
	metricServiceNames map[string]int
	logServiceNames    map[string]int
	metricNames        map[string]int
	metricAttributes   map[string][]map[string]string
	logMetrics         map[string][]logObservation
	decodeErrors       []string
}

func newIntegrationMock() *integrationMock {
	return &integrationMock{
		latestData:         make(map[string]int),
		metricServiceNames: make(map[string]int),
		logServiceNames:    make(map[string]int),
		metricNames:        make(map[string]int),
		metricAttributes:   make(map[string][]map[string]string),
		logMetrics:         make(map[string][]logObservation),
	}
}

func (m *integrationMock) setCancel(cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancel = cancel
}

func (m *integrationMock) snapshot() integrationSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	latestData := make(map[string]int, len(m.latestData))
	for key, value := range m.latestData {
		latestData[key] = value
	}
	return integrationSnapshot{
		metricsPosts:       m.metricsPosts,
		logsPosts:          m.logsPosts,
		metricDatapoints:   m.metricDatapoints,
		logRecords:         m.logRecords,
		latestData:         latestData,
		metricServiceNames: cloneStringCounts(m.metricServiceNames),
		logServiceNames:    cloneStringCounts(m.logServiceNames),
		metricNames:        cloneStringCounts(m.metricNames),
		metricAttributes:   cloneMetricAttributes(m.metricAttributes),
		logMetrics:         cloneLogMetrics(m.logMetrics),
		decodeErrors:       append([]string(nil), m.decodeErrors...),
	}
}

func (m *integrationMock) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/em/") {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	switch r.URL.Path {
	case "/v1/metrics":
		m.recordMetricsPost(r)
		writeJSON(w, `{"accepted":true}`)
	case "/v1/logs":
		m.recordLogsPost(r)
		writeJSON(w, `{"accepted":true}`)
	case "/em/api":
		writeJSON(w, `{"name":"oem-mock","version":"integration"}`)
	case "/em/api/incidents/":
		writeJSON(w, `{"items":[{"id":"`+exampleIncidentID+`","displayId":1001,"message":"Database target is down","targets":[{"id":"`+exampleRACID+`","name":"occp40bc","typeName":"rac_database","typeDisplayName":"Cluster Database"}],"timeCreated":"2026-06-14T12:30:15.123456Z","timeUpdated":"2026-06-14T12:45:16.789123Z","ageInHours":0.25,"isOpen":true,"status":"Open","owner":"SYSMAN","severity":"Critical","priority":"High"}],"links":{}}`)
	case "/em/api/incidents/" + exampleIncidentID:
		writeJSON(w, `{"id":"`+exampleIncidentID+`","status":"Open"}`)
	case "/em/api/targets/" + exampleRACID + "/metricGroups/Availability":
		writeJSON(w, `{"name":"Availability","keys":[],"metrics":[{"name":"Status","dataType":"NUMBER"}]}`)
	case "/em/api/targets/" + exampleRACID + "/metricGroups/Availability/latestData":
		m.recordLatestData(r.URL.Path)
		writeJSON(w, `{"metricGroupName":"Availability","targetId":"`+exampleRACID+`","items":[{"Status":1}],"links":{}}`)
	case "/em/api/targets/" + exampleRACID + "/metricGroups/service_performance":
		writeJSON(w, `{"name":"service_performance","keys":[{"name":"name"},{"name":"dbname"}],"metrics":[{"name":"DBTime_delta","dataType":"NUMBER"},{"name":"status","dataType":"STRING"}]}`)
	case "/em/api/targets/" + exampleRACID + "/metricGroups/service_performance/latestData":
		m.recordLatestData(r.URL.Path)
		writeJSON(w, `{"metricGroupName":"service_performance","targetId":"`+exampleRACID+`","items":[{"name":"svc_app","dbname":"occp40bc","DBTime_delta":3.5,"status":"Up"}],"links":{}}`)
	case "/em/api/targets/" + exampleDatabaseID + "/metricGroups/Response":
		writeJSON(w, `{"name":"Response","keys":[],"metrics":[{"name":"Status","dataType":"NUMBER"},{"name":"DatabaseStatus","dataType":"STRING"}]}`)
	case "/em/api/targets/" + exampleDatabaseID + "/metricGroups/Response/latestData":
		m.recordLatestData(r.URL.Path)
		writeJSON(w, `{"metricGroupName":"Response","targetId":"`+exampleDatabaseID+`","items":[{"Status":1,"DatabaseStatus":"ACTIVE"}],"links":{}}`)
	case "/em/api/targets/" + exampleDatabaseID + "/metricGroups/instance_throughput":
		writeJSON(w, `{"name":"instance_throughput","keys":[{"name":"instance"}],"metrics":[{"name":"callsPerSec","dataType":"NUMBER"}]}`)
	case "/em/api/targets/" + exampleDatabaseID + "/metricGroups/instance_throughput/latestData":
		m.recordLatestData(r.URL.Path)
		writeJSON(w, `{"metricGroupName":"instance_throughput","targetId":"`+exampleDatabaseID+`","items":[{"instance":"occp40bc3","callsPerSec":42}],"links":{}}`)
	default:
		http.NotFound(w, r)
	}
}

func (m *integrationMock) recordLatestData(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latestData[path]++
}

func (m *integrationMock) recordMetricsPost(r *http.Request) {
	body, err := io.ReadAll(r.Body)
	decoded := decodedMetrics{}
	if err != nil {
		m.recordDecodeError("metrics", err)
	} else if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-protobuf") {
		m.recordDecodeError("metrics", fmt.Errorf("unexpected content type %q", r.Header.Get("Content-Type")))
	} else {
		decoded, err = decodeMetricPayload(body)
		if err != nil {
			m.recordDecodeError("metrics", err)
		}
	}

	m.mu.Lock()
	m.metricsPosts++
	m.metricDatapoints += decoded.datapoints
	mergeCounts(m.metricServiceNames, decoded.serviceNames)
	mergeCounts(m.metricNames, decoded.metricNames)
	mergeMetricAttributes(m.metricAttributes, decoded.attributes)
	shouldCancel := m.readyToCancelLocked()
	cancel := m.cancel
	m.mu.Unlock()
	if shouldCancel {
		cancel()
	}
}

func (m *integrationMock) recordLogsPost(r *http.Request) {
	body, err := io.ReadAll(r.Body)
	decoded := decodedLogs{}
	if err != nil {
		m.recordDecodeError("logs", err)
	} else if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-protobuf") {
		m.recordDecodeError("logs", fmt.Errorf("unexpected content type %q", r.Header.Get("Content-Type")))
	} else {
		decoded, err = decodeLogsPayload(body)
		if err != nil {
			m.recordDecodeError("logs", err)
		}
	}

	m.mu.Lock()
	m.logsPosts++
	m.logRecords += decoded.records
	mergeCounts(m.logServiceNames, decoded.serviceNames)
	mergeLogMetrics(m.logMetrics, decoded.metrics)
	shouldCancel := m.readyToCancelLocked()
	cancel := m.cancel
	m.mu.Unlock()
	if shouldCancel {
		cancel()
	}
}

func (m *integrationMock) readyToCancelLocked() bool {
	return m.cancel != nil && m.metricsPosts > 0 && m.logsPosts > 0 && len(m.logMetrics["oem_incident"]) > 0
}

func (m *integrationMock) recordDecodeError(signal string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decodeErrors = append(m.decodeErrors, signal+": "+err.Error())
}

type decodedMetrics struct {
	datapoints   int
	serviceNames map[string]int
	metricNames  map[string]int
	attributes   map[string][]map[string]string
}

func decodeMetricPayload(body []byte) (decodedMetrics, error) {
	var request collectormetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &request); err != nil {
		return decodedMetrics{}, err
	}
	decoded := decodedMetrics{
		serviceNames: make(map[string]int),
		metricNames:  make(map[string]int),
		attributes:   make(map[string][]map[string]string),
	}
	for _, resource := range request.ResourceMetrics {
		if serviceName := attributesMap(resource.GetResource().GetAttributes())["service.name"]; serviceName != "" {
			decoded.serviceNames[serviceName]++
		}
		for _, scope := range resource.ScopeMetrics {
			for _, metric := range scope.Metrics {
				name := metric.GetName()
				decoded.metricNames[name] += len(metric.GetGauge().DataPoints)
				for _, point := range metric.GetGauge().DataPoints {
					decoded.datapoints++
					decoded.attributes[name] = append(decoded.attributes[name], attributesMap(point.GetAttributes()))
				}
			}
		}
	}
	return decoded, nil
}

type logObservation struct {
	Body         string
	SeverityText string
	TimeUnixNano uint64
	Attributes   map[string]string
}

type decodedLogs struct {
	records      int
	serviceNames map[string]int
	metrics      map[string][]logObservation
}

func decodeLogsPayload(body []byte) (decodedLogs, error) {
	var request collectorlogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &request); err != nil {
		return decodedLogs{}, err
	}
	decoded := decodedLogs{
		serviceNames: make(map[string]int),
		metrics:      make(map[string][]logObservation),
	}
	for _, resource := range request.ResourceLogs {
		if serviceName := attributesMap(resource.GetResource().GetAttributes())["service.name"]; serviceName != "" {
			decoded.serviceNames[serviceName]++
		}
		for _, scope := range resource.ScopeLogs {
			for _, record := range scope.LogRecords {
				decoded.records++
				attrs := attributesMap(record.GetAttributes())
				metricName := attrs["metric"]
				if metricName == "" {
					metricName = "<missing>"
				}
				decoded.metrics[metricName] = append(decoded.metrics[metricName], logObservation{
					Body:         anyValueString(record.GetBody()),
					SeverityText: record.GetSeverityText(),
					TimeUnixNano: record.GetTimeUnixNano(),
					Attributes:   attrs,
				})
			}
		}
	}
	return decoded, nil
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

func copyExampleConfig(t *testing.T, root, tmp, name string, replacements map[string]string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "configs", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	text := string(data)
	for oldValue, newValue := range replacements {
		text = strings.ReplaceAll(text, oldValue, newValue)
	}

	outputName := strings.TrimSuffix(name, ".example.yaml") + ".yaml"
	path := filepath.Join(tmp, outputName)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

type testLogger struct {
	t *testing.T
}

func (l testLogger) InfoContext(context.Context, string, ...any) {}

func (l testLogger) WarnContext(_ context.Context, msg string, args ...any) {
	l.t.Logf("warn: %s %v", msg, args)
}

func (l testLogger) ErrorContext(_ context.Context, msg string, args ...any) {
	l.t.Logf("error: %s %v", msg, args)
}

func attributesMap(attrs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[attr.GetKey()] = anyValueString(attr.GetValue())
	}
	return out
}

func anyValueString(value *commonpb.AnyValue) string {
	if value == nil {
		return ""
	}
	switch v := value.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_BoolValue:
		if v.BoolValue {
			return "true"
		}
		return "false"
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprint(v.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprint(v.DoubleValue)
	case *commonpb.AnyValue_BytesValue:
		return string(v.BytesValue)
	default:
		return ""
	}
}

func cloneStringCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneMetricAttributes(in map[string][]map[string]string) map[string][]map[string]string {
	out := make(map[string][]map[string]string, len(in))
	for key, attrs := range in {
		out[key] = cloneAttributesList(attrs)
	}
	return out
}

func cloneLogMetrics(in map[string][]logObservation) map[string][]logObservation {
	out := make(map[string][]logObservation, len(in))
	for key, records := range in {
		out[key] = make([]logObservation, 0, len(records))
		for _, record := range records {
			out[key] = append(out[key], logObservation{
				Body:         record.Body,
				SeverityText: record.SeverityText,
				TimeUnixNano: record.TimeUnixNano,
				Attributes:   cloneAttributes(record.Attributes),
			})
		}
	}
	return out
}

func cloneAttributesList(in []map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(in))
	for _, attrs := range in {
		out = append(out, cloneAttributes(attrs))
	}
	return out
}

func cloneAttributes(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeCounts(dst, src map[string]int) {
	for key, value := range src {
		dst[key] += value
	}
}

func mergeMetricAttributes(dst, src map[string][]map[string]string) {
	for name, attrs := range src {
		dst[name] = append(dst[name], cloneAttributesList(attrs)...)
	}
}

func mergeLogMetrics(dst, src map[string][]logObservation) {
	for name, records := range src {
		for _, record := range records {
			dst[name] = append(dst[name], logObservation{
				Body:         record.Body,
				SeverityText: record.SeverityText,
				TimeUnixNano: record.TimeUnixNano,
				Attributes:   cloneAttributes(record.Attributes),
			})
		}
	}
}

func assertObserved(t *testing.T, counts map[string]int, key, label string) {
	t.Helper()
	if counts[key] == 0 {
		t.Fatalf("expected %s %q; observed %#v", label, key, counts)
	}
}

func assertMetricAttributes(t *testing.T, snapshot integrationSnapshot, metricName string, expected map[string]string) {
	t.Helper()
	for _, attrs := range snapshot.metricAttributes[metricName] {
		if attributesContain(attrs, expected) {
			return
		}
	}
	t.Fatalf("expected metric %q with attributes %#v; observed %#v", metricName, expected, snapshot.metricAttributes[metricName])
}

func assertLogMetric(t *testing.T, snapshot integrationSnapshot, metricName string) {
	t.Helper()
	if len(snapshot.logMetrics[metricName]) == 0 {
		t.Fatalf("expected log metric %q; observed %#v", metricName, snapshot.logMetrics)
	}
}

func assertLogAttributes(t *testing.T, snapshot integrationSnapshot, metricName string, expected map[string]string, body string) {
	t.Helper()
	for _, record := range snapshot.logMetrics[metricName] {
		if record.Body == body && attributesContain(record.Attributes, expected) {
			return
		}
	}
	t.Fatalf("expected log metric %q with body %q and attributes %#v; observed %#v", metricName, body, expected, snapshot.logMetrics[metricName])
}

func assertLogObservation(t *testing.T, snapshot integrationSnapshot, metricName, body, severity string, expected map[string]string) logObservation {
	t.Helper()
	for _, record := range snapshot.logMetrics[metricName] {
		if record.Body == body && record.SeverityText == severity && attributesContain(record.Attributes, expected) {
			return record
		}
	}
	t.Fatalf("expected log metric %q with body %q, severity %q and attributes %#v; observed %#v", metricName, body, severity, expected, snapshot.logMetrics[metricName])
	return logObservation{}
}

func assertLogTimestamp(t *testing.T, record logObservation, want string, label string) {
	t.Helper()
	expected, err := time.Parse(time.RFC3339Nano, want)
	if err != nil {
		t.Fatalf("invalid expected timestamp %q: %v", want, err)
	}
	expectedNano := uint64(expected.UnixNano())
	if record.TimeUnixNano != expectedNano {
		observed := time.Unix(0, int64(record.TimeUnixNano)).UTC().Format(time.RFC3339Nano)
		t.Fatalf("%s = %d (%s), want %d (%s)", label, record.TimeUnixNano, observed, expectedNano, expected.UTC().Format(time.RFC3339Nano))
	}
}

func assertObservedKeysLowercase(t *testing.T, counts map[string]int, label string) {
	t.Helper()
	for key := range counts {
		if key != strings.ToLower(key) {
			t.Fatalf("%s contain non-lowercase key %q; observed %#v", label, key, counts)
		}
	}
}

func assertObservedLogMetricsLowercase(t *testing.T, logs map[string][]logObservation) {
	t.Helper()
	for key := range logs {
		if key == "<missing>" {
			t.Fatalf("logs contain records without metric attribute; observed %#v", logs[key])
		}
		if key != strings.ToLower(key) {
			t.Fatalf("log metric names contain non-lowercase key %q; observed %#v", key, logs)
		}
	}
}

func attributesContain(attrs, expected map[string]string) bool {
	for key, value := range expected {
		if attrs[key] != value {
			return false
		}
	}
	return true
}
