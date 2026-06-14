package transform

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/scheduler"
)

func TestFromCollectionNormalizesNamesAndClassifiesValues(t *testing.T) {
	result := transformResult(
		config.TargetConfig{
			ID:       "target-1",
			Name:     "host01",
			TypeName: "host",
			Tags: map[string]string{
				"target_name": "host01",
				"target_type": "host",
				"sistema":     "pix",
			},
		},
		collect.GroupMetadata{
			TargetID:        "target-1",
			MetricGroupName: "File Systems",
			Keys:            []string{"MountPoint", "FileSystem"},
			MetricByName: map[string]oem.MetricDefinition{
				"Space Used":   {Name: "Space Used", DataType: "NUMBER"},
				"pctAvailable": {Name: "pctAvailable", DataType: "NUMBER"},
				"Status":       {Name: "Status", DataType: "STRING"},
			},
		},
		[]map[string]any{{
			"MountPoint":   "/",
			"FileSystem":   "/dev/vda1",
			"Space Used":   json.Number("39.18"),
			"pctAvailable": "60.82",
			"Status":       json.Number("1"),
		}},
	)

	out := FromCollection(result, Options{})

	if len(out.Metrics) != 2 {
		t.Fatalf("metrics len = %d, want 2: %#v", len(out.Metrics), out.Metrics)
	}
	assertMetric(t, out.Metrics[0], "oem_file_systems_space_used", 39.18)
	assertMetric(t, out.Metrics[1], "oem_file_systems_pctavailable", 60.82)
	if out.Metrics[0].SeriesID != "target-1\x00/\x00/dev/vda1" {
		t.Fatalf("SeriesID = %q, want legacy target/key composition", out.Metrics[0].SeriesID)
	}

	wantAttrs := Attributes{
		"target_name": "host01",
		"target_type": "host",
		"sistema":     "pix",
		"MountPoint":  "/",
		"FileSystem":  "/dev/vda1",
	}
	if !reflect.DeepEqual(out.Metrics[0].Attributes, wantAttrs) {
		t.Fatalf("metric attributes = %#v, want %#v", out.Metrics[0].Attributes, wantAttrs)
	}

	if len(out.Logs) != 1 {
		t.Fatalf("logs len = %d, want 1: %#v", len(out.Logs), out.Logs)
	}
	if out.Logs[0].MetricName != "oem_file_systems_status" || out.Logs[0].Body != "1" {
		t.Fatalf("log = %#v, want metric oem_file_systems_status body 1", out.Logs[0])
	}
	if out.Logs[0].Attributes["metric"] != "oem_file_systems_status" {
		t.Fatalf("log metric attribute = %#v, want lowercase export name", out.Logs[0].Attributes["metric"])
	}
}

func TestFromCollectionKeepsNumericStringsAsLogsWithoutMetadata(t *testing.T) {
	result := transformResult(
		targetConfig("target-1", "db1", "oracle_database"),
		collect.GroupMetadata{
			TargetID:        "target-1",
			MetricGroupName: "Response",
			MetricByName:    map[string]oem.MetricDefinition{},
		},
		[]map[string]any{{"Status": "1"}},
	)

	out := FromCollection(result, Options{})

	if len(out.Metrics) != 0 {
		t.Fatalf("metrics len = %d, want 0 for numeric-looking string without metadata", len(out.Metrics))
	}
	if len(out.Logs) != 1 || out.Logs[0].MetricName != "oem_response_status" || out.Logs[0].Body != "1" {
		t.Fatalf("logs = %#v, want textual oem_response_status body 1", out.Logs)
	}
}

func TestFromCollectionSkipsKeysAsMetrics(t *testing.T) {
	result := transformResult(
		targetConfig("target-1", "db1", "oracle_database"),
		collect.GroupMetadata{
			TargetID:        "target-1",
			MetricGroupName: "ha_rac_intrconn_traffic",
			Keys:            []string{"instance"},
			MetricByName: map[string]oem.MetricDefinition{
				"interconnect_rate": {Name: "interconnect_rate", DataType: "NUMBER"},
			},
		},
		[]map[string]any{{"instance": "db1_1", "interconnect_rate": 10.5}},
	)

	out := FromCollection(result, Options{})

	if len(out.Metrics) != 1 {
		t.Fatalf("metrics = %#v, want only interconnect_rate", out.Metrics)
	}
	if out.Metrics[0].Name != "oem_ha_rac_intrconn_traffic_interconnect_rate" {
		t.Fatalf("metric name = %q", out.Metrics[0].Name)
	}
	if _, ok := out.Metrics[0].Attributes["instance"]; ok {
		t.Fatalf("legacy attribute conflict should rename instance: %#v", out.Metrics[0].Attributes)
	}
	if out.Metrics[0].Attributes["_instance"] != "db1_1" {
		t.Fatalf("_instance attr = %#v, want db1_1", out.Metrics[0].Attributes["_instance"])
	}
}

func TestFromCollectionMarksContinuousTextualMetrics(t *testing.T) {
	result := transformResult(
		targetConfig("target-1", "pdb1", "oracle_pdb"),
		collect.GroupMetadata{
			TargetID:        "target-1",
			MetricGroupName: "DBService",
			MetricByName: map[string]oem.MetricDefinition{
				"status": {Name: "status", DataType: "STRING"},
			},
		},
		[]map[string]any{{"status": "Up"}},
	)

	out := FromCollection(result, Options{Continuous: true})

	if len(out.Logs) != 1 {
		t.Fatalf("logs = %#v, want 1", out.Logs)
	}
	if !out.Logs[0].Continuous {
		t.Fatal("textual metric should preserve continuous flag")
	}
}

func TestFromCollectionWithMockLikeRecoveryAreaFixture(t *testing.T) {
	result := transformResult(
		targetConfig("240D79C7320E221DE06400144FFBE115", "occp40bc", "rac_database"),
		collect.GroupMetadata{
			TargetID:        "240D79C7320E221DE06400144FFBE115",
			MetricGroupName: "Recovery_Area",
			Keys:            []string{"Recovery_Area"},
			MetricByName: map[string]oem.MetricDefinition{
				"Actual_Free_Space": {Name: "Actual_Free_Space", DataType: "NUMBER"},
				"Free_Space":        {Name: "Free_Space", DataType: "NUMBER"},
			},
		},
		[]map[string]any{{
			"Recovery_Area":     "RECOVERY AREA",
			"Actual_Free_Space": 98.5,
			"Free_Space":        json.Number("1.25"),
		}},
	)

	out := FromCollection(result, Options{})

	if len(out.Logs) != 0 {
		t.Fatalf("logs = %#v, want none for numeric Recovery_Area fixture", out.Logs)
	}
	if len(out.Metrics) != 2 {
		t.Fatalf("metrics = %#v, want 2 numeric points", out.Metrics)
	}
	assertMetric(t, out.Metrics[0], "oem_recovery_area_actual_free_space", 98.5)
	assertMetric(t, out.Metrics[1], "oem_recovery_area_free_space", 1.25)
	if out.Metrics[0].Attributes["Recovery_Area"] != "RECOVERY AREA" {
		t.Fatalf("Recovery_Area key attr = %#v", out.Metrics[0].Attributes["Recovery_Area"])
	}
}

func TestNormalizeMetricName(t *testing.T) {
	got := NormalizeMetricName(" General Status ", " Response Time ")
	if got != "oem_general_status_response_time" {
		t.Fatalf("NormalizeMetricName = %q, want oem_general_status_response_time", got)
	}
	if got := NormalizeMetricName("", "Status"); got != "" {
		t.Fatalf("NormalizeMetricName empty group = %q, want empty", got)
	}
}

func assertMetric(t *testing.T, point MetricPoint, name string, value float64) {
	t.Helper()
	if point.Name != name || point.Value != value {
		t.Fatalf("metric = %#v, want %s=%v", point, name, value)
	}
}

func transformResult(target config.TargetConfig, metadata collect.GroupMetadata, items []map[string]any) collect.Result {
	collectedAt := time.Unix(1700000000, 0)
	return collect.Result{
		Job: scheduler.Job{
			Target:          target,
			MetricGroupName: metadata.MetricGroupName,
			MetricGroup:     config.MetricGroupConfig{Freq: 1, MetricGroupName: metadata.MetricGroupName},
		},
		Metadata: metadata,
		LatestData: oem.LatestData{
			TargetName:      target.Name,
			TargetTypeName:  target.TypeName,
			TargetID:        target.ID,
			MetricGroupName: metadata.MetricGroupName,
			Items:           items,
			Count:           len(items),
		},
		CollectedAt: collectedAt,
	}
}

func targetConfig(id, name, targetType string) config.TargetConfig {
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
