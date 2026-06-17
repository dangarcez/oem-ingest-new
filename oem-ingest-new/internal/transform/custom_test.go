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
			name:  "status has priority over non open state",
			items: []map[string]any{{"Status": 1, "State": "MOUNTED"}},
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

func TestFromServiceStatusUsesDBTimeDelta(t *testing.T) {
	tests := []struct {
		name string
		item map[string]any
		want float64
		body string
	}{
		{
			name: "positive DBTime_delta marks active service",
			item: map[string]any{"service_name": "pix_rw", "instance": "pdb1", "DBTime_delta": json.Number("0.25")},
			want: 1,
			body: "ativo",
		},
		{
			name: "zero DBTime_delta marks inactive service",
			item: map[string]any{"service_name": "pix_ro", "instance": "pdb2", "DBTime_delta": 0},
			want: 0,
			body: "inativo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serviceStatusResult("oracle_pdb", "DBService", []string{"service_name", "instance"}, []map[string]any{tt.item})

			out := FromServiceStatus(result)

			wantSeriesID := result.Job.Target.ID + "\x00" + tt.item["service_name"].(string) + "\x00" + tt.item["instance"].(string)
			assertServiceStatusOutput(t, out, result, wantSeriesID, tt.want, tt.body)
			assertAttrsContain(t, out.Metrics[0].Attributes, Attributes{"name_": tt.item["service_name"], "_instance": tt.item["instance"]})
		})
	}
}

func TestFromServiceStatusUsesStatusField(t *testing.T) {
	tests := []struct {
		name string
		item map[string]any
		want float64
		body string
	}{
		{
			name: "Up status marks active service",
			item: map[string]any{"name": "srv_rw", "dbname": "rac1", "status": "Up"},
			want: 1,
			body: "ativo",
		},
		{
			name: "non Up status marks inactive service",
			item: map[string]any{"name": "srv_ro", "dbname": "rac1", "status": "Down"},
			want: 0,
			body: "inativo",
		},
		{
			name: "status keeps legacy precedence over DBTime_delta",
			item: map[string]any{"name": "srv_batch", "dbname": "rac1", "DBTime_delta": 10, "status": "Down"},
			want: 0,
			body: "inativo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serviceStatusResult("rac_database", "service_performance", []string{"name", "dbname"}, []map[string]any{tt.item})

			out := FromServiceStatus(result)

			wantSeriesID := result.Job.Target.ID + "\x00" + tt.item["name"].(string) + "\x00" + tt.item["dbname"].(string)
			assertServiceStatusOutput(t, out, result, wantSeriesID, tt.want, tt.body)
			assertAttrsContain(t, out.Metrics[0].Attributes, Attributes{"name_": tt.item["name"], "dbname": tt.item["dbname"]})
		})
	}
}

func TestFromServiceStatusInfersLegacyKeysWhenMetadataIsEmpty(t *testing.T) {
	tests := []struct {
		name         string
		targetType   string
		groupName    string
		item         map[string]any
		wantSeriesID string
		wantAttrs    Attributes
	}{
		{
			name:         "rac_database service_performance",
			targetType:   "rac_database",
			groupName:    "service_performance",
			item:         map[string]any{"name": "SYS$USERS", "dbname": "rac1", "status": "Up"},
			wantSeriesID: "target-1\x00SYS$USERS\x00rac1",
			wantAttrs:    Attributes{"name_": "SYS$USERS", "dbname": "rac1"},
		},
		{
			name:         "oracle_pdb DBService",
			targetType:   "oracle_pdb",
			groupName:    "DBService",
			item:         map[string]any{"service_name": "pdb_rw", "instance": "pdb1", "DBTime_delta": 1},
			wantSeriesID: "target-1\x00pdb_rw\x00pdb1",
			wantAttrs:    Attributes{"name_": "pdb_rw", "_instance": "pdb1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serviceStatusResult(tt.targetType, tt.groupName, nil, []map[string]any{tt.item})

			out := FromServiceStatus(result)

			assertServiceStatusOutput(t, out, result, tt.wantSeriesID, 1, "ativo")
			assertAttrsContain(t, out.Metrics[0].Attributes, tt.wantAttrs)
		})
	}
}

func TestFromServiceStatusIgnoresUnsupportedInput(t *testing.T) {
	tests := []struct {
		name       string
		targetType string
		groupName  string
		items      []map[string]any
	}{
		{name: "unsupported target type", targetType: "oracle_database", groupName: "DBService", items: []map[string]any{{"status": "Up"}}},
		{name: "wrong group for rac_database", targetType: "rac_database", groupName: "Availability", items: []map[string]any{{"status": "Up"}}},
		{name: "missing status source", targetType: "oracle_pdb", groupName: "DBService", items: []map[string]any{{"service": "pdb_rw"}}},
		{name: "invalid DBTime_delta without status", targetType: "rac_database", groupName: "service_performance", items: []map[string]any{{"DBTime_delta": "not-a-number"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serviceStatusResult(tt.targetType, tt.groupName, nil, tt.items)

			out := FromServiceStatus(result)

			if len(out.Metrics) != 0 || len(out.Logs) != 0 {
				t.Fatalf("FromServiceStatus = %#v, want empty output", out)
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

func assertServiceStatusOutput(t *testing.T, out Output, result collect.Result, wantSeriesID string, wantValue float64, wantBody string) {
	t.Helper()
	if len(out.Metrics) != 1 {
		t.Fatalf("metrics = %#v, want one service status metric", out.Metrics)
	}
	if len(out.Logs) != 1 {
		t.Fatalf("logs = %#v, want one string service status log", out.Logs)
	}

	metric := out.Metrics[0]
	if metric.Name != ServiceStatusMetricName || metric.MetricName != ServiceStatusMetricName {
		t.Fatalf("metric names = %q/%q, want %q", metric.Name, metric.MetricName, ServiceStatusMetricName)
	}
	if metric.MetricGroupName != result.Metadata.MetricGroupName {
		t.Fatalf("metric group = %q, want %q", metric.MetricGroupName, result.Metadata.MetricGroupName)
	}
	if metric.TargetID != result.Job.Target.ID || metric.SeriesID != wantSeriesID {
		t.Fatalf("metric identity = %q/%q, want %q/%q", metric.TargetID, metric.SeriesID, result.Job.Target.ID, wantSeriesID)
	}
	if metric.Value != wantValue {
		t.Fatalf("metric value = %v, want %v", metric.Value, wantValue)
	}
	if !metric.Timestamp.Equal(result.CollectedAt) {
		t.Fatalf("metric timestamp = %s, want %s", metric.Timestamp, result.CollectedAt)
	}
	if metric.Attributes["target_name"] != result.Job.Target.Name || metric.Attributes["target_type"] != result.Job.Target.TypeName {
		t.Fatalf("metric attributes missing target tags: %#v", metric.Attributes)
	}
	if _, ok := metric.Attributes["DBTime_delta"]; ok {
		t.Fatalf("metric attributes should not include calculation field DBTime_delta: %#v", metric.Attributes)
	}
	if _, ok := metric.Attributes["status"]; ok {
		t.Fatalf("metric attributes should not include calculation field status: %#v", metric.Attributes)
	}

	log := out.Logs[0]
	if log.MetricName != StringServiceStatusMetricName {
		t.Fatalf("log metric name = %q, want %q", log.MetricName, StringServiceStatusMetricName)
	}
	if log.TargetID != result.Job.Target.ID || log.SeriesID != wantSeriesID {
		t.Fatalf("log identity = %q/%q, want %q/%q", log.TargetID, log.SeriesID, result.Job.Target.ID, wantSeriesID)
	}
	if log.Body != wantBody {
		t.Fatalf("log body = %q, want %q", log.Body, wantBody)
	}
	if log.Attributes["metric"] != StringServiceStatusMetricName {
		t.Fatalf("log metric attribute = %#v, want %q", log.Attributes["metric"], StringServiceStatusMetricName)
	}
	if !log.Continuous {
		t.Fatal("string service status log should be continuous")
	}
	if !log.Timestamp.Equal(result.CollectedAt) {
		t.Fatalf("log timestamp = %s, want %s", log.Timestamp, result.CollectedAt)
	}
	if !reflect.DeepEqual(metric.Attributes, attrsWithoutMetric(log.Attributes)) {
		t.Fatalf("metric/log attributes diverged: metric=%#v log=%#v", metric.Attributes, log.Attributes)
	}
}

func assertAttrsContain(t *testing.T, got Attributes, want Attributes) {
	t.Helper()
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Fatalf("attribute %q = %#v, want %#v; all attrs=%#v", key, got[key], value, got)
		}
	}
}

func attrsWithoutMetric(attrs Attributes) Attributes {
	out := attrs.Clone()
	delete(out, "metric")
	return out
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

func serviceStatusResult(targetType, groupName string, keys []string, items []map[string]any) collect.Result {
	return transformResult(targetConfig("target-1", "target-1", targetType), collect.GroupMetadata{
		TargetID:        "target-1",
		MetricGroupName: groupName,
		Keys:            keys,
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
