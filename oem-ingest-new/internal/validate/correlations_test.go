package validate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
)

func TestValidateTargetCorrelationsAddsMissingRACInstance(t *testing.T) {
	rac := newTestTarget("rac-id", "cdbp51bc", targetTypeRAC)
	rac.Tags["rac_database"] = "cdbp51bc"
	rac.Tags["sistema"] = "siapx"
	sites := []config.SiteConfig{newTestSite(rac)}

	inventory := fakeTargetInventory{
		targets: []oem.Target{
			apiTarget("sys-id", "cdbp51bc_sys", targetTypeDBSystem),
			apiTarget("rac-id", "cdbp51bc", targetTypeRAC),
			apiTarget("db-id", "cdbp51bc_cdbp51bc1", targetTypeDatabase),
		},
		properties: map[string][]oem.Property{
			"db-id": dbProperties("", "Primary"),
		},
	}

	result, err := ValidateTargetCorrelations(context.Background(), sites, singleInventoryFactory(inventory), CorrelationValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetCorrelations returned error: %v", err)
	}
	if !result.Changed() {
		t.Fatal("expected correlation validation to change config")
	}
	if len(result.TargetAdds) != 2 {
		t.Fatalf("TargetAdds = %#v, want dbsys and oracle_database", result.TargetAdds)
	}

	addedDB := findConfigTarget(t, result.Sites[0].Targets, "cdbp51bc_cdbp51bc1", targetTypeDatabase)
	if addedDB.ID != "db-id" {
		t.Fatalf("added DB ID = %q, want db-id", addedDB.ID)
	}
	wantTags := map[string]string{
		"target_name":     "cdbp51bc_cdbp51bc1",
		"target_type":     targetTypeDatabase,
		"oracle_dbsys":    "cdbp51bc_sys",
		"rac_database":    "cdbp51bc",
		"oracle_database": "cdbp51bc_cdbp51bc1",
		"dg_role":         "Primary",
		"sistema":         "siapx",
	}
	assertTargetTags(t, addedDB, wantTags)

	correctedRAC := findConfigTarget(t, result.Sites[0].Targets, "cdbp51bc", targetTypeRAC)
	if correctedRAC.Tags["oracle_dbsys"] != "cdbp51bc_sys" {
		t.Fatalf("RAC tags were not enriched with dbsys: %#v", correctedRAC.Tags)
	}
	if sites[0].Targets[0].Tags["oracle_dbsys"] != "" {
		t.Fatalf("original config was mutated: %#v", sites[0].Targets[0].Tags)
	}
}

func TestValidateTargetCorrelationsCorrectsHostAndListenerFromDatabaseProperties(t *testing.T) {
	rac := newTestTarget("rac-id", "cdbp51bc", targetTypeRAC)
	database := newTestTarget("db-id", "cdbp51bc_cdbp51bc1", targetTypeDatabase)
	database.Tags["rac_database"] = "cdbp51bc"
	database.Tags["oracle_database"] = "cdbp51bc_cdbp51bc1"
	database.Tags["machine_name"] = "wrong-host"
	database.Tags["listener_name"] = "wrong-host_lstnr"
	wrongHost := newTestTarget("wrong-host-id", "wronghost.example", targetTypeHost)
	wrongHost.Tags["target_name"] = "wronghost"
	wrongHost.Tags[targetTypeHost] = "wronghost"
	wrongListener := newTestTarget("wrong-listener-id", "LISTENER_wronghost.example", targetTypeListener)
	wrongListener.Tags["target_name"] = "wronghost_lstnr"
	wrongListener.Tags[targetTypeListener] = "wronghost_lstnr"
	sites := []config.SiteConfig{newTestSite(rac, database, wrongHost, wrongListener)}

	inventory := fakeTargetInventory{
		targets: []oem.Target{
			apiTarget("rac-id", "cdbp51bc", targetTypeRAC),
			apiTarget("db-id", "cdbp51bc_cdbp51bc1", targetTypeDatabase),
			apiTarget("host-id", "dbhost01.intra.example", targetTypeHost),
			apiTarget("listener-id", "LISTENER_dbhost01.intra.example", targetTypeListener),
		},
		properties: map[string][]oem.Property{
			"db-id": dbProperties("dbhost01.intra.example", "Physical Standby"),
		},
	}

	result, err := ValidateTargetCorrelations(context.Background(), sites, singleInventoryFactory(inventory), CorrelationValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetCorrelations returned error: %v", err)
	}

	correctedDB := findConfigTarget(t, result.Sites[0].Targets, "cdbp51bc_cdbp51bc1", targetTypeDatabase)
	if correctedDB.Tags["machine_name"] != "dbhost01" {
		t.Fatalf("machine_name = %q, want dbhost01; tags=%#v", correctedDB.Tags["machine_name"], correctedDB.Tags)
	}
	if correctedDB.Tags["listener_name"] != "dbhost01_lstnr" {
		t.Fatalf("listener_name = %q, want dbhost01_lstnr; tags=%#v", correctedDB.Tags["listener_name"], correctedDB.Tags)
	}
	correctHost := findConfigTarget(t, result.Sites[0].Targets, "dbhost01.intra.example", targetTypeHost)
	assertTargetTags(t, correctHost, map[string]string{
		"target_name": "dbhost01",
		"target_type": targetTypeHost,
		"host":        "dbhost01",
	})
	correctListener := findConfigTarget(t, result.Sites[0].Targets, "LISTENER_dbhost01.intra.example", targetTypeListener)
	assertTargetTags(t, correctListener, map[string]string{
		"target_name":     "dbhost01_lstnr",
		"target_type":     targetTypeListener,
		"oracle_listener": "dbhost01_lstnr",
	})
	_ = findConfigTarget(t, result.Sites[0].Targets, "wronghost.example", targetTypeHost)
	_ = findConfigTarget(t, result.Sites[0].Targets, "LISTENER_wronghost.example", targetTypeListener)
}

func TestValidateTargetCorrelationsExpandsPDBWithStandby(t *testing.T) {
	pdb := newTestTarget("pdb-id", "cdbp51bc_CDBP51BCPDB001", targetTypePDB)
	pdb.Tags["oracle_pdb"] = "cdbp51bc_CDBP51BCPDB001"
	pdb.Tags["sistema"] = "siapx"
	sites := []config.SiteConfig{newTestSite(pdb)}

	inventory := fakeTargetInventory{
		targets: []oem.Target{
			apiTarget("sys-primary-id", "cdbp51bc_sys", targetTypeDBSystem),
			apiTarget("rac-primary-id", "cdbp51bc", targetTypeRAC),
			apiTarget("pdb-id", "cdbp51bc_CDBP51BCPDB001", targetTypePDB),
			apiTarget("db-primary-id", "cdbp51bc_cdbp51bc1", targetTypeDatabase),
			apiTarget("sys-standby-id", "cdbs51bc_sys", targetTypeDBSystem),
			apiTarget("rac-standby-id", "cdbs51bc", targetTypeRAC),
			apiTarget("pdb-standby-id", "cdbs51bc_CDBP51BCPDB001", targetTypePDB),
			apiTarget("db-standby-id", "cdbs51bc_cdbs51bc1", targetTypeDatabase),
		},
		properties: map[string][]oem.Property{
			"db-primary-id": dbProperties("", "Primary"),
			"db-standby-id": dbProperties("", "Physical Standby"),
		},
	}

	result, err := ValidateTargetCorrelations(context.Background(), sites, singleInventoryFactory(inventory), CorrelationValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetCorrelations returned error: %v", err)
	}

	standbyPDB := findConfigTarget(t, result.Sites[0].Targets, "cdbs51bc_CDBP51BCPDB001", targetTypePDB)
	assertTargetTags(t, standbyPDB, map[string]string{
		"target_name":  "cdbs51bc_CDBP51BCPDB001",
		"target_type":  targetTypePDB,
		"oracle_dbsys": "cdbs51bc_sys",
		"rac_database": "cdbs51bc",
		"oracle_pdb":   "cdbs51bc_CDBP51BCPDB001",
		"sistema":      "siapx",
	})
	standbyDB := findConfigTarget(t, result.Sites[0].Targets, "cdbs51bc_cdbs51bc1", targetTypeDatabase)
	if standbyDB.Tags["dg_role"] != "Physical Standby" {
		t.Fatalf("standby dg_role = %q, want Physical Standby", standbyDB.Tags["dg_role"])
	}
}

func TestValidateTargetCorrelationsPreservesStandaloneTarget(t *testing.T) {
	host := newTestTarget("host-id", "dbhost01.intra.example", targetTypeHost)
	host.Tags["target_name"] = "dbhost01"
	host.Tags[targetTypeHost] = "dbhost01"
	host.Tags["sistema"] = "siapx"
	sites := []config.SiteConfig{newTestSite(host)}

	result, err := ValidateTargetCorrelations(context.Background(), sites, singleInventoryFactory(fakeTargetInventory{
		targets: []oem.Target{apiTarget("host-id", "dbhost01.intra.example", targetTypeHost)},
	}), CorrelationValidationOptions{Enabled: true})
	if err != nil {
		t.Fatalf("ValidateTargetCorrelations returned error: %v", err)
	}
	if result.Changed() {
		t.Fatalf("standalone target should be preserved without changes: %#v", result)
	}
	assertTargetTags(t, result.Sites[0].Targets[0], host.Tags)
}

func TestValidateTargetCorrelationsDisabledSkipsFactoryAndClonesConfig(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("rac-id", "cdbp51bc", targetTypeRAC))}
	result, err := ValidateTargetCorrelations(context.Background(), sites, func(config.SiteConfig) (TargetInventory, error) {
		t.Fatal("factory should not be called when validation is disabled")
		return nil, nil
	}, CorrelationValidationOptions{})
	if err != nil {
		t.Fatalf("ValidateTargetCorrelations returned error: %v", err)
	}
	result.Sites[0].Targets[0].Tags["target_name"] = "changed"
	if sites[0].Targets[0].Tags["target_name"] != "cdbp51bc" {
		t.Fatalf("disabled validation should still clone input tags, got %#v", sites[0].Targets[0].Tags)
	}
}

func TestValidateTargetCorrelationsReturnsFactoryAndListErrors(t *testing.T) {
	sites := []config.SiteConfig{newTestSite(newTestTarget("rac-id", "cdbp51bc", targetTypeRAC))}
	_, err := ValidateTargetCorrelations(context.Background(), sites, func(config.SiteConfig) (TargetInventory, error) {
		return nil, errors.New("factory failed")
	}, CorrelationValidationOptions{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "criar cliente OEM") {
		t.Fatalf("expected factory error with context, got %v", err)
	}

	_, err = ValidateTargetCorrelations(context.Background(), sites, singleInventoryFactory(fakeTargetInventory{listErr: errors.New("list failed")}), CorrelationValidationOptions{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "listar targets OEM") {
		t.Fatalf("expected list error with context, got %v", err)
	}
}

func singleInventoryFactory(inventory fakeTargetInventory) TargetInventoryFactory {
	return func(config.SiteConfig) (TargetInventory, error) {
		return inventory, nil
	}
}

type fakeTargetInventory struct {
	targets    []oem.Target
	properties map[string][]oem.Property
	listErr    error
}

func (f fakeTargetInventory) ListTargets(context.Context) (oem.Page[oem.Target], error) {
	if f.listErr != nil {
		return oem.Page[oem.Target]{}, f.listErr
	}
	return oem.Page[oem.Target]{Items: f.targets}, nil
}

func (f fakeTargetInventory) TargetProperties(_ context.Context, targetID string) (oem.Page[oem.Property], error) {
	return oem.Page[oem.Property]{Items: f.properties[targetID]}, nil
}

func apiTarget(id, name, targetType string) oem.Target {
	return oem.Target{ID: id, Name: name, TypeName: targetType}
}

func dbProperties(machineName, dgRole string) []oem.Property {
	var properties []oem.Property
	if machineName != "" {
		properties = append(properties, oem.Property{ID: "MachineName", Value: machineName})
	}
	if dgRole != "" {
		properties = append(properties, oem.Property{ID: "DataGuardStatus", Value: dgRole})
	}
	return properties
}

func findConfigTarget(t *testing.T, targets []config.TargetConfig, name, targetType string) config.TargetConfig {
	t.Helper()

	for _, target := range targets {
		if target.Name == name && target.TypeName == targetType {
			return target
		}
	}
	t.Fatalf("target %q tipo %q nao encontrado em %#v", name, targetType, targets)
	return config.TargetConfig{}
}

func assertTargetTags(t *testing.T, target config.TargetConfig, want map[string]string) {
	t.Helper()

	for key, value := range want {
		if target.Tags[key] != value {
			t.Fatalf("%s tag %q = %q, want %q; all tags=%#v", target.Name, key, target.Tags[key], value, target.Tags)
		}
	}
}
