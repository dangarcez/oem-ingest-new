package transform

import (
	"reflect"
	"testing"
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
)

func TestFromResponseMonitorMarksNeverCollectedTargetInactive(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	monitor := collect.NewResponseMonitor()
	sites := monitorResponseSites()

	points := FromResponseMonitor(sites, monitor, 21*time.Minute, observedAt)

	if len(points) != 1 {
		t.Fatalf("points len = %d, want 1", len(points))
	}
	assertMonitorResponsePoint(t, points[0], "target-1", 0, observedAt, Attributes{"target_name": "host01", "target_type": "host", "sistema": "pix"})
}

func TestFromResponseMonitorMarksTargetInsideToleranceActive(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	monitor := collect.NewResponseMonitor()
	monitor.Mark("target-1", observedAt.Add(-20*time.Minute))

	points := FromResponseMonitor(monitorResponseSites(), monitor, 21*time.Minute, observedAt)

	assertMonitorResponsePoint(t, points[0], "target-1", 1, observedAt, Attributes{"target_name": "host01", "target_type": "host", "sistema": "pix"})
}

func TestFromResponseMonitorMarksTargetOutsideToleranceInactive(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	monitor := collect.NewResponseMonitor()
	monitor.Mark("target-1", observedAt.Add(-22*time.Minute))

	points := FromResponseMonitor(monitorResponseSites(), monitor, 21*time.Minute, observedAt)

	assertMonitorResponsePoint(t, points[0], "target-1", 0, observedAt, Attributes{"target_name": "host01", "target_type": "host", "sistema": "pix"})
}

func TestFromResponseMonitorPreservesLegacyStrictToleranceBoundary(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	monitor := collect.NewResponseMonitor()
	monitor.Mark("target-1", observedAt.Add(-21*time.Minute))

	points := FromResponseMonitor(monitorResponseSites(), monitor, 21*time.Minute, observedAt)

	assertMonitorResponsePoint(t, points[0], "target-1", 0, observedAt, Attributes{"target_name": "host01", "target_type": "host", "sistema": "pix"})
}

func TestFromResponseMonitorCreatesPointForEachConfiguredTarget(t *testing.T) {
	observedAt := time.Unix(1700000000, 0)
	monitor := collect.NewResponseMonitor()
	monitor.Mark("target-2", observedAt.Add(-5*time.Minute))
	sites := []config.SiteConfig{
		{
			Name: "site-a",
			Targets: []config.TargetConfig{
				{ID: "target-1", Name: "host01", TypeName: "host", Tags: map[string]string{"target_name": "host01", "target_type": "host"}},
				{ID: "target-2", Name: "db01", TypeName: "oracle_database", Tags: map[string]string{"target_name": "db01", "target_type": "oracle_database"}},
			},
		},
	}

	points := FromResponseMonitor(sites, monitor, 21*time.Minute, observedAt)

	if len(points) != 2 {
		t.Fatalf("points len = %d, want 2", len(points))
	}
	assertMonitorResponsePoint(t, points[0], "target-1", 0, observedAt, Attributes{"target_name": "host01", "target_type": "host"})
	assertMonitorResponsePoint(t, points[1], "target-2", 1, observedAt, Attributes{"target_name": "db01", "target_type": "oracle_database"})
}

func assertMonitorResponsePoint(t *testing.T, point MetricPoint, targetID string, value float64, timestamp time.Time, wantAttrs Attributes) {
	t.Helper()
	if point.Name != MonitorResponseMetricName {
		t.Fatalf("Name = %q, want %q", point.Name, MonitorResponseMetricName)
	}
	if point.MetricName != MonitorResponseMetricName {
		t.Fatalf("MetricName = %q, want %q", point.MetricName, MonitorResponseMetricName)
	}
	if point.TargetID != targetID || point.SeriesID != targetID {
		t.Fatalf("target identity = %q/%q, want %q", point.TargetID, point.SeriesID, targetID)
	}
	if point.Value != value {
		t.Fatalf("Value = %v, want %v", point.Value, value)
	}
	if !point.Timestamp.Equal(timestamp) {
		t.Fatalf("Timestamp = %s, want %s", point.Timestamp, timestamp)
	}
	if !reflect.DeepEqual(point.Attributes, wantAttrs) {
		t.Fatalf("Attributes = %#v, want %#v", point.Attributes, wantAttrs)
	}
}

func monitorResponseSites() []config.SiteConfig {
	return []config.SiteConfig{
		{
			Name: "site-a",
			Targets: []config.TargetConfig{
				{
					ID:       "target-1",
					Name:     "host01",
					TypeName: "host",
					Tags: map[string]string{
						"target_name": "host01",
						"target_type": "host",
						"sistema":     "pix",
					},
				},
			},
		},
	}
}
