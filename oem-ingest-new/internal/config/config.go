// Package config loads environment variables and YAML configuration files.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultTargetsPath              = "./configs/configTargets.yaml"
	DefaultMetricsPath              = "./configs/configMetrics.yaml"
	DefaultValidatedTargetsPath     = "./configs/configTargets.validated.yaml"
	DefaultValidationReportPath     = "./configs/configTargets.validated.report.yaml"
	DefaultExportIntervalSeconds    = 60
	DefaultResponseToleranceMin     = 21
	DefaultHTTPTimeoutSeconds       = 30
	DefaultConnectTimeoutSeconds    = 10
	DefaultOTELExportTimeoutSeconds = 30
	DefaultHTTPMaxRetries           = 3
	DefaultMaxConcurrentRequests    = 10
	DefaultSchedulerJitterSeconds   = 60
	DefaultLogLevel                 = "info"
)

// Env contains process configuration read from environment variables.
type Env struct {
	TargetsPath              string
	MetricsPath              string
	ValidateConfig           bool
	ValidatedConfigOutput    string
	ValidationReportOutput   string
	User                     string
	Password                 string
	Token                    string
	AuthTokenHashFile        string
	OTELExportURL            string
	OTELExportTimeout        time.Duration
	ExportInterval           time.Duration
	MonitorResponseTolerance time.Duration
	HTTPTimeout              time.Duration
	HTTPConnectTimeout       time.Duration
	HTTPMaxRetries           int
	MaxConcurrentRequests    int
	SchedulerJitter          time.Duration
	LogLevel                 string
	TLSVerify                bool
	DiagnosticsInterval      time.Duration
}

// SiteConfig represents one OEM site from configTargets.yaml.
type SiteConfig struct {
	Name     string         `yaml:"name"`
	Site     string         `yaml:"site"`
	Endpoint string         `yaml:"endpoint"`
	Targets  []TargetConfig `yaml:"targets"`
}

// TargetConfig represents one OEM target from configTargets.yaml.
type TargetConfig struct {
	ID       string            `yaml:"id"`
	Name     string            `yaml:"name"`
	TypeName string            `yaml:"typeName"`
	Tags     map[string]string `yaml:"tags"`
}

// MetricsConfig maps an OEM target type to the metric groups collected for it.
type MetricsConfig map[string][]MetricGroupConfig

// MetricGroupConfig represents one metric group entry from configMetrics.yaml.
type MetricGroupConfig struct {
	Freq            int    `yaml:"freq"`
	MetricGroupName string `yaml:"metric_group_name"`
}

// Config is the complete static configuration loaded from both YAML files.
type Config struct {
	Sites   []SiteConfig
	Metrics MetricsConfig
}

// LoadEnv reads all public environment variables supported by the process.
func LoadEnv() (Env, error) {
	return ReadEnv(os.LookupEnv)
}

// ReadEnv reads environment variables through lookup, which keeps tests isolated
// from the host process environment.
func ReadEnv(lookup func(string) (string, bool)) (Env, error) {
	validatedConfigOutput := stringValue(lookup, "OEM_VALIDATED_CONFIG_OUTPUT", DefaultValidatedTargetsPath)
	env := Env{
		TargetsPath:              stringValue(lookup, "OEM_CONFIG_TARGETS", DefaultTargetsPath),
		MetricsPath:              stringValue(lookup, "OEM_CONFIG_METRICS", DefaultMetricsPath),
		ValidatedConfigOutput:    validatedConfigOutput,
		ValidationReportOutput:   stringValue(lookup, "OEM_VALIDATION_REPORT_OUTPUT", DefaultReportPath(validatedConfigOutput)),
		User:                     stringValue(lookup, "OEM_USER", ""),
		Password:                 stringValue(lookup, "OEM_PASSWORD", ""),
		Token:                    stringValue(lookup, "OEM_TOKEN", ""),
		AuthTokenHashFile:        stringValue(lookup, "OEM_AUTH_TOKEN_HASH_FILE", ""),
		OTELExportURL:            stringValue(lookup, "OTEL_EXPORT_URL", ""),
		OTELExportTimeout:        time.Duration(DefaultOTELExportTimeoutSeconds) * time.Second,
		ExportInterval:           time.Duration(DefaultExportIntervalSeconds) * time.Second,
		MonitorResponseTolerance: time.Duration(DefaultResponseToleranceMin) * time.Minute,
		HTTPTimeout:              time.Duration(DefaultHTTPTimeoutSeconds) * time.Second,
		HTTPConnectTimeout:       time.Duration(DefaultConnectTimeoutSeconds) * time.Second,
		HTTPMaxRetries:           DefaultHTTPMaxRetries,
		MaxConcurrentRequests:    DefaultMaxConcurrentRequests,
		SchedulerJitter:          time.Duration(DefaultSchedulerJitterSeconds) * time.Second,
		TLSVerify:                true,
	}

	var err error
	if env.LogLevel, err = logLevelValue(lookup, "OEM_LOG_LEVEL", DefaultLogLevel); err != nil {
		return Env{}, err
	}
	if env.ValidateConfig, err = boolValue(lookup, "OEM_VALIDATE_CONFIG", false); err != nil {
		return Env{}, err
	}
	if env.ExportInterval, err = secondsValue(lookup, "OEM_EXPORT_INTERVAL_SECONDS", DefaultExportIntervalSeconds); err != nil {
		return Env{}, err
	}
	if env.OTELExportTimeout, err = secondsValue(lookup, "OTEL_EXPORT_TIMEOUT_SECONDS", DefaultOTELExportTimeoutSeconds); err != nil {
		return Env{}, err
	}
	if env.MonitorResponseTolerance, err = minutesValue(lookup, "OEM_MONITOR_RESPONSE_TOLERANCE_MINUTES", DefaultResponseToleranceMin); err != nil {
		return Env{}, err
	}
	if env.HTTPTimeout, err = secondsValue(lookup, "OEM_HTTP_TIMEOUT_SECONDS", DefaultHTTPTimeoutSeconds); err != nil {
		return Env{}, err
	}
	if env.HTTPConnectTimeout, err = secondsValue(lookup, "OEM_HTTP_CONNECT_TIMEOUT_SECONDS", DefaultConnectTimeoutSeconds); err != nil {
		return Env{}, err
	}
	if env.HTTPMaxRetries, err = nonNegativeIntValue(lookup, "OEM_HTTP_MAX_RETRIES", DefaultHTTPMaxRetries); err != nil {
		return Env{}, err
	}
	if env.MaxConcurrentRequests, err = positiveIntValue(lookup, "OEM_MAX_CONCURRENT_REQUESTS", DefaultMaxConcurrentRequests); err != nil {
		return Env{}, err
	}
	if env.SchedulerJitter, err = nonNegativeSecondsValue(lookup, "OEM_SCHEDULER_JITTER_SECONDS", DefaultSchedulerJitterSeconds); err != nil {
		return Env{}, err
	}
	if env.TLSVerify, err = boolValue(lookup, "OEM_TLS_VERIFY", true); err != nil {
		return Env{}, err
	}
	if env.DiagnosticsInterval, err = nonNegativeSecondsValue(lookup, "OEM_DIAGNOSTICS_INTERVAL_SECONDS", 0); err != nil {
		return Env{}, err
	}

	return env, nil
}

// DefaultReportPath derives the validation report path from the validated
// target YAML path by inserting ".report" before the file extension.
func DefaultReportPath(validatedConfigOutput string) string {
	path := strings.TrimSpace(validatedConfigOutput)
	if path == "" {
		return DefaultValidationReportPath
	}
	ext := filepath.Ext(path)
	if ext == "" {
		return path + ".report.yaml"
	}
	return strings.TrimSuffix(path, ext) + ".report" + ext
}

// Load reads targets and metrics YAML files.
func Load(targetsPath, metricsPath string) (Config, error) {
	sites, err := LoadTargets(targetsPath)
	if err != nil {
		return Config{}, err
	}
	metrics, err := LoadMetrics(metricsPath)
	if err != nil {
		return Config{}, err
	}
	return Config{Sites: sites, Metrics: metrics}, nil
}

// LoadTargets reads and validates configTargets.yaml.
func LoadTargets(path string) ([]SiteConfig, error) {
	var sites []SiteConfig
	if err := readYAML(path, &sites); err != nil {
		return nil, fmt.Errorf("carregar targets %q: %w", path, err)
	}
	if err := validateTargets(sites); err != nil {
		return nil, fmt.Errorf("validar targets %q: %w", path, err)
	}
	return sites, nil
}

// LoadMetrics reads and validates configMetrics.yaml.
func LoadMetrics(path string) (MetricsConfig, error) {
	var raw map[string][]rawMetricGroup
	if err := readYAML(path, &raw); err != nil {
		return nil, fmt.Errorf("carregar metricas %q: %w", path, err)
	}
	metrics, err := validateMetrics(raw)
	if err != nil {
		return nil, fmt.Errorf("validar metricas %q: %w", path, err)
	}
	return metrics, nil
}

type rawMetricGroup struct {
	Freq            *int   `yaml:"freq"`
	MetricGroupName string `yaml:"metric_group_name"`
}

func readYAML(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return errors.New("arquivo YAML vazio")
	}
	if err := yaml.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("YAML invalido: %w", err)
	}
	return nil
}

func validateTargets(sites []SiteConfig) error {
	if len(sites) == 0 {
		return errors.New("configTargets.yaml deve conter ao menos um site")
	}
	for siteIndex, site := range sites {
		sitePath := fmt.Sprintf("site[%d]", siteIndex)
		if strings.TrimSpace(site.Endpoint) == "" {
			return fmt.Errorf("%s.endpoint: campo obrigatorio", sitePath)
		}
		if len(site.Targets) == 0 {
			return fmt.Errorf("%s.targets: informe ao menos um target", sitePath)
		}
		for targetIndex, target := range site.Targets {
			targetPath := fmt.Sprintf("%s.targets[%d]", sitePath, targetIndex)
			if strings.TrimSpace(target.ID) == "" {
				return fmt.Errorf("%s.id: campo obrigatorio", targetPath)
			}
			if strings.TrimSpace(target.Name) == "" {
				return fmt.Errorf("%s.name: campo obrigatorio", targetPath)
			}
			if strings.TrimSpace(target.TypeName) == "" {
				return fmt.Errorf("%s.typeName: campo obrigatorio", targetPath)
			}
			if len(target.Tags) == 0 {
				return fmt.Errorf("%s.tags: informe tags do target", targetPath)
			}
			if strings.TrimSpace(target.Tags["target_name"]) == "" {
				return fmt.Errorf("%s.tags.target_name: campo obrigatorio", targetPath)
			}
			if strings.TrimSpace(target.Tags["target_type"]) == "" {
				return fmt.Errorf("%s.tags.target_type: campo obrigatorio", targetPath)
			}
			if target.Tags["target_type"] != target.TypeName {
				return fmt.Errorf("%s.tags.target_type: esperado %q, encontrado %q", targetPath, target.TypeName, target.Tags["target_type"])
			}
			if expectedName := expectedTargetNameTag(target); target.Tags["target_name"] != expectedName {
				return fmt.Errorf("%s.tags.target_name: esperado %q, encontrado %q", targetPath, expectedName, target.Tags["target_name"])
			}
		}
	}
	return nil
}

func expectedTargetNameTag(target TargetConfig) string {
	switch target.TypeName {
	case "host":
		return strings.Split(target.Name, ".")[0]
	case "oracle_listener":
		listenerName := strings.ReplaceAll(target.Name, "LISTENER_", "")
		return strings.Split(listenerName, ".")[0] + "_lstnr"
	default:
		return target.Name
	}
}

func validateMetrics(raw map[string][]rawMetricGroup) (MetricsConfig, error) {
	if len(raw) == 0 {
		return nil, errors.New("configMetrics.yaml deve conter ao menos um tipo de target")
	}

	metrics := make(MetricsConfig, len(raw))
	for targetType, groups := range raw {
		if strings.TrimSpace(targetType) == "" {
			return nil, errors.New("tipo de target vazio em configMetrics.yaml")
		}
		if len(groups) == 0 {
			return nil, fmt.Errorf("%s: informe ao menos um grupo de metricas", targetType)
		}
		for groupIndex, group := range groups {
			groupPath := fmt.Sprintf("%s[%d]", targetType, groupIndex)
			if group.Freq == nil {
				return nil, fmt.Errorf("%s.freq: campo obrigatorio", groupPath)
			}
			if *group.Freq <= 0 {
				return nil, fmt.Errorf("%s.freq: deve ser maior que zero minutos", groupPath)
			}
			if strings.TrimSpace(group.MetricGroupName) == "" {
				return nil, fmt.Errorf("%s.metric_group_name: campo obrigatorio", groupPath)
			}
			metrics[targetType] = append(metrics[targetType], MetricGroupConfig{
				Freq:            *group.Freq,
				MetricGroupName: group.MetricGroupName,
			})
		}
	}
	return metrics, nil
}

func stringValue(lookup func(string) (string, bool), name, fallback string) string {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func boolValue(lookup func(string) (string, bool), name string, fallback bool) (bool, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s: use true ou false", name)
	}
}

func logLevelValue(lookup func(string) (string, bool), name, fallback string) (string, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return "debug", nil
	case "info":
		return "info", nil
	case "warn", "warning":
		return "warn", nil
	case "error":
		return "error", nil
	default:
		return "", fmt.Errorf("%s: use debug, info, warn ou error", name)
	}
}

func secondsValue(lookup func(string) (string, bool), name string, fallback int) (time.Duration, error) {
	value, err := positiveIntValue(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * time.Second, nil
}

func minutesValue(lookup func(string) (string, bool), name string, fallback int) (time.Duration, error) {
	value, err := positiveIntValue(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * time.Minute, nil
}

func nonNegativeSecondsValue(lookup func(string) (string, bool), name string, fallback int) (time.Duration, error) {
	value, err := nonNegativeIntValue(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * time.Second, nil
}

func positiveIntValue(lookup func(string) (string, bool), name string, fallback int) (int, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s: informe um numero inteiro maior que zero", name)
	}
	return parsed, nil
}

func nonNegativeIntValue(lookup func(string) (string, bool), name string, fallback int) (int, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s: informe um numero inteiro maior ou igual a zero", name)
	}
	return parsed, nil
}
