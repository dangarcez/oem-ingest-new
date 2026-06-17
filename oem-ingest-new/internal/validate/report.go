package validate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ValidationReport is the YAML artifact emitted beside the validated target
// configuration. It records what changed during validation.
type ValidationReport struct {
	SourceConfig    string                  `yaml:"sourceConfig"`
	ValidatedConfig string                  `yaml:"validatedConfig"`
	GeneratedAt     string                  `yaml:"generatedAt"`
	Summary         ValidationReportSummary `yaml:"summary"`
	IDCorrections   []IDCorrection          `yaml:"idCorrections"`
	TargetsRemoved  []TargetRemoval         `yaml:"targetsRemoved"`
	SitesRemoved    []SiteRemoval           `yaml:"sitesRemoved"`
	TargetsAdded    []TargetAddition        `yaml:"targetsAdded"`
	TagCorrections  []TagCorrection         `yaml:"tagCorrections"`
	Warnings        []Warning               `yaml:"warnings"`
}

// ValidationReportSummary contains aggregate counts for fast human and machine
// inspection of a validation run.
type ValidationReportSummary struct {
	IDCorrections  int `yaml:"idCorrections"`
	TargetsRemoved int `yaml:"targetsRemoved"`
	SitesRemoved   int `yaml:"sitesRemoved"`
	TargetsAdded   int `yaml:"targetsAdded"`
	TagCorrections int `yaml:"tagCorrections"`
	Warnings       int `yaml:"warnings"`
}

// NewValidationReport builds a report from the two validation phases.
func NewValidationReport(sourceConfig, validatedConfig string, generatedAt time.Time, ids IDValidationResult, correlation CorrelationValidationResult) ValidationReport {
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	warnings := make([]Warning, 0, len(ids.Warnings)+len(correlation.Warnings))
	warnings = append(warnings, ids.Warnings...)
	warnings = append(warnings, correlation.Warnings...)

	return ValidationReport{
		SourceConfig:    sourceConfig,
		ValidatedConfig: validatedConfig,
		GeneratedAt:     generatedAt.UTC().Format(time.RFC3339),
		Summary: ValidationReportSummary{
			IDCorrections:  len(ids.IDCorrections),
			TargetsRemoved: len(ids.TargetRemovals),
			SitesRemoved:   len(ids.SiteRemovals),
			TargetsAdded:   len(correlation.TargetAdds),
			TagCorrections: len(correlation.TagCorrections),
			Warnings:       len(warnings),
		},
		IDCorrections:  append([]IDCorrection(nil), ids.IDCorrections...),
		TargetsRemoved: append([]TargetRemoval(nil), ids.TargetRemovals...),
		SitesRemoved:   append([]SiteRemoval(nil), ids.SiteRemovals...),
		TargetsAdded:   append([]TargetAddition(nil), correlation.TargetAdds...),
		TagCorrections: append([]TagCorrection(nil), correlation.TagCorrections...),
		Warnings:       warnings,
	}
}

// MarshalValidationReport renders a validation report as YAML.
func MarshalValidationReport(report ValidationReport) ([]byte, error) {
	data, err := yaml.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("serializar relatorio de validacao: %w", err)
	}
	return data, nil
}

// WriteValidationReport writes the validation report YAML.
func WriteValidationReport(path string, report ValidationReport) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("caminho de saida do relatorio de validacao nao informado")
	}
	data, err := MarshalValidationReport(report)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("criar diretorio do relatorio de validacao %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("escrever relatorio de validacao %q: %w", path, err)
	}
	return nil
}
