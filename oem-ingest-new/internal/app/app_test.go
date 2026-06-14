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

func TestRunValidatesTargetIDsWhenEnabled(t *testing.T) {
	targetsPath := writeTargetsFile(t, "stale-id")
	var output bytes.Buffer
	factoryCalled := false

	factory := func(site config.SiteConfig) (validate.TargetLister, error) {
		factoryCalled = true
		if site.Name != "oraemc" || site.Endpoint != "http://oem.example" {
			t.Fatalf("unexpected site passed to factory: %#v", site)
		}
		return appFakeTargetLister{
			targets: []oem.Target{{ID: "current-id", Name: "cdbp51bc", TypeName: "rac_database"}},
		}, nil
	}

	err := Run(context.Background(), Options{
		Output:              &output,
		LookupEnv:           mapLookup(map[string]string{"OEM_VALIDATE_CONFIG": "true", "OEM_CONFIG_TARGETS": targetsPath}),
		TargetListerFactory: factory,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !factoryCalled {
		t.Fatal("expected validation factory to be called")
	}
	if !strings.Contains(output.String(), "validacao de IDs concluida: 1 correcoes, 1 avisos") {
		t.Fatalf("expected validation summary, got %q", output.String())
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

type appFakeTargetLister struct {
	targets []oem.Target
}

func (f appFakeTargetLister) ListTargets(context.Context) (oem.Page[oem.Target], error) {
	return oem.Page[oem.Target]{Items: f.targets}, nil
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
