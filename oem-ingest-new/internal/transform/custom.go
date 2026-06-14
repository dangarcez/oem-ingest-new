package transform

import (
	"fmt"
	"strings"
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
)

const MonitorResponseMetricName = "oem_monitor_response"
const MonitorStatusMetricName = "oem_monitor_stus"

// FromResponseMonitor builds the custom oem_monitor_response gauge for every
// configured target. Value 1 means the target had a useful collection inside
// the tolerance window; value 0 means it is stale or has never collected.
func FromResponseMonitor(sites []config.SiteConfig, monitor *collect.ResponseMonitor, tolerance time.Duration, observedAt time.Time) []MetricPoint {
	points := make([]MetricPoint, 0, countTargets(sites))
	for _, site := range sites {
		for _, target := range site.Targets {
			value := float64(0)
			if monitor.Active(target.ID, observedAt, tolerance) {
				value = 1
			}
			points = append(points, MetricPoint{
				Name:       MonitorResponseMetricName,
				MetricName: MonitorResponseMetricName,
				TargetID:   target.ID,
				SeriesID:   target.ID,
				Value:      value,
				Attributes: targetTagAttributes(target.Tags),
				Timestamp:  observedAt,
			})
		}
	}
	return points
}

// FromMonitorStatus builds the legacy custom oem_monitor_stus gauge from the
// latestData result used by each target type. Status codes are kept compatible
// with the Python collector: 0 down/inactive, 1 without collection, 2 up.
func FromMonitorStatus(result collect.Result, monitor *collect.ResponseMonitor, tolerance time.Duration) (MetricPoint, bool) {
	value, ok := monitorStatusValue(result, monitor, tolerance)
	if !ok {
		return MetricPoint{}, false
	}

	target := result.Job.Target
	targetID := resultTargetID(result)
	return MetricPoint{
		Name:       MonitorStatusMetricName,
		MetricName: MonitorStatusMetricName,
		TargetID:   targetID,
		SeriesID:   targetID,
		Value:      float64(value),
		Attributes: targetTagAttributes(target.Tags),
		Timestamp:  result.CollectedAt,
	}, true
}

func monitorStatusValue(result collect.Result, monitor *collect.ResponseMonitor, tolerance time.Duration) (int, bool) {
	targetType := strings.TrimSpace(result.Job.Target.TypeName)
	if targetType == "" {
		targetType = strings.TrimSpace(result.LatestData.TargetTypeName)
	}
	groupName := resultMetricGroupName(result)
	items := result.LatestData.Items

	switch targetType {
	case "rac_database":
		if !sameMetricGroup(groupName, "Availability") {
			return 0, false
		}
		if len(items) > 0 {
			return 0, true
		}
		return monitorActiveStatus(result, monitor, tolerance), true
	case "oracle_database":
		if !sameMetricGroup(groupName, "Response") {
			return 0, false
		}
		if len(items) == 0 {
			return monitorActiveStatus(result, monitor, tolerance), true
		}
		return oracleDatabaseStatus(items[0])
	case "oracle_pdb":
		if !sameMetricGroup(groupName, "Response") {
			return 0, false
		}
		if len(items) == 0 {
			return 1, true
		}
		return oraclePDBStatus(items[0])
	case "host":
		if !sameMetricGroup(groupName, "Response") {
			return 0, false
		}
		if len(items) == 0 {
			return monitorActiveStatus(result, monitor, tolerance), true
		}
		return hostStatus(items[0])
	default:
		return 0, false
	}
}

func oracleDatabaseStatus(item map[string]any) (int, bool) {
	if value, ok := item["Status"]; ok {
		if statusIsZero(value) {
			return 0, true
		}
		return 2, true
	}
	if value, ok := item["DatabaseStatus"]; ok {
		if strings.TrimSpace(fmt.Sprint(value)) != "ACTIVE" {
			return 0, true
		}
		return 2, true
	}
	return 0, false
}

func oraclePDBStatus(item map[string]any) (int, bool) {
	hasStatus := false
	if value, ok := item["Status"]; ok {
		hasStatus = true
		if statusIsZero(value) {
			return 0, true
		}
	}
	if value, ok := item["State"]; ok {
		if strings.TrimSpace(fmt.Sprint(value)) != "OPEN" {
			return 0, true
		}
		return 2, true
	}
	if hasStatus {
		return 2, true
	}
	return 0, false
}

func hostStatus(item map[string]any) (int, bool) {
	value, ok := item["Status"]
	if !ok {
		return 0, false
	}
	if statusIsZero(value) {
		return 0, true
	}
	return 2, true
}

func monitorActiveStatus(result collect.Result, monitor *collect.ResponseMonitor, tolerance time.Duration) int {
	if monitor.Active(resultTargetID(result), result.CollectedAt, tolerance) {
		return 2
	}
	return 1
}

func statusIsZero(value any) bool {
	number, ok := coerceNumber(value)
	return ok && number == 0
}

func resultTargetID(result collect.Result) string {
	if id := strings.TrimSpace(result.Job.Target.ID); id != "" {
		return id
	}
	if id := strings.TrimSpace(result.Metadata.TargetID); id != "" {
		return id
	}
	return strings.TrimSpace(result.LatestData.TargetID)
}

func resultMetricGroupName(result collect.Result) string {
	if groupName := strings.TrimSpace(result.Metadata.MetricGroupName); groupName != "" {
		return groupName
	}
	if groupName := strings.TrimSpace(result.Job.MetricGroupName); groupName != "" {
		return groupName
	}
	return strings.TrimSpace(result.LatestData.MetricGroupName)
}

func sameMetricGroup(got, want string) bool {
	return strings.EqualFold(strings.TrimSpace(got), want)
}

func countTargets(sites []config.SiteConfig) int {
	count := 0
	for _, site := range sites {
		count += len(site.Targets)
	}
	return count
}

func targetTagAttributes(tags map[string]string) Attributes {
	attrs := make(Attributes, len(tags))
	for key, value := range tags {
		attrs[key] = value
	}
	return attrs
}
