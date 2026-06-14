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
	"google.golang.org/protobuf/proto"
)

const (
	exampleRACID      = "240D79C7320E221DE06400144FFBE115"
	exampleDatabaseID = "3107639B0159F1634DCC581E477241AA"
)

func TestRuntimeIntegrationWithHTTPMockAndExampleConfigs(t *testing.T) {
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
	if !strings.Contains(output.String(), "coleta iniciada com 4 jobs") {
		t.Fatalf("expected startup message for example configs, got %q", output.String())
	}
}

type integrationMock struct {
	mu               sync.Mutex
	cancel           context.CancelFunc
	metricsPosts     int
	logsPosts        int
	metricDatapoints int
	logRecords       int
	latestData       map[string]int
	decodeErrors     []string
}

type integrationSnapshot struct {
	metricsPosts     int
	logsPosts        int
	metricDatapoints int
	logRecords       int
	latestData       map[string]int
	decodeErrors     []string
}

func newIntegrationMock() *integrationMock {
	return &integrationMock{latestData: make(map[string]int)}
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
		metricsPosts:     m.metricsPosts,
		logsPosts:        m.logsPosts,
		metricDatapoints: m.metricDatapoints,
		logRecords:       m.logRecords,
		latestData:       latestData,
		decodeErrors:     append([]string(nil), m.decodeErrors...),
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
		writeJSON(w, `{"items":[],"links":{}}`)
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
	count := 0
	if err != nil {
		m.recordDecodeError("metrics", err)
	} else if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-protobuf") {
		m.recordDecodeError("metrics", fmt.Errorf("unexpected content type %q", r.Header.Get("Content-Type")))
	} else {
		count, err = countMetricDatapoints(body)
		if err != nil {
			m.recordDecodeError("metrics", err)
		}
	}

	m.mu.Lock()
	m.metricsPosts++
	m.metricDatapoints += count
	shouldCancel := m.metricsPosts > 0 && m.logsPosts > 0 && m.cancel != nil
	cancel := m.cancel
	m.mu.Unlock()
	if shouldCancel {
		cancel()
	}
}

func (m *integrationMock) recordLogsPost(r *http.Request) {
	body, err := io.ReadAll(r.Body)
	count := 0
	if err != nil {
		m.recordDecodeError("logs", err)
	} else if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-protobuf") {
		m.recordDecodeError("logs", fmt.Errorf("unexpected content type %q", r.Header.Get("Content-Type")))
	} else {
		count, err = countLogRecords(body)
		if err != nil {
			m.recordDecodeError("logs", err)
		}
	}

	m.mu.Lock()
	m.logsPosts++
	m.logRecords += count
	shouldCancel := m.metricsPosts > 0 && m.logsPosts > 0 && m.cancel != nil
	cancel := m.cancel
	m.mu.Unlock()
	if shouldCancel {
		cancel()
	}
}

func (m *integrationMock) recordDecodeError(signal string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decodeErrors = append(m.decodeErrors, signal+": "+err.Error())
}

func countMetricDatapoints(body []byte) (int, error) {
	var request collectormetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &request); err != nil {
		return 0, err
	}
	total := 0
	for _, resource := range request.ResourceMetrics {
		for _, scope := range resource.ScopeMetrics {
			for _, metric := range scope.Metrics {
				total += len(metric.GetGauge().DataPoints)
			}
		}
	}
	return total, nil
}

func countLogRecords(body []byte) (int, error) {
	var request collectorlogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &request); err != nil {
		return 0, err
	}
	total := 0
	for _, resource := range request.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			total += len(scope.LogRecords)
		}
	}
	return total, nil
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
