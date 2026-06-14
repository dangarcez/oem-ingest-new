package selfmetrics

import (
	"reflect"
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

func TestFromSnapshotAggregatesTargetsDeterministically(t *testing.T) {
	observedAt := time.Unix(1700000200, 0)
	monitor := collect.NewResponseMonitor()
	monitor.Mark("host-1", observedAt.Add(-time.Minute))
	monitor.Mark("rac-1", observedAt.Add(-time.Minute))

	points := FromSnapshot(SnapshotInput{
		Sites: []config.SiteConfig{
			{
				Site:     "legacy-site",
				Endpoint: "http://b.example",
				Targets: []config.TargetConfig{
					target("host-2", "host02", "host"),
					target("host-1", "host01", "host"),
				},
			},
			{
				Endpoint: "http://a.example",
				Targets: []config.TargetConfig{
					target("rac-1", "rac01", "rac_database"),
				},
			},
		},
		ResponseMonitor:   monitor,
		ResponseTolerance: 21 * time.Minute,
		ObservedAt:        observedAt,
	})

	wantSeries := []string{
		TargetsConfiguredMetricName + "\x00legacy-site\x00http://b.example\x00host",
		TargetsActiveMetricName + "\x00legacy-site\x00http://b.example\x00host",
		TargetsInactiveMetricName + "\x00legacy-site\x00http://b.example\x00host",
		TargetsConfiguredMetricName + "\x00site_1\x00http://a.example\x00rac_database",
		TargetsActiveMetricName + "\x00site_1\x00http://a.example\x00rac_database",
		TargetsInactiveMetricName + "\x00site_1\x00http://a.example\x00rac_database",
	}
	if len(points) != len(wantSeries)+6 {
		t.Fatalf("points len = %d, want %d", len(points), len(wantSeries)+6)
	}
	for i, want := range wantSeries {
		if points[i].SeriesID != want {
			t.Fatalf("target point %d SeriesID = %q, want %q", i, points[i].SeriesID, want)
		}
	}

	assertPoint(t, points, TargetsConfiguredMetricName, transform.Attributes{"scope": "targets", "site": "legacy-site", "endpoint": "http://b.example", "target_type": "host"}, 2)
	assertPoint(t, points, TargetsActiveMetricName, transform.Attributes{"scope": "targets", "site": "legacy-site", "endpoint": "http://b.example", "target_type": "host"}, 1)
	assertPoint(t, points, TargetsInactiveMetricName, transform.Attributes{"scope": "targets", "site": "site_1", "endpoint": "http://a.example", "target_type": "rac_database"}, 0)
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

func TestMetricNamesListsRequiredMetricsAndReturnsCopy(t *testing.T) {
	want := []string{
		TargetsConfiguredMetricName,
		TargetsActiveMetricName,
		TargetsInactiveMetricName,
		OEMRequestsMetricName,
		OEMRequestErrorsMetricName,
		DatapointsCollectedMetricName,
		DatapointsExportedMetricName,
		ExportFailuresMetricName,
		ExportPayloadBytesMetricName,
	}
	got := MetricNames()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MetricNames() = %#v, want %#v", got, want)
	}

	got[0] = "mutated"
	if MetricNames()[0] != TargetsConfiguredMetricName {
		t.Fatalf("MetricNames should return a defensive copy")
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
