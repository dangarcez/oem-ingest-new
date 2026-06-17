package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteValidationReportWritesStructuredYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "configTargets.validated.report.yaml")
	generatedAt := time.Date(2026, 6, 16, 12, 30, 0, 0, time.UTC)
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
	report := NewValidationReport("configTargets.yaml", "configTargets.validated.yaml", generatedAt, ids, correlation)

	if err := WriteValidationReport(path, report); err != nil {
		t.Fatalf("WriteValidationReport returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"sourceConfig: configTargets.yaml",
		"validatedConfig: configTargets.validated.yaml",
		"generatedAt: \"2026-06-16T12:30:00Z\"",
		"idCorrections: 1",
		"targetsRemoved: 1",
		"sitesRemoved: 1",
		"targetsAdded: 1",
		"tagCorrections: 1",
		"oldID: old-id",
		"newID: new-id",
		"reason: target_missing",
		"sourceRootName: rac1",
		"message: target removido",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q:\n%s", want, text)
		}
	}
}
