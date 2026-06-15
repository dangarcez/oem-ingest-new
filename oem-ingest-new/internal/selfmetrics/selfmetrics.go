package selfmetrics

import (
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/transform"
)

const (
	TargetsConfiguredMetricName   = "oem_collector_targets_configured"
	TargetsActiveMetricName       = "oem_collector_targets_active"
	TargetsInactiveMetricName     = "oem_collector_targets_inactive"
	OEMRequestsMetricName         = "oem_collector_oem_requests_total"
	OEMRequestErrorsMetricName    = "oem_collector_oem_request_errors_total"
	DatapointsCollectedMetricName = "oem_collector_datapoints_collected_total"
	DatapointsExportedMetricName  = "oem_collector_datapoints_exported_total"
	LogsExportedMetricName        = "oem_collector_logs_exported_total"
	ExportFailuresMetricName      = "oem_collector_export_failures_total"
	ExportPayloadBytesMetricName  = "oem_collector_export_payload_bytes"
	ExportDurationMetricName      = "oem_collector_export_duration_seconds"
)

var metricNames = []string{
	TargetsConfiguredMetricName,
	TargetsActiveMetricName,
	TargetsInactiveMetricName,
	OEMRequestsMetricName,
	OEMRequestErrorsMetricName,
	DatapointsCollectedMetricName,
	DatapointsExportedMetricName,
	LogsExportedMetricName,
	ExportFailuresMetricName,
	ExportPayloadBytesMetricName,
	ExportDurationMetricName,
}

// MetricNames lists the internal metrics emitted by this package.
func MetricNames() []string {
	out := make([]string, len(metricNames))
	copy(out, metricNames)
	return out
}

// ExporterStats is the exporter's contribution to internal metrics.
type ExporterStats struct {
	DatapointsExportedTotal uint64
	LogsExportedTotal       uint64
	ExportFailuresTotal     uint64
	ExportPayloadBytes      uint64
	ExportDurationSeconds   float64
}

// Registry keeps exporter counters that do not yet live in a concrete exporter.
type Registry struct {
	datapointsExported  uint64
	logsExported        uint64
	exportFailures      uint64
	exportPayloadBytes  uint64
	exportDurationNanos uint64
}

// NewRegistry creates an empty internal metrics registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// RecordExportSuccess records a successful export batch.
func (r *Registry) RecordExportSuccess(datapointsExported, payloadBytes uint64) {
	r.RecordMetricsExportSuccess(datapointsExported, payloadBytes, 0)
}

// RecordMetricsExportSuccess records a successful metrics export batch.
func (r *Registry) RecordMetricsExportSuccess(datapointsExported, payloadBytes uint64, duration time.Duration) {
	if r == nil {
		return
	}
	atomic.AddUint64(&r.datapointsExported, datapointsExported)
	r.recordPayloadAndDuration(payloadBytes, duration)
}

// RecordExportFailure records a failed export attempt and the payload size that
// was attempted, when known.
func (r *Registry) RecordExportFailure(payloadBytes uint64) {
	r.RecordMetricsExportFailure(payloadBytes, 0)
}

// RecordMetricsExportFailure records a failed metrics export attempt.
func (r *Registry) RecordMetricsExportFailure(payloadBytes uint64, duration time.Duration) {
	if r == nil {
		return
	}
	atomic.AddUint64(&r.exportFailures, 1)
	r.recordPayloadAndDuration(payloadBytes, duration)
}

// RecordLogsExportSuccess records a successful log export batch.
func (r *Registry) RecordLogsExportSuccess(logsExported, payloadBytes uint64, duration time.Duration) {
	if r == nil {
		return
	}
	atomic.AddUint64(&r.logsExported, logsExported)
	r.recordPayloadAndDuration(payloadBytes, duration)
}

// RecordLogsExportFailure records a failed log export attempt.
func (r *Registry) RecordLogsExportFailure(payloadBytes uint64, duration time.Duration) {
	if r == nil {
		return
	}
	atomic.AddUint64(&r.exportFailures, 1)
	r.recordPayloadAndDuration(payloadBytes, duration)
}

func (r *Registry) recordPayloadAndDuration(payloadBytes uint64, duration time.Duration) {
	atomic.StoreUint64(&r.exportPayloadBytes, payloadBytes)
	if duration < 0 {
		duration = 0
	}
	atomic.StoreUint64(&r.exportDurationNanos, uint64(duration))
}

// SnapshotStats returns a consistent snapshot of exporter counters.
func (r *Registry) SnapshotStats() ExporterStats {
	if r == nil {
		return ExporterStats{}
	}
	return ExporterStats{
		DatapointsExportedTotal: atomic.LoadUint64(&r.datapointsExported),
		LogsExportedTotal:       atomic.LoadUint64(&r.logsExported),
		ExportFailuresTotal:     atomic.LoadUint64(&r.exportFailures),
		ExportPayloadBytes:      atomic.LoadUint64(&r.exportPayloadBytes),
		ExportDurationSeconds:   float64(atomic.LoadUint64(&r.exportDurationNanos)) / float64(time.Second),
	}
}

// SnapshotInput contains the process snapshots used to build internal metrics.
type SnapshotInput struct {
	Sites             []config.SiteConfig
	ResponseMonitor   *collect.ResponseMonitor
	ResponseTolerance time.Duration
	OEM               oem.Stats
	Collector         collect.Stats
	Exporter          ExporterStats
	ObservedAt        time.Time
}

// FromSnapshot builds the oem_collector_* gauges. Target counts are aggregated
// by site and target type to keep cardinality stable.
func FromSnapshot(input SnapshotInput) []transform.MetricPoint {
	observedAt := input.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	points := make([]transform.MetricPoint, 0, len(input.Sites)*3+8)
	for _, group := range targetGroups(input.Sites, input.ResponseMonitor, input.ResponseTolerance, observedAt) {
		attrs := transform.Attributes{
			"scope":       "targets",
			"site":        group.siteName,
			"endpoint":    group.endpoint,
			"target_type": group.targetType,
		}
		points = append(points,
			metricPoint(TargetsConfiguredMetricName, float64(group.configured), attrs, observedAt),
			metricPoint(TargetsActiveMetricName, float64(group.active), attrs, observedAt),
			metricPoint(TargetsInactiveMetricName, float64(group.configured-group.active), attrs, observedAt),
		)
	}

	globalAttrs := transform.Attributes{"scope": "global"}
	points = append(points,
		metricPoint(OEMRequestsMetricName, float64(input.OEM.RequestsTotal), globalAttrs, observedAt),
		metricPoint(OEMRequestErrorsMetricName, float64(input.OEM.RequestErrorsTotal), globalAttrs, observedAt),
		metricPoint(DatapointsCollectedMetricName, float64(input.Collector.DatapointsCollectedTotal), globalAttrs, observedAt),
		metricPoint(DatapointsExportedMetricName, float64(input.Exporter.DatapointsExportedTotal), globalAttrs, observedAt),
		metricPoint(LogsExportedMetricName, float64(input.Exporter.LogsExportedTotal), globalAttrs, observedAt),
		metricPoint(ExportFailuresMetricName, float64(input.Exporter.ExportFailuresTotal), globalAttrs, observedAt),
		metricPoint(ExportPayloadBytesMetricName, float64(input.Exporter.ExportPayloadBytes), globalAttrs, observedAt),
		metricPoint(ExportDurationMetricName, input.Exporter.ExportDurationSeconds, globalAttrs, observedAt),
	)

	return points
}

type targetGroup struct {
	siteName   string
	endpoint   string
	targetType string
	configured int
	active     int
}

func targetGroups(sites []config.SiteConfig, monitor *collect.ResponseMonitor, tolerance time.Duration, observedAt time.Time) []targetGroup {
	groupsByKey := make(map[string]*targetGroup)
	for siteIndex, site := range sites {
		siteName := site.Name
		if siteName == "" {
			siteName = site.Site
		}
		if siteName == "" {
			siteName = "site_" + strconv.Itoa(siteIndex)
		}
		for _, target := range site.Targets {
			targetType := target.TypeName
			if targetType == "" {
				targetType = "unknown"
			}
			key := siteName + "\x00" + site.Endpoint + "\x00" + targetType
			group := groupsByKey[key]
			if group == nil {
				group = &targetGroup{siteName: siteName, endpoint: site.Endpoint, targetType: targetType}
				groupsByKey[key] = group
			}
			group.configured++
			if monitor.Active(target.ID, observedAt, tolerance) {
				group.active++
			}
		}
	}

	groups := make([]targetGroup, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].siteName != groups[j].siteName {
			return groups[i].siteName < groups[j].siteName
		}
		if groups[i].endpoint != groups[j].endpoint {
			return groups[i].endpoint < groups[j].endpoint
		}
		return groups[i].targetType < groups[j].targetType
	})
	return groups
}

func metricPoint(name string, value float64, attrs transform.Attributes, observedAt time.Time) transform.MetricPoint {
	return transform.MetricPoint{
		Name:       name,
		MetricName: name,
		SeriesID:   seriesID(name, attrs),
		Value:      value,
		Attributes: attrs.Clone(),
		Timestamp:  observedAt,
	}
}

func seriesID(name string, attrs transform.Attributes) string {
	if attrs["scope"] == "targets" {
		return name + "\x00" + stringAttr(attrs, "site") + "\x00" + stringAttr(attrs, "endpoint") + "\x00" + stringAttr(attrs, "target_type")
	}
	return name + "\x00global"
}

func stringAttr(attrs transform.Attributes, key string) string {
	value, _ := attrs[key].(string)
	return value
}
