package validate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
)

func TestValidateTargetIDsKeepsCorrectID(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("current-id", "cdbp51bc", "rac_database"))}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{{ID: "current-id", Name: "cdbp51bc", TypeName: "rac_database"}},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	if result.Changed() {
		t.Fatalf("result should not be changed: %#v", result)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", result.Warnings)
	}
	if result.Sites[0].Targets[0].ID != "current-id" {
		t.Fatalf("target ID = %q, want current-id", result.Sites[0].Targets[0].ID)
	}
}

func TestValidateTargetIDsCorrectsDivergentIDAndDoesNotMutateOriginal(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("stale-id", "cdbp51bc", "rac_database"))}
	sites[0].Targets[0].Tags["sistema"] = "siapx"
	logger := &recordingLogger{}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{{ID: "current-id", Name: "cdbp51bc", TypeName: "rac_database"}},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{
		Enabled: true,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}

	if !result.Changed() {
		t.Fatal("result should be changed")
	}
	if result.Sites[0].Targets[0].ID != "current-id" {
		t.Fatalf("corrected ID = %q, want current-id", result.Sites[0].Targets[0].ID)
	}
	if result.Sites[0].Targets[0].Tags["sistema"] != "siapx" {
		t.Fatalf("external tags were not preserved: %#v", result.Sites[0].Targets[0].Tags)
	}
	if sites[0].Targets[0].ID != "stale-id" {
		t.Fatalf("original config was mutated: %#v", sites[0].Targets[0])
	}
	if len(result.IDCorrections) != 1 {
		t.Fatalf("IDCorrections = %#v, want one correction", result.IDCorrections)
	}
	correction := result.IDCorrections[0]
	if correction.OldID != "stale-id" || correction.NewID != "current-id" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
	assertWarning(t, result.Warnings, WarningIDDivergent)
	if len(logger.messages) != 1 || !strings.Contains(logger.messages[0], "diverge") {
		t.Fatalf("expected divergent ID warning log, got %#v", logger.messages)
	}
}

func TestValidateTargetIDsNormalizesConfiguredIDWhitespace(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget(" current-id ", "cdbp51bc", "rac_database"))}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{{ID: "current-id", Name: "cdbp51bc", TypeName: "rac_database"}},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	if !result.Changed() {
		t.Fatal("result should be changed")
	}
	if result.Sites[0].Targets[0].ID != "current-id" {
		t.Fatalf("corrected ID = %q, want current-id", result.Sites[0].Targets[0].ID)
	}
	correction := result.IDCorrections[0]
	if correction.OldID != " current-id " || correction.NewID != "current-id" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
	assertWarning(t, result.Warnings, WarningIDDivergent)
}

func TestValidateTargetIDsWarnsAndRemovesMissingTarget(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("configured-id", "missing", "oracle_database"))}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{{ID: "other-id", Name: "other", TypeName: "oracle_database"}},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	if !result.Changed() {
		t.Fatal("missing target should change config")
	}
	if len(result.Sites) != 0 {
		t.Fatalf("site with only missing targets should be removed: %#v", result.Sites)
	}
	if len(result.TargetRemovals) != 1 {
		t.Fatalf("TargetRemovals = %#v, want one removal", result.TargetRemovals)
	}
	removal := result.TargetRemovals[0]
	if removal.ConfigID != "configured-id" || removal.TargetName != "missing" || removal.Reason != WarningTargetMissing {
		t.Fatalf("unexpected removal: %#v", removal)
	}
	if len(result.SiteRemovals) != 1 || result.SiteRemovals[0].RemovedTargets != 1 {
		t.Fatalf("SiteRemovals = %#v, want one removed site", result.SiteRemovals)
	}
	assertWarning(t, result.Warnings, WarningTargetMissing)
}

func TestValidateTargetIDsRemovesWhenAPITargetHasNoID(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("configured-id", "cdbp51bc", "rac_database"))}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{{ID: " ", Name: "cdbp51bc", TypeName: "rac_database"}},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	if !result.Changed() {
		t.Fatal("API target without ID should remove configured target")
	}
	if len(result.Sites) != 0 || len(result.TargetRemovals) != 1 {
		t.Fatalf("unexpected result after missing ID: %#v", result)
	}
	assertWarning(t, result.Warnings, WarningTargetMissing)
}

func TestValidateTargetIDsRemovesMultipleMissingTargetsAndPreservesRemainingOrder(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(
		newTestTarget("missing-1", "missing1", "host"),
		newTestTarget("keep-1", "host1", "host"),
		newTestTarget("missing-2", "missing2", "host"),
		newTestTarget("keep-2", "host2", "host"),
	)}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{
			{ID: "keep-1", Name: "host1", TypeName: "host"},
			{ID: "keep-2", Name: "host2", TypeName: "host"},
		},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	if len(result.TargetRemovals) != 2 {
		t.Fatalf("TargetRemovals = %#v, want two removals", result.TargetRemovals)
	}
	if len(result.SiteRemovals) != 0 {
		t.Fatalf("site should remain because targets were kept: %#v", result.SiteRemovals)
	}
	if len(result.Sites) != 1 || len(result.Sites[0].Targets) != 2 {
		t.Fatalf("unexpected filtered sites: %#v", result.Sites)
	}
	gotNames := []string{result.Sites[0].Targets[0].Name, result.Sites[0].Targets[1].Name}
	wantNames := []string{"host1", "host2"}
	if gotNames[0] != wantNames[0] || gotNames[1] != wantNames[1] {
		t.Fatalf("remaining target order = %#v, want %#v", gotNames, wantNames)
	}
	if sites[0].Targets[0].Name != "missing1" || len(sites[0].Targets) != 4 {
		t.Fatalf("original config was mutated: %#v", sites[0].Targets)
	}
}

func TestValidateTargetIDsWarnsAndKeepsDuplicatedTarget(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("configured-id", "cdbp51bc", "rac_database"))}
	factory := singleListerFactory(fakeTargetLister{
		targets: []oem.Target{
			{ID: "current-id-1", Name: "cdbp51bc", TypeName: "rac_database"},
			{ID: "current-id-2", Name: "cdbp51bc", TypeName: "rac_database"},
		},
	})

	result, err := ValidateTargetIDs(context.Background(), sites, factory, IDValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	if result.Changed() {
		t.Fatalf("duplicate target should not change config: %#v", result)
	}
	if result.Sites[0].Targets[0].ID != "configured-id" {
		t.Fatalf("target ID = %q, want configured-id", result.Sites[0].Targets[0].ID)
	}
	warning := assertWarning(t, result.Warnings, WarningTargetDuplicate)
	if warning.Count != 2 {
		t.Fatalf("duplicate count = %d, want 2", warning.Count)
	}
}

func TestValidateTargetIDsDisabledSkipsFactoryAndClonesConfig(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("configured-id", "cdbp51bc", "rac_database"))}
	result, err := ValidateTargetIDs(context.Background(), sites, func(config.SiteConfig) (TargetLister, error) {
		t.Fatal("factory should not be called when validation is disabled")
		return nil, nil
	}, IDValidationOptions{})
	if err != nil {
		t.Fatalf("ValidateTargetIDs returned error: %v", err)
	}
	result.Sites[0].Targets[0].Tags["target_name"] = "changed"
	if sites[0].Targets[0].Tags["target_name"] != "cdbp51bc" {
		t.Fatalf("disabled validation should still clone input tags, got %#v", sites[0].Targets[0].Tags)
	}
}

func TestValidateTargetIDsReturnsFactoryAndListErrors(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("configured-id", "cdbp51bc", "rac_database"))}
	_, err := ValidateTargetIDs(context.Background(), sites, func(config.SiteConfig) (TargetLister, error) {
		return nil, errors.New("factory failed")
	}, IDValidationOptions{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "criar cliente OEM") {
		t.Fatalf("expected factory error with context, got %v", err)
	}

	_, err = ValidateTargetIDs(context.Background(), sites, singleListerFactory(fakeTargetLister{err: errors.New("list failed")}), IDValidationOptions{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "listar targets OEM") {
		t.Fatalf("expected list error with context, got %v", err)
	}
}

func assertWarning(t *testing.T, warnings []Warning, code WarningCode) Warning {
	t.Helper()

	for _, warning := range warnings {
		if warning.Code == code {
			return warning
		}
	}
	t.Fatalf("expected warning %q, got %#v", code, warnings)
	return Warning{}
}

func singleListerFactory(lister fakeTargetLister) TargetListerFactory {
	return func(config.SiteConfig) (TargetLister, error) {
		return lister, nil
	}
}

type fakeTargetLister struct {
	targets []oem.Target
	err     error
}

func (f fakeTargetLister) ListTargets(context.Context) (oem.Page[oem.Target], error) {
	if f.err != nil {
		return oem.Page[oem.Target]{}, f.err
	}
	return oem.Page[oem.Target]{Items: f.targets}, nil
}

type recordingLogger struct {
	messages []string
}

func (r *recordingLogger) WarnContext(_ context.Context, msg string, _ ...any) {
	r.messages = append(r.messages, msg)
}

func newTestSite(targets ...config.TargetConfig) config.SiteConfig {
	return config.SiteConfig{
		Name:     "oraemc",
		Endpoint: "http://oem.example",
		Targets:  targets,
	}
}

func newTestTarget(id, name, targetType string) config.TargetConfig {
	return config.TargetConfig{
		ID:       id,
		Name:     name,
		TypeName: targetType,
		Tags: map[string]string{
			"target_name": name,
			"target_type": targetType,
		},
	}
}
