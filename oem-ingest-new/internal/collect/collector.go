package collect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/scheduler"
)

// LatestDataClient is the OEM client method required by Collector.
type LatestDataClient interface {
	LatestData(ctx context.Context, targetID, groupName string) (oem.LatestData, error)
}

// Client is the OEM client surface required for regular collection.
type Client interface {
	MetricGroupClient
	LatestDataClient
}

// TargetIDRepairer can refresh one job's target ID after a metric endpoint
// returns 404. It is implemented by the application wiring so collect stays
// independent from config files and report artifacts.
type TargetIDRepairer interface {
	RepairTargetID(ctx context.Context, req TargetIDRepairRequest) (TargetIDRepairResult, error)
}

// TargetIDRepairRequest describes the 404 that triggered a runtime ID check.
type TargetIDRepairRequest struct {
	Job       scheduler.Job
	Trigger   string
	Stage     string
	Err       error
	CheckedAt time.Time
}

// TargetIDRepairResult returns the corrected job when a new ID was found.
type TargetIDRepairResult struct {
	Job       scheduler.Job
	Corrected bool
}

const (
	TargetIDRepairTriggerMetric404 = "metric_404"
	TargetIDRepairStageMetadata    = "metadata"
	TargetIDRepairStageLatestData  = "latestData"
)

// Collector fetches metadata and latestData for scheduler jobs.
type Collector struct {
	latestClient LatestDataClient
	metadata     *MetadataCache
	logger       Logger
	clock        func() time.Time
	responses    *ResponseMonitor
	idRepairer   TargetIDRepairer
	stats        collectorStats
}

// CollectorOptions configures Collector.
type CollectorOptions struct {
	MetadataCache   *MetadataCache
	Logger          Logger
	Clock           func() time.Time
	ResponseMonitor *ResponseMonitor
	IDRepairer      TargetIDRepairer
}

type collectorStats struct {
	datapointsCollected uint64
	collectionErrors    uint64
	unavailableGroups   uint64
}

// Stats is a snapshot of collector counters meant to feed self metrics later.
type Stats struct {
	DatapointsCollectedTotal uint64
	CollectionErrorsTotal    uint64
	UnavailableGroupsTotal   uint64
}

// Result is one successful latestData collection result.
type Result struct {
	Job         scheduler.Job
	Metadata    GroupMetadata
	LatestData  oem.LatestData
	CollectedAt time.Time
}

// Datapoints returns the number of metric values collected in this result.
// OEM items can include key columns; those identify the series and are not
// counted as datapoints.
func (r Result) Datapoints() int {
	if len(r.LatestData.Items) == 0 {
		return 0
	}
	keys := make(map[string]struct{}, len(r.Metadata.Keys))
	for _, key := range r.Metadata.Keys {
		keys[key] = struct{}{}
	}

	count := 0
	for _, item := range r.LatestData.Items {
		for name := range item {
			if _, isKey := keys[name]; isKey {
				continue
			}
			count++
		}
	}
	return count
}

// LatestDataUnavailableError describes a target/group pair whose latestData
// endpoint is not available. It matches ErrMetricGroupUnavailable with errors.Is.
type LatestDataUnavailableError struct {
	TargetID        string
	TargetName      string
	MetricGroupName string
	StatusCode      int
	Err             error
}

func (e *LatestDataUnavailableError) Error() string {
	target := e.TargetID
	if e.TargetName != "" {
		target = e.TargetName
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("latestData do grupo de metrica %q indisponivel para target %q: HTTP %d", e.MetricGroupName, target, e.StatusCode)
	}
	return fmt.Sprintf("latestData do grupo de metrica %q indisponivel para target %q", e.MetricGroupName, target)
}

func (e *LatestDataUnavailableError) Unwrap() error {
	return e.Err
}

func (e *LatestDataUnavailableError) Is(target error) bool {
	return target == ErrMetricGroupUnavailable
}

// NewCollector creates a latestData collector. The same client is used for
// metadata and latestData unless a MetadataCache is injected for tests.
func NewCollector(client Client, opts CollectorOptions) *Collector {
	metadata := opts.MetadataCache
	if metadata == nil {
		metadata = NewMetadataCache(client, MetadataCacheOptions{Logger: opts.Logger})
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	responses := opts.ResponseMonitor
	if responses == nil {
		responses = NewResponseMonitor()
	}
	return &Collector{
		latestClient: client,
		metadata:     metadata,
		logger:       opts.Logger,
		clock:        clock,
		responses:    responses,
		idRepairer:   opts.IDRepairer,
	}
}

// Collect fetches latestData for one scheduler job. A useful collection is a
// successful response with at least one item; only those update target response
// monitoring.
func (c *Collector) Collect(ctx context.Context, job scheduler.Job) (Result, error) {
	return c.collect(ctx, job, true)
}

func (c *Collector) collect(ctx context.Context, job scheduler.Job, allowRepair bool) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if c == nil || c.latestClient == nil || c.metadata == nil {
		return Result{}, errors.New("collect: Collector sem cliente OEM")
	}

	metadata, err := c.metadata.Get(ctx, MetadataRequest{
		TargetID:        job.Target.ID,
		TargetName:      job.Target.Name,
		MetricGroupName: job.MetricGroupName,
		Bodyless:        job.MetricGroup.Bodyless,
	})
	if err != nil {
		if allowRepair && isHTTPStatus(err, http.StatusNotFound) {
			if repairedJob, ok := c.repairJobAfterNotFound(ctx, job, TargetIDRepairStageMetadata, err); ok {
				return c.collect(ctx, repairedJob, false)
			}
		}
		if errors.Is(err, ErrMetricGroupUnavailable) {
			c.recordUnavailableGroup()
		} else {
			c.recordCollectionError()
			c.warn(ctx, "falha transitoria ao consultar metadata de grupo de metrica", job, err)
		}
		return Result{}, err
	}
	job = jobWithCollectionIdentity(job, metadata.TargetID, metadata.MetricGroupName)

	latest, err := c.latestClient.LatestData(ctx, job.Target.ID, job.MetricGroupName)
	if err != nil {
		if allowRepair && isHTTPStatus(err, http.StatusNotFound) {
			if repairedJob, ok := c.repairJobAfterNotFound(ctx, job, TargetIDRepairStageLatestData, err); ok {
				return c.collect(ctx, repairedJob, false)
			}
		}
		if metadata.Bodyless && isHTTPError(err) {
			latest = oem.LatestData{
				MetricGroupName: job.MetricGroupName,
				TargetID:        job.Target.ID,
				Items:           nil,
			}
		} else {
			if isHTTPStatus(err, http.StatusNotFound) {
				unavailable := latestDataUnavailableError(job, err)
				c.recordUnavailableGroup()
				c.warn(ctx, "latestData de grupo de metrica indisponivel; job de coleta sera ignorado", job, unavailable)
				return Result{}, unavailable
			}
			c.recordCollectionError()
			c.warn(ctx, "falha transitoria ao coletar latestData", job, err)
			return Result{}, fmt.Errorf("coletar latestData target %q grupo %q: %w", job.Target.Name, job.MetricGroupName, err)
		}
	}

	collectedAt := c.now()
	result := Result{
		Job:         job,
		Metadata:    metadata,
		LatestData:  latest,
		CollectedAt: collectedAt,
	}
	if result.Datapoints() > 0 {
		c.responses.Mark(job.Target.ID, collectedAt)
		atomic.AddUint64(&c.stats.datapointsCollected, uint64(result.Datapoints()))
	}
	return result, nil
}

func (c *Collector) repairJobAfterNotFound(ctx context.Context, job scheduler.Job, stage string, cause error) (scheduler.Job, bool) {
	if c == nil || c.idRepairer == nil {
		return scheduler.Job{}, false
	}
	result, err := c.idRepairer.RepairTargetID(ctx, TargetIDRepairRequest{
		Job:       job,
		Trigger:   TargetIDRepairTriggerMetric404,
		Stage:     stage,
		Err:       cause,
		CheckedAt: c.now(),
	})
	if err != nil {
		c.warn(ctx, "falha ao revalidar ID do target apos 404", job, err)
		return scheduler.Job{}, false
	}
	if !result.Corrected || result.Job.Target.ID == "" {
		return scheduler.Job{}, false
	}
	return result.Job, true
}

func isHTTPError(err error) bool {
	var httpErr *oem.HTTPError
	return errors.As(err, &httpErr)
}

func jobWithCollectionIdentity(job scheduler.Job, targetID, groupName string) scheduler.Job {
	if targetID != "" {
		job.Target.ID = targetID
	}
	if groupName != "" {
		job.MetricGroupName = groupName
		job.MetricGroup.MetricGroupName = groupName
	}
	return job
}

// SnapshotStats returns collector counters accumulated since process start.
func (c *Collector) SnapshotStats() Stats {
	if c == nil {
		return Stats{}
	}
	return Stats{
		DatapointsCollectedTotal: atomic.LoadUint64(&c.stats.datapointsCollected),
		CollectionErrorsTotal:    atomic.LoadUint64(&c.stats.collectionErrors),
		UnavailableGroupsTotal:   atomic.LoadUint64(&c.stats.unavailableGroups),
	}
}

// ResponseMonitor tracks the last useful collection time per target.
type ResponseMonitor struct {
	mu   sync.RWMutex
	last map[string]time.Time
}

// NewResponseMonitor creates an empty response monitor.
func NewResponseMonitor() *ResponseMonitor {
	return &ResponseMonitor{last: make(map[string]time.Time)}
}

// Mark records a useful collection for targetID.
func (m *ResponseMonitor) Mark(targetID string, at time.Time) {
	if m == nil || targetID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		m.last = make(map[string]time.Time)
	}
	m.last[targetID] = at
}

// Last returns the last useful collection time for targetID.
func (m *ResponseMonitor) Last(targetID string) (time.Time, bool) {
	if m == nil || targetID == "" {
		return time.Time{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	at, ok := m.last[targetID]
	return at, ok
}

// Active reports whether targetID had a useful collection strictly inside the
// configured tolerance window. The strict comparison preserves the legacy
// Python collector behavior for oem_monitor_response.
func (m *ResponseMonitor) Active(targetID string, now time.Time, tolerance time.Duration) bool {
	if m == nil || targetID == "" || tolerance <= 0 {
		return false
	}
	at, ok := m.Last(targetID)
	if !ok {
		return false
	}
	return now.Sub(at) < tolerance
}

// Snapshot returns a copy of all target response timestamps.
func (m *ResponseMonitor) Snapshot() map[string]time.Time {
	if m == nil {
		return map[string]time.Time{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]time.Time, len(m.last))
	for targetID, at := range m.last {
		out[targetID] = at
	}
	return out
}

func (c *Collector) now() time.Time {
	if c.clock == nil {
		return time.Now()
	}
	return c.clock()
}

func (c *Collector) recordCollectionError() {
	atomic.AddUint64(&c.stats.collectionErrors, 1)
}

func (c *Collector) recordUnavailableGroup() {
	atomic.AddUint64(&c.stats.unavailableGroups, 1)
}

func (c *Collector) warn(ctx context.Context, msg string, job scheduler.Job, err error) {
	if c.logger == nil {
		return
	}
	attrs := []any{
		"job_id", job.ID,
		"endpoint", job.Endpoint,
		"target_id", job.Target.ID,
		"target_name", job.Target.Name,
		"target_type", job.Target.TypeName,
		"metric_group", job.MetricGroupName,
	}
	if err != nil {
		attrs = append(attrs, "err", err)
		var httpErr *oem.HTTPError
		if errors.As(err, &httpErr) {
			attrs = append(attrs, "status_code", httpErr.StatusCode)
		}
	}
	c.logger.WarnContext(ctx, msg, attrs...)
}

func latestDataUnavailableError(job scheduler.Job, err error) error {
	var httpErr *oem.HTTPError
	statusCode := 0
	if errors.As(err, &httpErr) {
		statusCode = httpErr.StatusCode
	}
	return &LatestDataUnavailableError{
		TargetID:        job.Target.ID,
		TargetName:      job.Target.Name,
		MetricGroupName: job.MetricGroupName,
		StatusCode:      statusCode,
		Err:             err,
	}
}
