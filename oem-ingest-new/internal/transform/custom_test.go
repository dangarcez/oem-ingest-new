package transform

import (
	"encoding/json"
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

func TestFromMonitorStatusRACDatabase(t *testing.T) {
	tests := []struct {
		name          string
		items         []map[string]any
		monitorActive bool
		want          float64
	}{
		{
			name:  "availability with data marks target inactive",
			items: []map[string]any{{"Availability_Status": "down"}},
			want:  0,
		},
		{
			name:          "empty availability uses active response monitor",
			monitorActive: true,
			want:          2,
		},
		{
			name: "empty availability without recent collection marks no collection",
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitorStatusResult("rac_database", "Availability", tt.items)
			point, ok := FromMonitorStatus(result, monitorForStatusResult(result, tt.monitorActive), 21*time.Minute)

			if !ok {
				t.Fatal("FromMonitorStatus returned ok=false, want true")
			}
			assertMonitorStatusPoint(t, point, result.Job.Target.ID, tt.want, result.CollectedAt)
		})
	}
}

func TestFromMonitorStatusOracleDatabase(t *testing.T) {
	tests := []struct {
		name          string
		items         []map[string]any
		monitorActive bool
		want          float64
	}{
		{
			name:          "empty response uses active response monitor",
			monitorActive: true,
			want:          2,
		},
		{
			name: "empty response without recent collection marks no collection",
			want: 1,
		},
		{
			name:  "status zero marks inactive",
			items: []map[string]any{{"Status": json.Number("0")}},
			want:  0,
		},
		{
			name:  "status nonzero marks up",
			items: []map[string]any{{"Status": 1}},
			want:  2,
		},
		{
			name:  "active database status marks up",
			items: []map[string]any{{"DatabaseStatus": "ACTIVE"}},
			want:  2,
		},
		{
			name:  "non active database status marks inactive",
			items: []map[string]any{{"DatabaseStatus": "MOUNTED"}},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitorStatusResult("oracle_database", "Response", tt.items)
			point, ok := FromMonitorStatus(result, monitorForStatusResult(result, tt.monitorActive), 21*time.Minute)

			if !ok {
				t.Fatal("FromMonitorStatus returned ok=false, want true")
			}
			assertMonitorStatusPoint(t, point, result.Job.Target.ID, tt.want, result.CollectedAt)
		})
	}
}

func TestFromMonitorStatusOraclePDB(t *testing.T) {
	tests := []struct {
		name          string
		items         []map[string]any
		monitorActive bool
		want          float64
	}{
		{
			name:          "empty response always marks no collection",
			monitorActive: true,
			want:          1,
		},
		{
			name:  "status zero marks inactive",
			items: []map[string]any{{"Status": 0}},
			want:  0,
		},
		{
			name:  "status nonzero marks up",
			items: []map[string]any{{"Status": json.Number("1")}},
			want:  2,
		},
		{
			name:  "open state marks up",
			items: []map[string]any{{"State": "OPEN"}},
			want:  2,
		},
		{
			name:  "non open state marks inactive",
			items: []map[string]any{{"State": "MOUNTED"}},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitorStatusResult("oracle_pdb", "Response", tt.items)
			point, ok := FromMonitorStatus(result, monitorForStatusResult(result, tt.monitorActive), 21*time.Minute)

			if !ok {
				t.Fatal("FromMonitorStatus returned ok=false, want true")
			}
			assertMonitorStatusPoint(t, point, result.Job.Target.ID, tt.want, result.CollectedAt)
		})
	}
}

func TestFromMonitorStatusHost(t *testing.T) {
	tests := []struct {
		name          string
		items         []map[string]any
		monitorActive bool
		want          float64
	}{
		{
			name:          "empty response uses active response monitor",
			monitorActive: true,
			want:          2,
		},
		{
			name: "empty response without recent collection marks no collection",
			want: 1,
		},
		{
			name:  "status zero marks inactive",
			items: []map[string]any{{"Status": json.Number("0")}},
			want:  0,
		},
		{
			name:  "status nonzero marks up",
			items: []map[string]any{{"Status": 1}},
			want:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitorStatusResult("host", "Response", tt.items)
			point, ok := FromMonitorStatus(result, monitorForStatusResult(result, tt.monitorActive), 21*time.Minute)

			if !ok {
				t.Fatal("FromMonitorStatus returned ok=false, want true")
			}
			assertMonitorStatusPoint(t, point, result.Job.Target.ID, tt.want, result.CollectedAt)
		})
	}
}

func TestFromMonitorStatusIgnoresUnsupportedTargetOrGroup(t *testing.T) {
	tests := []struct {
		name       string
		targetType string
		groupName  string
		items      []map[string]any
	}{
		{name: "unsupported target type", targetType: "oracle_listener", groupName: "Response"},
		{name: "wrong group for host", targetType: "host", groupName: "Load", items: []map[string]any{{"Status": 1}}},
		{name: "missing status field", targetType: "host", groupName: "Response", items: []map[string]any{{"Value": 1}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitorStatusResult(tt.targetType, tt.groupName, tt.items)

			if point, ok := FromMonitorStatus(result, collect.NewResponseMonitor(), 21*time.Minute); ok {
				t.Fatalf("FromMonitorStatus = %#v, true; want no point", point)
			}
		})
	}
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

func assertMonitorStatusPoint(t *testing.T, point MetricPoint, targetID string, value float64, timestamp time.Time) {
	t.Helper()
	if point.Name != MonitorStatusMetricName {
		t.Fatalf("Name = %q, want %q", point.Name, MonitorStatusMetricName)
	}
	if point.MetricName != MonitorStatusMetricName {
		t.Fatalf("MetricName = %q, want %q", point.MetricName, MonitorStatusMetricName)
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
	wantAttrs := Attributes{"target_name": point.Attributes["target_name"], "target_type": point.Attributes["target_type"]}
	if !reflect.DeepEqual(point.Attributes, wantAttrs) {
		t.Fatalf("Attributes = %#v, want target tags only %#v", point.Attributes, wantAttrs)
	}
}

func monitorStatusResult(targetType, groupName string, items []map[string]any) collect.Result {
	return transformResult(targetConfig("target-1", "target-1", targetType), collect.GroupMetadata{
		TargetID:        "target-1",
		MetricGroupName: groupName,
	}, items)
}

func monitorForStatusResult(result collect.Result, active bool) *collect.ResponseMonitor {
	monitor := collect.NewResponseMonitor()
	if active {
		monitor.Mark(result.Job.Target.ID, result.CollectedAt.Add(-5*time.Minute))
	}
	return monitor
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
