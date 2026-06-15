package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadEnvDefaults(t *testing.T) {
	env, err := ReadEnv(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("ReadEnv returned error: %v", err)
	}

	if env.TargetsPath != DefaultTargetsPath {
		t.Fatalf("TargetsPath = %q, want %q", env.TargetsPath, DefaultTargetsPath)
	}
	if env.MetricsPath != DefaultMetricsPath {
		t.Fatalf("MetricsPath = %q, want %q", env.MetricsPath, DefaultMetricsPath)
	}
	if env.ValidateConfig {
		t.Fatal("ValidateConfig should default to false")
	}
	if env.ExportInterval != time.Duration(DefaultExportIntervalSeconds)*time.Second {
		t.Fatalf("ExportInterval = %s", env.ExportInterval)
	}
	if env.MonitorResponseTolerance != time.Duration(DefaultResponseToleranceMin)*time.Minute {
		t.Fatalf("MonitorResponseTolerance = %s", env.MonitorResponseTolerance)
	}
	if env.SchedulerJitter != time.Duration(DefaultSchedulerJitterSeconds)*time.Second {
		t.Fatalf("SchedulerJitter = %s", env.SchedulerJitter)
	}
	if !env.TLSVerify {
		t.Fatal("TLSVerify should default to true")
	}
}

func TestReadEnvOverrides(t *testing.T) {
	values := map[string]string{
		"OEM_CONFIG_TARGETS":                     "/tmp/targets.yaml",
		"OEM_CONFIG_METRICS":                     "/tmp/metrics.yaml",
		"OEM_VALIDATE_CONFIG":                    "true",
		"OEM_VALIDATED_CONFIG_OUTPUT":            "/tmp/validated.yaml",
		"OEM_USER":                               "oem_user",
		"OEM_PASSWORD":                           "secret",
		"OEM_TOKEN":                              "token",
		"OEM_AUTH_TOKEN_HASH_FILE":               "/tmp/hash-source",
		"OTEL_EXPORT_URL":                        "http://collector:4318",
		"OEM_EXPORT_INTERVAL_SECONDS":            "15",
		"OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES": "7",
		"OEM_HTTP_TIMEOUT_SECONDS":               "20",
		"OEM_HTTP_CONNECT_TIMEOUT_SECONDS":       "3",
		"OEM_HTTP_MAX_RETRIES":                   "4",
		"OEM_MAX_CONCURRENT_REQUESTS":            "5",
		"OEM_SCHEDULER_JITTER_SECONDS":           "12",
		"OEM_LOG_LEVEL":                          "debug",
		"OEM_TLS_VERIFY":                         "false",
	}

	env, err := ReadEnv(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("ReadEnv returned error: %v", err)
	}

	if env.TargetsPath != "/tmp/targets.yaml" || env.MetricsPath != "/tmp/metrics.yaml" {
		t.Fatalf("unexpected config paths: %#v", env)
	}
	if !env.ValidateConfig {
		t.Fatal("ValidateConfig should be true")
	}
	if env.User != "oem_user" || env.Password != "secret" || env.Token != "token" {
		t.Fatalf("unexpected auth env: %#v", env)
	}
	if env.ExportInterval != 15*time.Second {
		t.Fatalf("ExportInterval = %s", env.ExportInterval)
	}
	if env.MonitorResponseTolerance != 7*time.Minute {
		t.Fatalf("MonitorResponseTolerance = %s", env.MonitorResponseTolerance)
	}
	if env.HTTPTimeout != 20*time.Second || env.HTTPConnectTimeout != 3*time.Second {
		t.Fatalf("unexpected HTTP timeouts: %#v", env)
	}
	if env.HTTPMaxRetries != 4 || env.MaxConcurrentRequests != 5 || env.LogLevel != "debug" {
		t.Fatalf("unexpected numeric/log values: %#v", env)
	}
	if env.SchedulerJitter != 12*time.Second {
		t.Fatalf("SchedulerJitter = %s", env.SchedulerJitter)
	}
	if env.TLSVerify {
		t.Fatal("TLSVerify should be false when OEM_TLS_VERIFY=false")
	}
}

func TestReadEnvInvalidBool(t *testing.T) {
	tests := []string{"OEM_VALIDATE_CONFIG", "OEM_TLS_VERIFY"}

	for _, envName := range tests {
		t.Run(envName, func(t *testing.T) {
			_, err := ReadEnv(func(key string) (string, bool) {
				if key == envName {
					return "yes", true
				}
				return "", false
			})
			if err == nil || !strings.Contains(err.Error(), envName) {
				t.Fatalf("expected %s error, got %v", envName, err)
			}
		})
	}
}

func TestReadEnvAllowsDisabledSchedulerJitter(t *testing.T) {
	env, err := ReadEnv(func(key string) (string, bool) {
		if key == "OEM_SCHEDULER_JITTER_SECONDS" {
			return "0", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("ReadEnv returned error: %v", err)
	}
	if env.SchedulerJitter != 0 {
		t.Fatalf("SchedulerJitter = %s, want disabled", env.SchedulerJitter)
	}
}

func TestReadEnvRejectsInvalidSchedulerJitter(t *testing.T) {
	_, err := ReadEnv(func(key string) (string, bool) {
		if key == "OEM_SCHEDULER_JITTER_SECONDS" {
			return "-1", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "OEM_SCHEDULER_JITTER_SECONDS") {
		t.Fatalf("expected OEM_SCHEDULER_JITTER_SECONDS error, got %v", err)
	}
}

func TestReadEnvNormalizesLogLevel(t *testing.T) {
	env, err := ReadEnv(func(key string) (string, bool) {
		if key == "OEM_LOG_LEVEL" {
			return "WARNING", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("ReadEnv returned error: %v", err)
	}
	if env.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q, want warn", env.LogLevel)
	}
}

func TestReadEnvRejectsInvalidLogLevel(t *testing.T) {
	_, err := ReadEnv(func(key string) (string, bool) {
		if key == "OEM_LOG_LEVEL" {
			return "verbose", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "OEM_LOG_LEVEL") {
		t.Fatalf("expected OEM_LOG_LEVEL error, got %v", err)
	}
}

func TestLoadValidFiles(t *testing.T) {
	dir := t.TempDir()
	targetsPath := writeFile(t, dir, "configTargets.yaml", `
- name: oraemc
  site: null
  endpoint: http://localhost:8008
  targets:
    - id: "240D79C7320E221DE06400144FFBE115"
      name: "occp40bc"
      typeName: "rac_database"
      tags:
        rac_database: "occp40bc"
        target_name: "occp40bc"
        target_type: "rac_database"
        sistema: "siapx"
`)
	metricsPath := writeFile(t, dir, "configMetrics.yaml", `
rac_database:
  - freq: 5
    metric_group_name: Availability
host:
  - freq: 10
    metric_group_name: Load
`)

	cfg, err := Load(targetsPath, metricsPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.Sites) != 1 || len(cfg.Sites[0].Targets) != 1 {
		t.Fatalf("unexpected sites: %#v", cfg.Sites)
	}
	target := cfg.Sites[0].Targets[0]
	if target.TypeName != "rac_database" || target.Tags["sistema"] != "siapx" {
		t.Fatalf("unexpected target: %#v", target)
	}
	if got := cfg.Metrics["rac_database"][0]; got.Freq != 5 || got.MetricGroupName != "Availability" {
		t.Fatalf("unexpected metric group: %#v", got)
	}
}

func TestLoadVersionedExampleFiles(t *testing.T) {
	cfg, err := Load(
		filepath.Join("..", "..", "configs", "configTargets.example.yaml"),
		filepath.Join("..", "..", "configs", "configMetrics.example.yaml"),
	)
	if err != nil {
		t.Fatalf("Load returned error for versioned examples: %v", err)
	}
	if len(cfg.Sites) == 0 {
		t.Fatal("expected at least one site in versioned target example")
	}
	if len(cfg.Metrics) == 0 {
		t.Fatal("expected at least one target type in versioned metrics example")
	}
}

func TestWriteTargetsWritesSimplifiedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "configTargets.validated.yaml")
	sites := []SiteConfig{
		{
			Name:     "oraemc",
			Endpoint: "http://oem.example",
			Targets: []TargetConfig{
				{
					ID:       "current-id",
					Name:     "cdbp51bc",
					TypeName: "rac_database",
					Tags: map[string]string{
						"rac_database": "cdbp51bc",
						"target_name":  "cdbp51bc",
						"target_type":  "rac_database",
						"sistema":      "siapx",
					},
				},
				{
					ID:       "host-id",
					Name:     "dbhost01.intra.example",
					TypeName: "host",
					Tags: map[string]string{
						"host":        "dbhost01",
						"target_name": "dbhost01",
						"target_type": "host",
					},
				},
			},
		},
	}

	if err := WriteTargets(path, sites); err != nil {
		t.Fatalf("WriteTargets returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written targets: %v", err)
	}
	want := strings.TrimLeft(`
- name: oraemc
  site: null
  endpoint: http://oem.example
  targets:
    - id: current-id
      name: cdbp51bc
      typeName: rac_database
      tags:
        rac_database: cdbp51bc
        sistema: siapx
        target_name: cdbp51bc
        target_type: rac_database
    - id: host-id
      name: dbhost01.intra.example
      typeName: host
      tags:
        host: dbhost01
        target_name: dbhost01
        target_type: host
`, "\n")
	if string(data) != want {
		t.Fatalf("validated YAML mismatch\nwant:\n%s\ngot:\n%s", want, data)
	}
	if _, err := LoadTargets(path); err != nil {
		t.Fatalf("written targets should load cleanly: %v", err)
	}
}

func TestLoadTargetsMissingFile(t *testing.T) {
	_, err := LoadTargets(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !strings.Contains(err.Error(), "carregar targets") || !strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("missing file error should be actionable, got %v", err)
	}
}

func TestLoadTargetsSiteWithoutEndpoint(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configTargets.yaml", `
- name: oraemc
  targets:
    - id: "target-id"
      name: "occp40bc"
      typeName: "rac_database"
      tags:
        target_name: "occp40bc"
        target_type: "rac_database"
`)
	_, err := LoadTargets(path)
	assertErrorContains(t, err, "site[0].endpoint")
}

func TestLoadTargetsTargetMissingRequiredField(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configTargets.yaml", `
- name: oraemc
  endpoint: http://localhost:8008
  targets:
    - id: "target-id"
      name: "occp40bc"
      tags:
        target_name: "occp40bc"
        target_type: "rac_database"
`)
	_, err := LoadTargets(path)
	assertErrorContains(t, err, "site[0].targets[0].typeName")
}

func TestLoadTargetsTargetMissingTag(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configTargets.yaml", `
- name: oraemc
  endpoint: http://localhost:8008
  targets:
    - id: "target-id"
      name: "occp40bc"
      typeName: "rac_database"
      tags:
        target_name: "occp40bc"
`)
	_, err := LoadTargets(path)
	assertErrorContains(t, err, "site[0].targets[0].tags.target_type")
}

func TestLoadTargetsAcceptsLegacyTargetNameNormalization(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configTargets.yaml", `
- name: oraemc
  endpoint: http://localhost:8008
  targets:
    - id: "host-id"
      name: "cadecrk01cl01vm03.intra.caixa.gov.br"
      typeName: "host"
      tags:
        host: "cadecrk01cl01vm03"
        target_name: "cadecrk01cl01vm03"
        target_type: "host"
    - id: "listener-id"
      name: "LISTENER_cadecrk01cl01vm03.intra.caixa.gov.br"
      typeName: "oracle_listener"
      tags:
        oracle_listener: "cadecrk01cl01vm03_lstnr"
        target_name: "cadecrk01cl01vm03_lstnr"
        target_type: "oracle_listener"
`)
	targets, err := LoadTargets(path)
	if err != nil {
		t.Fatalf("LoadTargets returned error: %v", err)
	}
	if len(targets[0].Targets) != 2 {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestLoadTargetsRejectsInconsistentTargetTypeTag(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configTargets.yaml", `
- name: oraemc
  endpoint: http://localhost:8008
  targets:
    - id: "target-id"
      name: "occp40bc"
      typeName: "rac_database"
      tags:
        target_name: "occp40bc"
        target_type: "oracle_database"
`)
	_, err := LoadTargets(path)
	assertErrorContains(t, err, `site[0].targets[0].tags.target_type: esperado "rac_database"`)
}

func TestLoadTargetsRejectsInconsistentTargetNameTag(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configTargets.yaml", `
- name: oraemc
  endpoint: http://localhost:8008
  targets:
    - id: "target-id"
      name: "LISTENER_cadecrk01cl01vm03.intra.caixa.gov.br"
      typeName: "oracle_listener"
      tags:
        target_name: "LISTENER_cadecrk01cl01vm03.intra.caixa.gov.br"
        target_type: "oracle_listener"
`)
	_, err := LoadTargets(path)
	assertErrorContains(t, err, `site[0].targets[0].tags.target_name: esperado "cadecrk01cl01vm03_lstnr"`)
}

func TestLoadMetricsMissingFreq(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configMetrics.yaml", `
rac_database:
  - metric_group_name: Availability
`)
	_, err := LoadMetrics(path)
	assertErrorContains(t, err, "rac_database[0].freq")
}

func TestLoadMetricsMissingMetricGroupName(t *testing.T) {
	path := writeFile(t, t.TempDir(), "configMetrics.yaml", `
rac_database:
  - freq: 5
`)
	_, err := LoadMetrics(path)
	assertErrorContains(t, err, "rac_database[0].metric_group_name")
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	return path
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}
