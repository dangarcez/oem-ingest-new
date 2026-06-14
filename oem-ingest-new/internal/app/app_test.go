package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	warnings []string
	infos    []string
}

func (r *appRecordingLogger) WarnContext(_ context.Context, msg string, _ ...any) {
	r.warnings = append(r.warnings, msg)
}

func (r *appRecordingLogger) InfoContext(_ context.Context, msg string, _ ...any) {
	r.infos = append(r.infos, msg)
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
