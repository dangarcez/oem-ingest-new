package transform

import (
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
)

const MonitorResponseMetricName = "oem_monitor_response"

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
