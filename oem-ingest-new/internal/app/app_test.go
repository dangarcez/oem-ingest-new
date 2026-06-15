package app

import (
	"bytes"
	"context"
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

	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
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
	if !strings.Contains(output.String(), "coleta iniciada com 2 jobs") {
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
		Logger: &appRecordingLogger{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if metricsPosts.Load() != 2 {
		t.Fatalf("metrics POSTs = %d, want failed initial export plus final flush retry", metricsPosts.Load())
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
	if !strings.Contains(output.String(), "validacao de configuracao concluida: 1 correcoes de ID, 0 targets adicionados, 1 tags corrigidas, 2 avisos") {
		t.Fatalf("expected validation summary, got %q", output.String())
	}
	if !strings.Contains(output.String(), "configuracao validada escrita em "+validatedPath) {
		t.Fatalf("expected validated config output path, got %q", output.String())
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
	if len(logger.infos) != 1 || !strings.Contains(logger.infos[0], "configuracao validada escrita") {
		t.Fatalf("expected validation summary log, got %#v", logger.infos)
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

func (r *appRecordingLogger) warningsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.warnings))
	copy(out, r.warnings)
	return out
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

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func emptyLookup(string) (string, bool) {
	return "", false
}
