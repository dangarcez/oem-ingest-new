package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oem-ingest-new/internal/auth"
	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/scheduler"
	"oem-ingest-new/internal/validate"
)

func TestRunDoesNotRequireExternalServices(t *testing.T) {
	var output bytes.Buffer

	err := Run(context.Background(), Options{Output: &output, LookupEnv: emptyLookup})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(output.String(), "scaffold inicializado") {
		t.Fatalf("expected scaffold message, got %q", output.String())
	}
}

func TestRunRejectsInvalidConfiguredLogLevel(t *testing.T) {
	var output, logs bytes.Buffer

	err := Run(context.Background(), Options{
		Output:    &output,
		LogOutput: &logs,
		LookupEnv: mapLookup(map[string]string{
			"OEM_LOG_LEVEL": "verbose",
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "OEM_LOG_LEVEL") {
		t.Fatalf("expected OEM_LOG_LEVEL error, got %v", err)
	}
	if output.Len() != 0 || logs.Len() != 0 {
		t.Fatalf("expected no output before app starts; output=%q logs=%q", output.String(), logs.String())
	}
}

func TestRunUsesConfiguredLogLevelForDefaultLogger(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantLog bool
	}{
		{name: "info emits validation summary", level: "INFO", wantLog: true},
		{name: "error filters validation summary", level: "ERROR", wantLog: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetsPath := writeTargetsFile(t, "configured-id")
			validatedPath := filepath.Join(t.TempDir(), "validated", "configTargets.yaml")
			var output, logs bytes.Buffer

			err := Run(context.Background(), Options{
				Output:    &output,
				LogOutput: &logs,
				LookupEnv: mapLookup(map[string]string{
					"OEM_VALIDATE_CONFIG":         "true",
					"OEM_CONFIG_TARGETS":          targetsPath,
					"OEM_VALIDATED_CONFIG_OUTPUT": validatedPath,
					"OEM_LOG_LEVEL":               tt.level,
				}),
				TargetInventoryFactory: func(config.SiteConfig) (validate.TargetInventory, error) {
					return appFakeTargetLister{
						targets: []oem.Target{{ID: "configured-id", Name: "cdbp51bc", TypeName: "rac_database"}},
					}, nil
				},
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			hasSummaryLog := strings.Contains(logs.String(), "configuracao validada escrita")
			if hasSummaryLog != tt.wantLog {
				t.Fatalf("summary log presence = %t, want %t; logs=%q", hasSummaryLog, tt.wantLog, logs.String())
			}
		})
	}
}

func TestSchedulerOptionsUsesConfiguredJitter(t *testing.T) {
	opts := schedulerOptions(config.Env{SchedulerJitter: 12 * time.Second}, nil)
	if opts.Jitter != 12*time.Second {
		t.Fatalf("Jitter = %s", opts.Jitter)
	}
}

func TestSchedulerOptionsDisablesZeroJitter(t *testing.T) {
	opts := schedulerOptions(config.Env{SchedulerJitter: 0}, nil)
	if opts.Jitter >= 0 {
		t.Fatalf("Jitter = %s, want negative duration to disable scheduler jitter", opts.Jitter)
	}
}

func TestRunCollectsAndExportsWhenOTELExportURLConfigured(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
  - freq: 5
    metric_group_name: TextGroup
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var metricsPosts atomic.Int32
	var logsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/targets/t1/metricGroups/TextGroup":
			fmt.Fprint(w, `{"name":"TextGroup","keys":[{"name":"component"}],"metrics":[{"name":"state","dataType":"STRING"}]}`)
		case "/em/api/targets/t1/metricGroups/TextGroup/latestData":
			fmt.Fprint(w, `{"items":[{"component":"listener","state":"OPEN"}]}`)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			fmt.Fprint(w, `{"accepted":true}`)
		case "/v1/logs":
			logsPosts.Add(1)
			fmt.Fprint(w, `{"accepted":true}`)
		default:
			http.NotFound(w, r)
			return
		}
		if cancel != nil && metricsPosts.Load() > 0 && logsPosts.Load() > 0 {
			cancel()
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	var output bytes.Buffer
	err = Run(ctx, Options{
		Output: &output,
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_CONFIG_METRICS":          metricsPath,
			"OEM_USER":                    "user",
			"OEM_PASSWORD":                "secret",
			"OTEL_EXPORT_URL":             server.URL,
			"OEM_EXPORT_INTERVAL_SECONDS": "1",
		}),
		Logger: &appRecordingLogger{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected at least one OTLP metrics POST")
	}
	if logsPosts.Load() == 0 {
		t.Fatal("expected at least one OTLP logs POST")
	}
	if !strings.Contains(output.String(), "coleta iniciada com 3 jobs") {
		t.Fatalf("expected collection startup message, got %q", output.String())
	}
}

func TestRunPollsIncidentsWhenRuntimeStarts(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var metricsPosts atomic.Int32
	var logsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[{"id":"inc-1","message":"incidente aberto","timeCreated":"2025-07-24T20:53:48.000Z","status":"new","severity":"Critical","targets":[{"id":"t1","name":"host1","typeName":"host"}]}]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			fmt.Fprint(w, `{"accepted":true}`)
		case "/v1/logs":
			logsPosts.Add(1)
			fmt.Fprint(w, `{"accepted":true}`)
		default:
			http.NotFound(w, r)
			return
		}
		if cancel != nil && metricsPosts.Load() > 0 && logsPosts.Load() > 0 {
			cancel()
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	var output bytes.Buffer
	err = Run(ctx, Options{
		Output: &output,
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_CONFIG_METRICS":          metricsPath,
			"OEM_USER":                    "user",
			"OEM_PASSWORD":                "secret",
			"OTEL_EXPORT_URL":             server.URL,
			"OEM_EXPORT_INTERVAL_SECONDS": "1",
		}),
		Logger: &appRecordingLogger{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected at least one OTLP metrics POST")
	}
	if logsPosts.Load() == 0 {
		t.Fatal("expected incident log POST")
	}
}

func TestRunContinuesWhenStartupAPIValidationTemporarilyFails(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var apiCalls atomic.Int32
	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			apiCalls.Add(1)
			http.Error(w, "oem temporarily unavailable", http.StatusServiceUnavailable)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			if cancel != nil {
				time.AfterFunc(10*time.Millisecond, cancel)
			}
		case "/v1/logs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	logger := &appRecordingLogger{}
	err = Run(ctx, Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":               targetsPath,
			"OEM_CONFIG_METRICS":               metricsPath,
			"OEM_USER":                         "user",
			"OEM_PASSWORD":                     "secret",
			"OTEL_EXPORT_URL":                  server.URL,
			"OEM_HTTP_MAX_RETRIES":             "0",
			"OEM_EXPORT_INTERVAL_SECONDS":      "60",
			"OEM_DIAGNOSTICS_INTERVAL_SECONDS": "60",
		}),
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if apiCalls.Load() == 0 {
		t.Fatal("expected startup API validation to be attempted")
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected collection/export to continue after temporary API validation failure")
	}
	if !logger.containsWarning("runtime continuara tentando") {
		t.Fatalf("expected recoverable API validation warning, got %#v", logger.warningsSnapshot())
	}
}

func TestRunAcceptsStartupAPIValidationNotFound(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var apiCalls atomic.Int32
	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			apiCalls.Add(1)
			http.NotFound(w, r)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			if cancel != nil {
				time.AfterFunc(10*time.Millisecond, cancel)
			}
		case "/v1/logs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	logger := &appRecordingLogger{}
	err = Run(ctx, Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":               targetsPath,
			"OEM_CONFIG_METRICS":               metricsPath,
			"OEM_USER":                         "user",
			"OEM_PASSWORD":                     "secret",
			"OTEL_EXPORT_URL":                  server.URL,
			"OEM_HTTP_MAX_RETRIES":             "0",
			"OEM_EXPORT_INTERVAL_SECONDS":      "60",
			"OEM_DIAGNOSTICS_INTERVAL_SECONDS": "60",
		}),
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if apiCalls.Load() == 0 {
		t.Fatal("expected startup API validation to be attempted")
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected collection/export to continue after /em/api returned 404")
	}
	if logger.containsWarning("runtime continuara tentando") {
		t.Fatalf("404 should be accepted as a successful connection check, got warnings %#v", logger.warningsSnapshot())
	}
	if !logger.containsInfo("diagnostico runtime") {
		t.Fatalf("expected runtime diagnostics log, got %#v", logger.infos)
	}
}

func TestRunFailsFastWhenStartupAPIValidationReturnsUnauthorized(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var metricsPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/em/api" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/v1/metrics" {
			metricsPosts.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	err = Run(context.Background(), Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":   targetsPath,
			"OEM_CONFIG_METRICS":   metricsPath,
			"OEM_USER":             "user",
			"OEM_PASSWORD":         "secret",
			"OTEL_EXPORT_URL":      server.URL,
			"OEM_HTTP_MAX_RETRIES": "0",
		}),
		Logger: &appRecordingLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "validar conexao OEM") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected unauthorized startup validation error, got %v", err)
	}
	if metricsPosts.Load() != 0 {
		t.Fatalf("metrics POSTs = %d, want no runtime export after unauthorized startup validation", metricsPosts.Load())
	}
}

func TestRunContinuesWhenInitialCollectionsFail(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: custom_target
      tags:
        target_name: host1
        target_type: custom_target
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
custom_target:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var latestDataCalls atomic.Int32
	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			latestDataCalls.Add(1)
			http.Error(w, "oem temporarily unavailable", http.StatusServiceUnavailable)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			fmt.Fprint(w, `{"accepted":true}`)
			if cancel != nil {
				time.AfterFunc(10*time.Millisecond, cancel)
			}
		case "/v1/logs":
			fmt.Fprint(w, `{"accepted":true}`)
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	logger := &appRecordingLogger{}
	err = Run(ctx, Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_CONFIG_METRICS":          metricsPath,
			"OEM_USER":                    "user",
			"OEM_PASSWORD":                "secret",
			"OTEL_EXPORT_URL":             server.URL,
			"OEM_HTTP_MAX_RETRIES":        "0",
			"OEM_EXPORT_INTERVAL_SECONDS": "60",
		}),
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if latestDataCalls.Load() == 0 {
		t.Fatal("expected initial latestData collection to be attempted")
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected runtime self metrics to be exported despite initial collection failure")
	}
	if !logger.containsWarning("falha na coleta inicial") {
		t.Fatalf("expected initial collection failure warning, got %#v", logger.warningsSnapshot())
	}
	if !logger.containsWarning("scheduler continuara tentando") {
		t.Fatalf("expected recoverable startup warning, got %#v", logger.warningsSnapshot())
	}
}

func TestRunRetriesPendingMetricsDuringFinalFlush(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	var scheduleCancel sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			if metricsPosts.Add(1) == 1 {
				http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
				scheduleCancel.Do(func() {
					if cancel != nil {
						time.AfterFunc(20*time.Millisecond, cancel)
					}
				})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/logs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	logger := &appRecordingLogger{}
	err = Run(ctx, Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_CONFIG_METRICS":          metricsPath,
			"OEM_USER":                    "user",
			"OEM_PASSWORD":                "secret",
			"OTEL_EXPORT_URL":             server.URL,
			"OEM_HTTP_MAX_RETRIES":        "0",
			"OEM_EXPORT_INTERVAL_SECONDS": "60",
		}),
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if metricsPosts.Load() != 2 {
		t.Fatalf("metrics POSTs = %d, want failed initial export plus final flush retry", metricsPosts.Load())
	}
	if !logger.containsWarning("falha ao exportar metricas pendentes") {
		t.Fatalf("expected enriched pending metrics export warning, got %#v", logger.warningsSnapshot())
	}
}

func TestRunExportsRuntimeMetricsBeforeInitialCollectionFinishes(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: t1
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets/t1/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/t1/metricGroups/Load/latestData":
			time.Sleep(2 * time.Second)
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			if cancel != nil {
				cancel()
			}
		case "/v1/logs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	started := time.Now()
	err = Run(ctx, Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_CONFIG_METRICS":          metricsPath,
			"OEM_USER":                    "user",
			"OEM_PASSWORD":                "secret",
			"OTEL_EXPORT_URL":             server.URL,
			"OEM_HTTP_MAX_RETRIES":        "0",
			"OEM_EXPORT_INTERVAL_SECONDS": "60",
			"OEM_MAX_CONCURRENT_REQUESTS": "1",
		}),
		Logger: &appRecordingLogger{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected runtime self metrics to be exported before slow initial collection finishes")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run took %s; initial collection appears to be blocking startup export", elapsed)
	}
}

func TestRunReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, Options{LookupEnv: emptyLookup})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestRunValidatesTargetsWhenEnabled(t *testing.T) {
	targetsPath := writeTargetsFile(t, "stale-id")
	validatedPath := filepath.Join(t.TempDir(), "validated", "configTargets.yaml")
	var output bytes.Buffer
	logger := &appRecordingLogger{}
	factoryCalled := false

	factory := func(site config.SiteConfig) (validate.TargetInventory, error) {
		factoryCalled = true
		if site.Name != "oraemc" || site.Endpoint != "http://oem.example" {
			t.Fatalf("unexpected site passed to factory: %#v", site)
		}
		return appFakeTargetLister{
			targets: []oem.Target{{ID: "current-id", Name: "cdbp51bc", TypeName: "rac_database"}},
		}, nil
	}

	err := Run(context.Background(), Options{
		Output:                 &output,
		LookupEnv:              mapLookup(map[string]string{"OEM_VALIDATE_CONFIG": "true", "OEM_CONFIG_TARGETS": targetsPath, "OEM_VALIDATED_CONFIG_OUTPUT": validatedPath}),
		Logger:                 logger,
		TargetInventoryFactory: factory,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !factoryCalled {
		t.Fatal("expected validation factory to be called")
	}
	if !strings.Contains(output.String(), "validacao de configuracao concluida: 1 correcoes de ID, 0 targets removidos, 0 sites removidos, 0 targets adicionados, 1 tags corrigidas, 2 avisos") {
		t.Fatalf("expected validation summary, got %q", output.String())
	}
	if !strings.Contains(output.String(), "configuracao validada escrita em "+validatedPath) {
		t.Fatalf("expected validated config output path, got %q", output.String())
	}
	reportPath := config.DefaultReportPath(validatedPath)
	if !strings.Contains(output.String(), "relatorio de validacao escrito em "+reportPath) {
		t.Fatalf("expected validation report output path, got %q", output.String())
	}
	if !strings.Contains(output.String(), "scaffold inicializado") {
		t.Fatalf("expected scaffold message, got %q", output.String())
	}

	contents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets file: %v", err)
	}
	if !strings.Contains(string(contents), "stale-id") || strings.Contains(string(contents), "current-id") {
		t.Fatalf("validation should not overwrite original file, got:\n%s", contents)
	}

	validatedContents, err := os.ReadFile(validatedPath)
	if err != nil {
		t.Fatalf("read validated targets file: %v", err)
	}
	wantValidated := strings.TrimLeft(`
- name: oraemc
  site: null
  endpoint: http://oem.example
  targets:
    - id: current-id
      name: cdbp51bc
      typeName: rac_database
      tags:
        rac_database: cdbp51bc
        target_name: cdbp51bc
        target_type: rac_database
`, "\n")
	if string(validatedContents) != wantValidated {
		t.Fatalf("validated YAML mismatch\nwant:\n%s\ngot:\n%s", wantValidated, validatedContents)
	}
	reportContents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read validation report file: %v", err)
	}
	reportEvents := parseAppJSONLReport(t, reportContents)
	if !hasAppReportEvent(reportEvents, "id_correction", "targetName", "cdbp51bc") ||
		!hasAppReportEvent(reportEvents, "tag_correction", "targetName", "cdbp51bc") {
		t.Fatalf("validation report missing expected events:\n%s", reportContents)
	}
	infos := logger.infosSnapshot()
	assertInfoSequence(t, infos, []string{
		"validacao de configuracao iniciada",
		"validacao de IDs iniciada",
		"validacao de IDs: listando targets OEM",
		"conexao OEM validada",
		"validacao de IDs: targets OEM listados",
		"validacao de IDs concluida",
		"validacao de correlacoes iniciada",
		"validacao de correlacoes: listando targets OEM",
		"validacao de correlacoes: targets OEM listados",
		"validacao de correlacoes concluida",
		"configuracao validada escrita",
	})
	if got := countInfoMessages(infos, "conexao OEM validada"); got != 1 {
		t.Fatalf("conexao OEM validada logs = %d, want 1; infos=%#v", got, infos)
	}
}

func TestRunWithValidationDoesNotCollectRemovedTarget(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	validatedPath := filepath.Join(tmp, "validated", "configTargets.yaml")
	reportPath := filepath.Join(tmp, "validated", "configTargets.report.jsonl")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: keep-id
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
    - id: missing-id
      name: missing
      typeName: host
      tags:
        target_name: missing
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var keepLatestDataCalls atomic.Int32
	var missingLatestDataCalls atomic.Int32
	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets/keep-id/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/keep-id/metricGroups/Load/latestData":
			keepLatestDataCalls.Add(1)
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/targets/missing-id/metricGroups/Load":
			http.Error(w, "removed target should not be collected", http.StatusInternalServerError)
		case "/em/api/targets/missing-id/metricGroups/Load/latestData":
			missingLatestDataCalls.Add(1)
			http.Error(w, "removed target should not be collected", http.StatusInternalServerError)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			if cancel != nil {
				time.AfterFunc(10*time.Millisecond, cancel)
			}
		case "/v1/logs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	cancel = stop
	defer stop()

	var output bytes.Buffer
	err = Run(ctx, Options{
		Output: &output,
		LookupEnv: mapLookup(map[string]string{
			"OEM_VALIDATE_CONFIG":          "true",
			"OEM_CONFIG_TARGETS":           targetsPath,
			"OEM_CONFIG_METRICS":           metricsPath,
			"OEM_VALIDATED_CONFIG_OUTPUT":  validatedPath,
			"OEM_VALIDATION_REPORT_OUTPUT": reportPath,
			"OEM_USER":                     "user",
			"OEM_PASSWORD":                 "secret",
			"OTEL_EXPORT_URL":              server.URL,
			"OEM_HTTP_MAX_RETRIES":         "0",
			"OEM_EXPORT_INTERVAL_SECONDS":  "60",
		}),
		Logger: &appRecordingLogger{},
		TargetInventoryFactory: func(config.SiteConfig) (validate.TargetInventory, error) {
			return appFakeTargetLister{
				targets: []oem.Target{{ID: "keep-id", Name: "host1", TypeName: "host"}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if keepLatestDataCalls.Load() == 0 {
		t.Fatal("expected kept target to be collected")
	}
	if missingLatestDataCalls.Load() != 0 {
		t.Fatalf("removed target latestData calls = %d, want zero", missingLatestDataCalls.Load())
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected metrics export after collecting kept target")
	}
	if !strings.Contains(output.String(), "coleta iniciada com 2 jobs") {
		t.Fatalf("expected two jobs after validation removal, got %q", output.String())
	}

	validatedContents, err := os.ReadFile(validatedPath)
	if err != nil {
		t.Fatalf("read validated targets: %v", err)
	}
	if strings.Contains(string(validatedContents), "missing-id") || !strings.Contains(string(validatedContents), "keep-id") {
		t.Fatalf("validated config should keep only found target:\n%s", validatedContents)
	}
	reportContents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read validation report: %v", err)
	}
	reportEvents := parseAppJSONLReport(t, reportContents)
	if !hasAppReportEvent(reportEvents, "target_removed", "targetName", "missing") {
		t.Fatalf("validation report should describe removed target:\n%s", reportContents)
	}
}

func TestRunWithValidationRepairsTargetIDAfterRuntimeLatestDataNotFound(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	validatedPath := filepath.Join(tmp, "validated", "configTargets.yaml")
	reportPath := filepath.Join(tmp, "validated", "configTargets.report.jsonl")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: PLACEHOLDER
  targets:
    - id: old-id
      name: host1
      typeName: host
      tags:
        target_name: host1
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	var targetListCalls atomic.Int32
	var oldLatestDataCalls atomic.Int32
	var newLatestDataCalls atomic.Int32
	var metricsPosts atomic.Int32
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/em/") {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user" || pass != "secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/em/api":
			fmt.Fprint(w, `{"name":"mock","version":"test"}`)
		case "/em/api/targets":
			if targetListCalls.Add(1) <= 2 {
				fmt.Fprint(w, `{"items":[{"id":"old-id","name":"host1","typeName":"host"}]}`)
				return
			}
			fmt.Fprint(w, `{"items":[{"id":"new-id","name":"host1","typeName":"host"}]}`)
		case "/em/api/targets/old-id/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/old-id/metricGroups/Load/latestData":
			oldLatestDataCalls.Add(1)
			http.NotFound(w, r)
		case "/em/api/targets/old-id/metricGroups/Response/latestData":
			fmt.Fprint(w, `{"items":[]}`)
		case "/em/api/targets/new-id/metricGroups/Load":
			fmt.Fprint(w, `{"name":"Load","keys":[{"name":"mount"}],"metrics":[{"name":"value","dataType":"NUMBER"}]}`)
		case "/em/api/targets/new-id/metricGroups/Load/latestData":
			newLatestDataCalls.Add(1)
			fmt.Fprint(w, `{"items":[{"mount":"/","value":1.5}]}`)
		case "/em/api/incidents/":
			fmt.Fprint(w, `{"items":[]}`)
		case "/v1/metrics":
			metricsPosts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			if cancel != nil && newLatestDataCalls.Load() > 0 {
				time.AfterFunc(10*time.Millisecond, cancel)
			}
		case "/v1/logs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	targetsContents, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read targets: %v", err)
	}
	targetsContents = []byte(strings.ReplaceAll(string(targetsContents), "PLACEHOLDER", server.URL))
	if err := os.WriteFile(targetsPath, targetsContents, 0o600); err != nil {
		t.Fatalf("rewrite targets: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 6*time.Second)
	cancel = stop
	defer stop()

	err = Run(ctx, Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_VALIDATE_CONFIG":                     "true",
			"OEM_CONFIG_TARGETS":                      targetsPath,
			"OEM_CONFIG_METRICS":                      metricsPath,
			"OEM_VALIDATED_CONFIG_OUTPUT":             validatedPath,
			"OEM_VALIDATION_REPORT_OUTPUT":            reportPath,
			"OEM_RUNTIME_ID_RECHECK_INTERVAL_SECONDS": "3600",
			"OEM_USER":                               "user",
			"OEM_PASSWORD":                           "secret",
			"OTEL_EXPORT_URL":                        server.URL,
			"OEM_HTTP_MAX_RETRIES":                   "0",
			"OEM_EXPORT_INTERVAL_SECONDS":            "1",
			"OEM_DIAGNOSTICS_INTERVAL_SECONDS":       "60",
			"OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES": "21",
		}),
		Logger: &appRecordingLogger{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if oldLatestDataCalls.Load() == 0 || newLatestDataCalls.Load() == 0 {
		t.Fatalf("latestData calls old/new = %d/%d, want both", oldLatestDataCalls.Load(), newLatestDataCalls.Load())
	}
	if metricsPosts.Load() == 0 {
		t.Fatal("expected metrics export after repaired collection")
	}

	validatedContents, err := os.ReadFile(validatedPath)
	if err != nil {
		t.Fatalf("read validated targets: %v", err)
	}
	if strings.Contains(string(validatedContents), "old-id") || !strings.Contains(string(validatedContents), "new-id") {
		t.Fatalf("validated config should be rewritten with new ID:\n%s", validatedContents)
	}
	reportContents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read validation report: %v", err)
	}
	reportEvents := parseAppJSONLReport(t, reportContents)
	if !hasAppReportEventFields(reportEvents, map[string]any{
		"event":           "id_correction",
		"phase":           "runtime",
		"trigger":         "metric_404",
		"metricGroupName": "Load",
		"oldID":           "old-id",
		"newID":           "new-id",
	}) {
		t.Fatalf("validation report should include runtime ID correction:\n%s", reportContents)
	}
}

func TestRuntimeTargetRepairSkipsLookupWhenResponseIsActive(t *testing.T) {
	var targetListCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/em/api/targets" {
			targetListCalls.Add(1)
			fmt.Fprint(w, `{"items":[{"id":"new-id","name":"host1","typeName":"host"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := newRuntimeRepairClient(t, server.URL)
	monitor := collect.NewResponseMonitor()
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	monitor.Mark("old-id", now.Add(-time.Minute))
	state := newRuntimeTargetState(runtimeRepairConfig(server.URL), runtimeRepairEnv(t, time.Hour), monitor, map[string]*oem.Client{server.URL: client}, &appRecordingLogger{})

	result, err := state.RepairTargetID(context.Background(), collect.TargetIDRepairRequest{
		Job:       runtimeRepairJob(server.URL),
		Trigger:   collect.TargetIDRepairTriggerMetric404,
		Stage:     collect.TargetIDRepairStageLatestData,
		CheckedAt: now,
	})
	if err != nil {
		t.Fatalf("RepairTargetID returned error: %v", err)
	}
	if result.Corrected {
		t.Fatalf("response-active target should not be corrected: %#v", result)
	}
	if targetListCalls.Load() != 0 {
		t.Fatalf("target list calls = %d, want zero when response is active", targetListCalls.Load())
	}
}

func TestRuntimeTargetRepairAppliesCooldownWhenIDDoesNotChange(t *testing.T) {
	var targetListCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/em/api/targets" {
			targetListCalls.Add(1)
			fmt.Fprint(w, `{"items":[{"id":"old-id","name":"host1","typeName":"host"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := newRuntimeRepairClient(t, server.URL)
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	state := newRuntimeTargetState(runtimeRepairConfig(server.URL), runtimeRepairEnv(t, time.Hour), collect.NewResponseMonitor(), map[string]*oem.Client{server.URL: client}, &appRecordingLogger{})
	req := collect.TargetIDRepairRequest{
		Job:     runtimeRepairJob(server.URL),
		Trigger: collect.TargetIDRepairTriggerMetric404,
		Stage:   collect.TargetIDRepairStageLatestData,
	}

	req.CheckedAt = now
	if result, err := state.RepairTargetID(context.Background(), req); err != nil || result.Corrected {
		t.Fatalf("first RepairTargetID result = %#v, %v; want no correction", result, err)
	}
	req.CheckedAt = now.Add(time.Minute)
	if result, err := state.RepairTargetID(context.Background(), req); err != nil || result.Corrected {
		t.Fatalf("second RepairTargetID result = %#v, %v; want cooldown without correction", result, err)
	}
	if targetListCalls.Load() != 1 {
		t.Fatalf("target list calls = %d, want one due cooldown", targetListCalls.Load())
	}
}

func TestRunWithValidationFailsBeforeCollectionWhenAllTargetsRemoved(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "configTargets.yaml")
	metricsPath := filepath.Join(tmp, "configMetrics.yaml")
	validatedPath := filepath.Join(tmp, "validated", "configTargets.yaml")
	reportPath := filepath.Join(tmp, "validated", "configTargets.report.jsonl")
	if err := os.WriteFile(targetsPath, []byte(`
- name: mock
  endpoint: http://oem.example
  targets:
    - id: missing-id
      name: missing
      typeName: host
      tags:
        target_name: missing
        target_type: host
`), 0o600); err != nil {
		t.Fatalf("write targets: %v", err)
	}
	if err := os.WriteFile(metricsPath, []byte(`
host:
  - freq: 5
    metric_group_name: Load
`), 0o600); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	err := Run(context.Background(), Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_VALIDATE_CONFIG":          "true",
			"OEM_CONFIG_TARGETS":           targetsPath,
			"OEM_CONFIG_METRICS":           metricsPath,
			"OEM_VALIDATED_CONFIG_OUTPUT":  validatedPath,
			"OEM_VALIDATION_REPORT_OUTPUT": reportPath,
			"OTEL_EXPORT_URL":              "http://collector.example",
		}),
		Logger: &appRecordingLogger{},
		TargetInventoryFactory: func(config.SiteConfig) (validate.TargetInventory, error) {
			return appFakeTargetLister{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "validacao removeu todos os targets") {
		t.Fatalf("expected all-targets-removed error, got %v", err)
	}
	validatedContents, readErr := os.ReadFile(validatedPath)
	if readErr != nil {
		t.Fatalf("read validated targets: %v", readErr)
	}
	if !strings.Contains(string(validatedContents), "[]") {
		t.Fatalf("validated config should contain no sites, got:\n%s", validatedContents)
	}
	reportContents, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Fatalf("read validation report: %v", readErr)
	}
	reportEvents := parseAppJSONLReport(t, reportContents)
	if !hasAppReportEvent(reportEvents, "target_removed", "targetName", "missing") ||
		!hasAppReportEvent(reportEvents, "site_removed", "siteName", "mock") {
		t.Fatalf("validation report should record all removals:\n%s", reportContents)
	}
}

func TestRunValidationUsesCredentialsWhenFactoryIsNotInjected(t *testing.T) {
	targetsPath := writeTargetsFile(t, "configured-id")

	err := Run(context.Background(), Options{
		LookupEnv: mapLookup(map[string]string{"OEM_VALIDATE_CONFIG": "true", "OEM_CONFIG_TARGETS": targetsPath}),
	})
	if err == nil || !strings.Contains(err.Error(), "OEM_USER") {
		t.Fatalf("expected credentials error, got %v", err)
	}
}

func TestTargetInventoryFactoryCanDisableTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/em/api/targets" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"id":"target-1","name":"db1","typeName":"oracle_database"}]}`)
	}))
	defer server.Close()

	env, err := config.ReadEnv(mapLookup(map[string]string{
		"OEM_USER":       "user",
		"OEM_PASSWORD":   "secret",
		"OEM_TLS_VERIFY": "false",
	}))
	if err != nil {
		t.Fatalf("ReadEnv returned error: %v", err)
	}
	factory, err := targetInventoryFactory(env)
	if err != nil {
		t.Fatalf("targetInventoryFactory returned error: %v", err)
	}
	inventory, err := factory(config.SiteConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("factory returned error: %v", err)
	}

	targets, err := inventory.ListTargets(context.Background())
	if err != nil {
		t.Fatalf("ListTargets with OEM_TLS_VERIFY=false returned error: %v", err)
	}
	if len(targets.Items) != 1 || targets.Items[0].ID != "target-1" {
		t.Fatalf("unexpected targets: %#v", targets.Items)
	}
}

func TestRunValidationRejectsOutputPathEqualToOriginal(t *testing.T) {
	targetsPath := writeTargetsFile(t, "configured-id")

	err := Run(context.Background(), Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_VALIDATE_CONFIG":         "true",
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_VALIDATED_CONFIG_OUTPUT": targetsPath,
		}),
		TargetInventoryFactory: func(config.SiteConfig) (validate.TargetInventory, error) {
			return appFakeTargetLister{
				targets: []oem.Target{{ID: "configured-id", Name: "cdbp51bc", TypeName: "rac_database"}},
			}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "deve ser diferente") {
		t.Fatalf("expected same-path validation error, got %v", err)
	}

	contents, readErr := os.ReadFile(targetsPath)
	if readErr != nil {
		t.Fatalf("read original targets file: %v", readErr)
	}
	if !strings.Contains(string(contents), "configured-id") || strings.Contains(string(contents), "site: null") {
		t.Fatalf("original file should remain untouched, got:\n%s", contents)
	}
}

func TestRunValidationRejectsOutputSymlinkToOriginal(t *testing.T) {
	targetsPath := writeTargetsFile(t, "configured-id")
	outputPath := filepath.Join(filepath.Dir(targetsPath), "validated.yaml")
	if err := os.Symlink(targetsPath, outputPath); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	err := Run(context.Background(), Options{
		LookupEnv: mapLookup(map[string]string{
			"OEM_VALIDATE_CONFIG":         "true",
			"OEM_CONFIG_TARGETS":          targetsPath,
			"OEM_VALIDATED_CONFIG_OUTPUT": outputPath,
		}),
		TargetInventoryFactory: func(config.SiteConfig) (validate.TargetInventory, error) {
			return appFakeTargetLister{
				targets: []oem.Target{{ID: "configured-id", Name: "cdbp51bc", TypeName: "rac_database"}},
			}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "aponta para o mesmo arquivo") {
		t.Fatalf("expected symlink same-file validation error, got %v", err)
	}

	contents, readErr := os.ReadFile(targetsPath)
	if readErr != nil {
		t.Fatalf("read original targets file: %v", readErr)
	}
	if !strings.Contains(string(contents), "configured-id") || strings.Contains(string(contents), "site: null") {
		t.Fatalf("original file should remain untouched, got:\n%s", contents)
	}
}

func TestRunValidationRejectsReportPathEqualToOriginalOrValidatedConfig(t *testing.T) {
	for _, tt := range []struct {
		name       string
		reportPath func(targetsPath, validatedPath string) string
		want       string
	}{
		{
			name: "original",
			reportPath: func(targetsPath, _ string) string {
				return targetsPath
			},
			want: "OEM_CONFIG_TARGETS",
		},
		{
			name: "validated",
			reportPath: func(_, validatedPath string) string {
				return validatedPath
			},
			want: "OEM_VALIDATED_CONFIG_OUTPUT",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targetsPath := writeTargetsFile(t, "configured-id")
			validatedPath := filepath.Join(t.TempDir(), "validated.yaml")

			err := Run(context.Background(), Options{
				LookupEnv: mapLookup(map[string]string{
					"OEM_VALIDATE_CONFIG":          "true",
					"OEM_CONFIG_TARGETS":           targetsPath,
					"OEM_VALIDATED_CONFIG_OUTPUT":  validatedPath,
					"OEM_VALIDATION_REPORT_OUTPUT": tt.reportPath(targetsPath, validatedPath),
				}),
				TargetInventoryFactory: func(config.SiteConfig) (validate.TargetInventory, error) {
					return appFakeTargetLister{
						targets: []oem.Target{{ID: "configured-id", Name: "cdbp51bc", TypeName: "rac_database"}},
					}, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected report path validation error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestMonitorStatusWarmupCoversInitialCollectionAndExtraDuration(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	state := &runtimeState{env: config.Env{MonitorStatusWarmup: 5 * time.Minute}}

	if !state.monitorStatusWarmupActive(observedAt) {
		t.Fatal("warmup should be active before initial collection completes")
	}

	state.setMonitorStatusWarmupUntil(observedAt.Add(5 * time.Minute))
	if !state.monitorStatusWarmupActive(observedAt.Add(5*time.Minute - time.Nanosecond)) {
		t.Fatal("warmup should stay active before configured deadline")
	}
	if state.monitorStatusWarmupActive(observedAt.Add(5 * time.Minute)) {
		t.Fatal("warmup should end at configured deadline")
	}
}

func TestFinishMonitorStatusWarmupLogsImmediatelyWithoutExtraDuration(t *testing.T) {
	logger := &appRecordingLogger{}
	state := &runtimeState{logger: logger}

	timer := state.finishMonitorStatusWarmupAfterInitial(context.Background(), time.Now())

	if timer != nil {
		timer.Stop()
		t.Fatal("warmup without extra duration should not schedule a timer")
	}
	if !logger.containsInfo("warm-up de oem_monitor_stus concluido") {
		t.Fatalf("missing warmup completion info log: %#v", logger.infosSnapshot())
	}
}

func TestFinishMonitorStatusWarmupSchedulesExtraDuration(t *testing.T) {
	logger := &appRecordingLogger{}
	state := &runtimeState{
		env:    config.Env{MonitorStatusWarmup: time.Hour},
		logger: logger,
	}

	timer := state.finishMonitorStatusWarmupAfterInitial(context.Background(), time.Now())
	if timer == nil {
		t.Fatal("warmup with extra duration should schedule a timer")
	}
	timer.Stop()
	if logger.containsInfo("warm-up de oem_monitor_stus concluido") {
		t.Fatalf("warmup completion should wait for timer: %#v", logger.infosSnapshot())
	}
}

type appFakeTargetLister struct {
	targets []oem.Target
}

func (f appFakeTargetLister) ListTargets(context.Context) (oem.Page[oem.Target], error) {
	return oem.Page[oem.Target]{Items: f.targets}, nil
}

func (f appFakeTargetLister) TargetProperties(context.Context, string) (oem.Page[oem.Property], error) {
	return oem.Page[oem.Property]{}, nil
}

type appRecordingLogger struct {
	mu       sync.Mutex
	warnings []string
	infos    []string
}

func (r *appRecordingLogger) WarnContext(_ context.Context, msg string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, msg)
}

func (r *appRecordingLogger) InfoContext(_ context.Context, msg string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, msg)
}

func (r *appRecordingLogger) ErrorContext(_ context.Context, msg string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, msg)
}

func (r *appRecordingLogger) containsWarning(part string) bool {
	for _, warning := range r.warningsSnapshot() {
		if strings.Contains(warning, part) {
			return true
		}
	}
	return false
}

func (r *appRecordingLogger) containsInfo(part string) bool {
	for _, info := range r.infosSnapshot() {
		if strings.Contains(info, part) {
			return true
		}
	}
	return false
}

func (r *appRecordingLogger) infosSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.infos))
	copy(out, r.infos)
	return out
}

func (r *appRecordingLogger) warningsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.warnings))
	copy(out, r.warnings)
	return out
}

func assertInfoSequence(t *testing.T, infos []string, want []string) {
	t.Helper()

	next := 0
	for _, info := range infos {
		if next < len(want) && strings.Contains(info, want[next]) {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("missing info log sequence from %q; got %#v", want[next], infos)
	}
}

func countInfoMessages(infos []string, part string) int {
	count := 0
	for _, info := range infos {
		if strings.Contains(info, part) {
			count++
		}
	}
	return count
}

func writeTargetsFile(t *testing.T, targetID string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "configTargets.yaml")
	data := []byte(`
- name: oraemc
  endpoint: http://oem.example
  targets:
    - id: "` + targetID + `"
      name: cdbp51bc
      typeName: rac_database
      tags:
        target_name: cdbp51bc
        target_type: rac_database
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write targets file: %v", err)
	}
	return path
}

func parseAppJSONLReport(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL report line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func hasAppReportEvent(events []map[string]any, eventName, field string, value any) bool {
	for _, event := range events {
		if event["event"] == eventName && event[field] == value {
			return true
		}
	}
	return false
}

func hasAppReportEventFields(events []map[string]any, fields map[string]any) bool {
	for _, event := range events {
		matches := true
		for key, want := range fields {
			if event[key] != want {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func newRuntimeRepairClient(t *testing.T, endpoint string) *oem.Client {
	t.Helper()

	client, err := oem.New(oem.Options{
		Endpoint:    endpoint,
		Credentials: auth.Credentials{User: "user", Password: "secret"},
		MaxRetries:  0,
	})
	if err != nil {
		t.Fatalf("oem.New returned error: %v", err)
	}
	return client
}

func runtimeRepairEnv(t *testing.T, recheckInterval time.Duration) config.Env {
	t.Helper()

	dir := t.TempDir()
	return config.Env{
		ValidateConfig:           true,
		TargetsPath:              filepath.Join(dir, "configTargets.yaml"),
		ValidatedConfigOutput:    filepath.Join(dir, "configTargets.validated.yaml"),
		ValidationReportOutput:   filepath.Join(dir, "configTargets.validated.report.jsonl"),
		RuntimeIDRecheckInterval: recheckInterval,
		MonitorResponseTolerance: time.Hour,
	}
}

func runtimeRepairConfig(endpoint string) config.Config {
	return config.Config{
		Sites: []config.SiteConfig{{
			Name:     "mock",
			Endpoint: endpoint,
			Targets: []config.TargetConfig{{
				ID:       "old-id",
				Name:     "host1",
				TypeName: "host",
				Tags: map[string]string{
					"target_name": "host1",
					"target_type": "host",
				},
			}},
		}},
	}
}

func runtimeRepairJob(endpoint string) scheduler.Job {
	return scheduler.Job{
		ID:              "job-load",
		SiteName:        "mock",
		Endpoint:        endpoint,
		Target:          runtimeRepairConfig(endpoint).Sites[0].Targets[0],
		MetricGroup:     config.MetricGroupConfig{Freq: 1, MetricGroupName: "Load"},
		MetricGroupName: "Load",
		Frequency:       time.Minute,
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func emptyLookup(string) (string, bool) {
	return "", false
}
