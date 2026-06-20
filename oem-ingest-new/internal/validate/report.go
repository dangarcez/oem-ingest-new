package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ReportPhaseStartup = "startup"
	ReportPhaseRuntime = "runtime"
	ReportTrigger404   = "metric_404"

	ReportEventIDCorrection  = "id_correction"
	ReportEventTargetRemoved = "target_removed"
	ReportEventSiteRemoved   = "site_removed"
	ReportEventTargetAdded   = "target_added"
	ReportEventTagCorrection = "tag_correction"
	ReportEventWarning       = "warning"
)

// ValidationReportEvent is one JSONL event emitted by validation. The fields
// are intentionally dynamic because startup and runtime corrections carry
// different details.
type ValidationReportEvent map[string]any

// NewStartupValidationEvents builds append-only JSONL events from the startup
// validation phases.
func NewStartupValidationEvents(sourceConfig, validatedConfig string, generatedAt time.Time, ids IDValidationResult, correlation CorrelationValidationResult) []ValidationReportEvent {
	generatedAt = reportTime(generatedAt)
	events := make([]ValidationReportEvent, 0,
		len(ids.IDCorrections)+
			len(ids.TargetRemovals)+
			len(ids.SiteRemovals)+
			len(correlation.TargetAdds)+
			len(correlation.TagCorrections)+
			len(ids.Warnings)+
			len(correlation.Warnings),
	)

	for _, correction := range ids.IDCorrections {
		event := baseReportEvent(ReportEventIDCorrection, ReportPhaseStartup, sourceConfig, validatedConfig, generatedAt)
		addIDCorrection(event, correction)
		events = append(events, event)
	}
	for _, removal := range ids.TargetRemovals {
		event := baseReportEvent(ReportEventTargetRemoved, ReportPhaseStartup, sourceConfig, validatedConfig, generatedAt)
		event["siteIndex"] = removal.SiteIndex
		event["targetIndex"] = removal.TargetIndex
		event["siteName"] = removal.SiteName
		event["targetName"] = removal.TargetName
		event["targetType"] = removal.TargetType
		event["configID"] = removal.ConfigID
		event["reason"] = string(removal.Reason)
		events = append(events, event)
	}
	for _, removal := range ids.SiteRemovals {
		event := baseReportEvent(ReportEventSiteRemoved, ReportPhaseStartup, sourceConfig, validatedConfig, generatedAt)
		event["siteIndex"] = removal.SiteIndex
		event["siteName"] = removal.SiteName
		event["endpoint"] = removal.Endpoint
		event["removedTargets"] = removal.RemovedTargets
		events = append(events, event)
	}
	for _, addition := range correlation.TargetAdds {
		event := baseReportEvent(ReportEventTargetAdded, ReportPhaseStartup, sourceConfig, validatedConfig, generatedAt)
		event["siteIndex"] = addition.SiteIndex
		event["targetIndex"] = addition.TargetIndex
		event["siteName"] = addition.SiteName
		event["targetName"] = addition.TargetName
		event["targetType"] = addition.TargetType
		event["sourceRootName"] = addition.SourceRootName
		event["sourceRootType"] = addition.SourceRootType
		events = append(events, event)
	}
	for _, correction := range correlation.TagCorrections {
		event := baseReportEvent(ReportEventTagCorrection, ReportPhaseStartup, sourceConfig, validatedConfig, generatedAt)
		event["siteIndex"] = correction.SiteIndex
		event["targetIndex"] = correction.TargetIndex
		event["siteName"] = correction.SiteName
		event["targetName"] = correction.TargetName
		event["targetType"] = correction.TargetType
		events = append(events, event)
	}
	for _, warning := range append(append([]Warning(nil), ids.Warnings...), correlation.Warnings...) {
		event := baseReportEvent(ReportEventWarning, ReportPhaseStartup, sourceConfig, validatedConfig, generatedAt)
		addWarning(event, warning)
		events = append(events, event)
	}

	return events
}

// NewRuntimeIDCorrectionEvent builds one runtime correction event for JSONL
// appends after a 404-driven target ID recheck.
func NewRuntimeIDCorrectionEvent(sourceConfig, validatedConfig string, generatedAt time.Time, correction IDCorrection, metricGroupName string) ValidationReportEvent {
	event := baseReportEvent(ReportEventIDCorrection, ReportPhaseRuntime, sourceConfig, validatedConfig, reportTime(generatedAt))
	event["trigger"] = ReportTrigger404
	event["metricGroupName"] = metricGroupName
	addIDCorrection(event, correction)
	return event
}

// MarshalValidationReportEvents renders events as newline-delimited JSON.
func MarshalValidationReportEvents(events []ValidationReportEvent) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return nil, fmt.Errorf("serializar evento do relatorio de validacao: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// WriteValidationReport writes the validation report JSONL, truncating any
// existing file before startup events are written.
func WriteValidationReport(path string, events []ValidationReportEvent) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("caminho de saida do relatorio de validacao nao informado")
	}
	data, err := MarshalValidationReportEvents(events)
	if err != nil {
		return err
	}
	if err := ensureReportDir(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("escrever relatorio de validacao %q: %w", path, err)
	}
	return nil
}

// AppendValidationReportEvent appends one JSON object to an existing JSONL
// validation report. The file is created if startup created no report yet.
func AppendValidationReportEvent(path string, event ValidationReportEvent) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("caminho de saida do relatorio de validacao nao informado")
	}
	data, err := MarshalValidationReportEvents([]ValidationReportEvent{event})
	if err != nil {
		return err
	}
	if err := ensureReportDir(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("abrir relatorio de validacao %q para append: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("anexar evento ao relatorio de validacao %q: %w", path, err)
	}
	return nil
}

func ensureReportDir(path string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("criar diretorio do relatorio de validacao %q: %w", dir, err)
		}
	}
	return nil
}

func baseReportEvent(event, phase, sourceConfig, validatedConfig string, at time.Time) ValidationReportEvent {
	return ValidationReportEvent{
		"timestamp":       at.UTC().Format(time.RFC3339Nano),
		"event":           event,
		"phase":           phase,
		"sourceConfig":    sourceConfig,
		"validatedConfig": validatedConfig,
	}
}

func addIDCorrection(event ValidationReportEvent, correction IDCorrection) {
	event["siteIndex"] = correction.SiteIndex
	event["targetIndex"] = correction.TargetIndex
	event["siteName"] = correction.SiteName
	event["targetName"] = correction.TargetName
	event["targetType"] = correction.TargetType
	event["oldID"] = correction.OldID
	event["newID"] = correction.NewID
}

func addWarning(event ValidationReportEvent, warning Warning) {
	event["code"] = string(warning.Code)
	event["siteIndex"] = warning.SiteIndex
	event["targetIndex"] = warning.TargetIndex
	event["siteName"] = warning.SiteName
	event["targetName"] = warning.TargetName
	event["targetType"] = warning.TargetType
	event["message"] = warning.Message
	if warning.ConfigID != "" {
		event["configID"] = warning.ConfigID
	}
	if warning.CurrentID != "" {
		event["currentID"] = warning.CurrentID
	}
	if warning.Count > 0 {
		event["count"] = warning.Count
	}
}

func reportTime(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now()
	}
	return at
}
