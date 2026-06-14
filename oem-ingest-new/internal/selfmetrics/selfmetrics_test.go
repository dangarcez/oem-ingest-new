package selfmetrics

import (
	"strings"
	"testing"
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/transform"
)

func TestFromSnapshotBuildsConfiguredActiveInactiveTargetMetrics(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	monitor := collect.NewResponseMonitor()
	monitor.Mark("host-1", observedAt.Add(-time.Minute))
	monitor.Mark("db-1", observedAt.Add(-30*time.Minute))

	points := FromSnapshot(SnapshotInput{
		Sites: []config.SiteConfig{
			{
				Name:     "oraemc",
				Endpoint: "http://oem.example",
				Targets: []config.TargetConfig{
					target("host-1", "host01", "host"),
					target("host-2", "host02", "host"),
					target("db-1", "db01", "oracle_database"),
				},
			},
		},
		ResponseMonitor:   monitor,
		ResponseTolerance: 21 * time.Minute,
		ObservedAt:        observedAt,
	})

	hostAttrs := transform.Attributes{"scope": "targets", "site": "oraemc", "endpoint": "http://oem.example", "target_type": "host"}
	assertPoint(t, points, TargetsConfiguredMetricName, hostAttrs, 2)
	assertPoint(t, points, TargetsActiveMetricName, hostAttrs, 1)
	assertPoint(t, points, TargetsInactiveMetricName, hostAttrs, 1)

	dbAttrs := transform.Attributes{"scope": "targets", "site": "oraemc", "endpoint": "http://oem.example", "target_type": "oracle_database"}
	assertPoint(t, points, TargetsConfiguredMetricName, dbAttrs, 1)
	assertPoint(t, points, TargetsActiveMetricName, dbAttrs, 0)
	assertPoint(t, points, TargetsInactiveMetricName, dbAttrs, 1)
	assertNoTargetIdentityAttrs(t, points)
}

func TestFromSnapshotBuildsRequestCollectionAndExportMetrics(t *testing.T) {
	observedAt := time.Unix(1700000100, 0)
	points := FromSnapshot(SnapshotInput{
		OEM:       oem.Stats{RequestsTotal: 8, RequestErrorsTotal: 2},
		Collector: collect.Stats{DatapointsCollectedTotal: 13},
		Exporter: ExporterStats{
			DatapointsExportedTotal: 11,
			ExportFailuresTotal:     1,
			ExportPayloadBytes:      2048,
		},
		ObservedAt: observedAt,
	})

	attrs := transform.Attributes{"scope": "global"}
	assertPoint(t, points, OEMRequestsMetricName, attrs, 8)
	assertPoint(t, points, OEMRequestErrorsMetricName, attrs, 2)
	assertPoint(t, points, DatapointsCollectedMetricName, attrs, 13)
	assertPoint(t, points, DatapointsExportedMetricName, attrs, 11)
	assertPoint(t, points, ExportFailuresMetricName, attrs, 1)
	assertPoint(t, points, ExportPayloadBytesMetricName, attrs, 2048)
}

func TestRegistryRecordsExportStats(t *testing.T) {
	registry := NewRegistry()

	registry.RecordExportSuccess(7, 512)
	registry.RecordExportFailure(1024)
	registry.RecordExportSuccess(3, 256)

	stats := registry.SnapshotStats()
	if stats.DatapointsExportedTotal != 10 {
		t.Fatalf("DatapointsExportedTotal = %d, want 10", stats.DatapointsExportedTotal)
	}
	if stats.ExportFailuresTotal != 1 {
		t.Fatalf("ExportFailuresTotal = %d, want 1", stats.ExportFailuresTotal)
	}
	if stats.ExportPayloadBytes != 256 {
		t.Fatalf("ExportPayloadBytes = %d, want last payload size 256", stats.ExportPayloadBytes)
	}
}

func TestMetricNamesUseCollectorPrefix(t *testing.T) {
	for _, name := range MetricNames() {
		if !strings.HasPrefix(name, "oem_collector_") {
			t.Fatalf("metric name %q does not use oem_collector_ prefix", name)
		}
	}
}

func target(id, name, targetType string) config.TargetConfig {
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

func assertPoint(t *testing.T, points []transform.MetricPoint, name string, attrs transform.Attributes, value float64) {
	t.Helper()
	for _, point := range points {
		if point.Name == name && attrsEqual(point.Attributes, attrs) {
			if point.MetricName != name {
				t.Fatalf("%s MetricName = %q, want same as Name", name, point.MetricName)
			}
			if point.Value != value {
				t.Fatalf("%s value = %v, want %v", name, point.Value, value)
			}
			if point.SeriesID == "" {
				t.Fatalf("%s SeriesID is empty", name)
			}
			return
		}
	}
	t.Fatalf("point %s with attrs %#v not found in %#v", name, attrs, points)
}

func attrsEqual(got, want transform.Attributes) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			return false
		}
	}
	return true
}

func assertNoTargetIdentityAttrs(t *testing.T, points []transform.MetricPoint) {
	t.Helper()
	for _, point := range points {
		if point.Attributes["scope"] != "targets" {
			continue
		}
		if _, ok := point.Attributes["target_id"]; ok {
			t.Fatalf("target metric %s has high-cardinality target_id attr: %#v", point.Name, point.Attributes)
		}
		if _, ok := point.Attributes["target_name"]; ok {
			t.Fatalf("target metric %s has high-cardinality target_name attr: %#v", point.Name, point.Attributes)
		}
	}
}
