package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteValidationReportWritesJSONLEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "configTargets.validated.report.jsonl")
	generatedAt := time.Date(2026, 6, 16, 12, 30, 0, 123, time.UTC)
	ids := IDValidationResult{
		IDCorrections: []IDCorrection{{
			SiteIndex:   0,
			TargetIndex: 1,
			SiteName:    "oraemc",
			TargetName:  "db1",
			TargetType:  "oracle_database",
			OldID:       "old-id",
			NewID:       "new-id",
		}},
		TargetRemovals: []TargetRemoval{{
			SiteIndex:   0,
			TargetIndex: 2,
			SiteName:    "oraemc",
			TargetName:  "missing",
			TargetType:  "host",
			ConfigID:    "missing-id",
			Reason:      WarningTargetMissing,
		}},
		SiteRemovals: []SiteRemoval{{
			SiteIndex:      1,
			SiteName:       "empty",
			Endpoint:       "http://oem-empty.example",
			RemovedTargets: 1,
		}},
		Warnings: []Warning{{
			Code:       WarningTargetMissing,
			SiteName:   "oraemc",
			TargetName: "missing",
			TargetType: "host",
			ConfigID:   "missing-id",
			Message:    "target removido",
		}},
	}
	correlation := CorrelationValidationResult{
		TargetAdds: []TargetAddition{{
			SiteIndex:      0,
			TargetIndex:    3,
			SiteName:       "oraemc",
			TargetName:     "host1",
			TargetType:     "host",
			SourceRootName: "rac1",
			SourceRootType: "rac_database",
		}},
		TagCorrections: []TagCorrection{{
			SiteIndex:   0,
			TargetIndex: 0,
			SiteName:    "oraemc",
			TargetName:  "rac1",
			TargetType:  "rac_database",
		}},
	}
	events := NewStartupValidationEvents("configTargets.yaml", "configTargets.validated.yaml", generatedAt, ids, correlation)

	if err := WriteValidationReport(path, events); err != nil {
		t.Fatalf("WriteValidationReport returned error: %v", err)
	}

	got := readJSONLReport(t, path)
	if len(got) != 6 {
		t.Fatalf("events len = %d, want 6: %#v", len(got), got)
	}
	assertJSONLEvent(t, got[0], ReportEventIDCorrection, map[string]any{
		"timestamp": "2026-06-16T12:30:00.000000123Z",
		"phase":     ReportPhaseStartup,
		"oldID":     "old-id",
		"newID":     "new-id",
	})
	assertJSONLEvent(t, got[1], ReportEventTargetRemoved, map[string]any{
		"reason":     string(WarningTargetMissing),
		"targetName": "missing",
	})
	assertJSONLEvent(t, got[2], ReportEventSiteRemoved, map[string]any{
		"siteName":       "empty",
		"removedTargets": float64(1),
	})
	assertJSONLEvent(t, got[3], ReportEventTargetAdded, map[string]any{
		"sourceRootName": "rac1",
	})
	assertJSONLEvent(t, got[4], ReportEventTagCorrection, map[string]any{
		"targetName": "rac1",
	})
	assertJSONLEvent(t, got[5], ReportEventWarning, map[string]any{
		"code":    string(WarningTargetMissing),
		"message": "target removido",
	})
}

func TestAppendValidationReportEventAppendsOneJSONLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.jsonl")
	first := ValidationReportEvent{"timestamp": "2026-06-16T12:30:00Z", "event": "first"}
	second := ValidationReportEvent{"timestamp": "2026-06-16T12:31:00Z", "event": "second"}

	if err := WriteValidationReport(path, []ValidationReportEvent{first}); err != nil {
		t.Fatalf("WriteValidationReport returned error: %v", err)
	}
	if err := AppendValidationReportEvent(path, second); err != nil {
		t.Fatalf("AppendValidationReportEvent returned error: %v", err)
	}

	got := readJSONLReport(t, path)
	if len(got) != 2 || got[0]["event"] != "first" || got[1]["event"] != "second" {
		t.Fatalf("unexpected appended report: %#v", got)
	}
}

func readJSONLReport(t *testing.T, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		if _, ok := event["summary"]; ok {
			t.Fatalf("JSONL event should not include fixed YAML summary: %#v", event)
		}
		events = append(events, event)
	}
	return events
}

func assertJSONLEvent(t *testing.T, event map[string]any, eventName string, fields map[string]any) {
	t.Helper()

	if event["event"] != eventName {
		t.Fatalf("event name = %#v, want %q in %#v", event["event"], eventName, event)
	}
	if event["timestamp"] == "" {
		t.Fatalf("event missing timestamp: %#v", event)
	}
	for key, want := range fields {
		if got := event[key]; got != want {
			t.Fatalf("%s = %#v, want %#v in %#v", key, got, want, event)
		}
	}
}
