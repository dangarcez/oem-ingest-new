package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/transform"
)

const (
	DefaultPollInterval = 5 * time.Minute
	DefaultAgeWindow    = 1
	incidentMetricName  = "oem_incident"
)

// Client lists OEM incidents.
type Client interface {
	Incidents(context.Context, int) (oem.Page[oem.Incident], error)
}

// LogSink receives incident logs. LogsExporter satisfies this interface.
type LogSink interface {
	Add(...transform.LogRecord)
}

// Logger is intentionally compatible with slog.Logger.
type Logger interface {
	InfoContext(context.Context, string, ...any)
	WarnContext(context.Context, string, ...any)
}

// Options configures Poller.
type Options struct {
	Client         Client
	Logs           LogSink
	PollInterval   time.Duration
	AgeWindowHours int
	Logger         Logger
}

// PollResult summarizes one incident polling cycle.
type PollResult struct {
	Seen       int
	New        int
	Duplicates int
}

// Poller periodically reads OEM incidents and converts new incidents into OTLP
// log records.
type Poller struct {
	client         Client
	logs           LogSink
	pollInterval   time.Duration
	ageWindowHours int
	logger         Logger

	mu   sync.Mutex
	seen map[string]struct{}
}

// New creates a Poller with the legacy polling defaults: a 5 minute interval
// and a 1 hour incident search window.
func New(opts Options) (*Poller, error) {
	if opts.Client == nil {
		return nil, errors.New("incidents: Client obrigatorio")
	}
	if opts.Logs == nil {
		return nil, errors.New("incidents: LogSink obrigatorio")
	}

	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}
	if pollInterval < 0 {
		return nil, errors.New("incidents: PollInterval nao pode ser negativo")
	}

	ageWindowHours := opts.AgeWindowHours
	if ageWindowHours == 0 {
		ageWindowHours = DefaultAgeWindow
	}
	if ageWindowHours < 0 {
		return nil, errors.New("incidents: AgeWindowHours nao pode ser negativo")
	}

	return &Poller{
		client:         opts.Client,
		logs:           opts.Logs,
		pollInterval:   pollInterval,
		ageWindowHours: ageWindowHours,
		logger:         opts.Logger,
		seen:           make(map[string]struct{}),
	}, nil
}

// Run starts incident polling. It polls immediately once, then repeats at the
// configured interval until ctx is canceled.
func (p *Poller) Run(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	p.pollAndLog(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.pollAndLog(ctx)
		}
	}
}

// PollOnce executes one incident polling cycle.
func (p *Poller) PollOnce(ctx context.Context) (PollResult, error) {
	if p == nil {
		return PollResult{}, nil
	}
	if err := ctx.Err(); err != nil {
		return PollResult{}, err
	}

	page, err := p.client.Incidents(ctx, p.ageWindowHours)
	if err != nil {
		return PollResult{}, err
	}

	result := PollResult{Seen: len(page.Items)}
	records := make([]transform.LogRecord, 0, len(page.Items))
	for _, incident := range page.Items {
		id := strings.TrimSpace(incident.ID)
		if id == "" {
			continue
		}
		if !p.markSeen(id) {
			result.Duplicates++
			continue
		}
		records = append(records, incidentLogRecord(incident))
		result.New++
	}

	if len(records) > 0 {
		p.logs.Add(records...)
	}
	p.logPollSummary(ctx, result)
	return result, nil
}

func (p *Poller) pollAndLog(ctx context.Context) {
	_, err := p.PollOnce(ctx)
	if err != nil {
		if p.logger != nil && ctx.Err() == nil {
			p.logger.WarnContext(ctx, "falha ao consultar incidentes OEM", "err", err)
		}
		return
	}
}

func (p *Poller) markSeen(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seen[id]; ok {
		return false
	}
	p.seen[id] = struct{}{}
	return true
}

func (p *Poller) logPollSummary(ctx context.Context, result PollResult) {
	if p.logger == nil {
		return
	}
	p.logger.InfoContext(ctx, "polling de incidentes OEM concluido",
		"incidents_seen", result.Seen,
		"incidents_new", result.New,
		"incidents_duplicates", result.Duplicates,
		"age_window_hours", p.ageWindowHours,
	)
}

func incidentLogRecord(incident oem.Incident) transform.LogRecord {
	attrs := incidentAttributes(incident)
	correctedCreated, createdOK := correctedIncidentTime(incident.TimeCreated)
	if createdOK {
		attrs["timeCreated"] = formatIncidentTime(correctedCreated)
	}
	if correctedUpdated, ok := correctedIncidentTime(incident.TimeUpdated); ok {
		attrs["timeUpdated"] = formatIncidentTime(correctedUpdated)
	}

	targetID := strings.TrimSpace(incident.ID)
	if len(incident.Targets) > 0 && strings.TrimSpace(incident.Targets[0].ID) != "" {
		targetID = strings.TrimSpace(incident.Targets[0].ID)
	}

	record := transform.LogRecord{
		MetricName:   incidentMetricName,
		TargetID:     targetID,
		SeriesID:     strings.TrimSpace(incident.ID),
		Body:         incident.Message,
		Attributes:   attrs,
		SeverityText: "WARN",
	}
	if createdOK {
		record.Timestamp = correctedCreated
	}
	return record
}

func incidentAttributes(incident oem.Incident) transform.Attributes {
	attrs := transform.Attributes{}
	addPresent := func(field string, value any) {
		if incident.HasField(field) {
			attrs[field] = value
		}
	}

	addPresent("id", incident.ID)
	addPresent("displayId", incident.DisplayID)
	addPresent("timeCreated", incident.TimeCreated)
	addPresent("timeUpdated", incident.TimeUpdated)
	addPresent("ageInHours", incident.AgeInHours)
	addPresent("isOpen", incident.IsOpen)
	addPresent("status", incident.Status)
	addPresent("owner", incident.Owner)
	addPresent("isAcknowledged", incident.IsAcknowledged)
	addPresent("isEscalated", incident.IsEscalated)
	addPresent("severity", incident.Severity)
	addPresent("canBeManuallyClosed", incident.CanBeManuallyClosed)
	addPresent("isDiagnosticIncident", incident.IsDiagnostic)

	if incident.HasField("targets") {
		attrs["incident_target_count"] = len(incident.Targets)
	}
	if incident.HasField("targets") && len(incident.Targets) > 0 {
		target := incident.Targets[0]
		attrs["target_id"] = target.ID
		attrs["target_name"] = target.Name
		attrs["target_type"] = target.TypeName
		attrs["target_type_display_name"] = target.TypeDisplayName
		attrs["targets"] = jsonString(incident.Targets)
	}
	if incident.HasField("links") && len(incident.Links) > 0 {
		attrs["links"] = jsonString(incident.Links)
	}
	for key, value := range incident.Extra {
		if key == "" || key == "message" {
			continue
		}
		if _, exists := attrs[key]; exists {
			continue
		}
		attrs[key] = normalizeAttributeValue(value)
	}
	return attrs
}

func normalizeAttributeValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return v
	default:
		return jsonString(v)
	}
}

func jsonString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

// correctedIncidentTime preserves the legacy workaround from oemalert.py:
// incident timestamps returned by the OEM environment were three hours ahead,
// so the collector subtracts three hours before exporting/logging them.
func correctedIncidentTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC().Add(-3 * time.Hour), true
}

func formatIncidentTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
