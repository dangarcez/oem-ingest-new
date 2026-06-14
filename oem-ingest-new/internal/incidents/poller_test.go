package incidents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/transform"
)

func TestPollOnceExportsNewIncidentAsLog(t *testing.T) {
	client := &fakeIncidentClient{
		pages: []oem.Page[oem.Incident]{{
			Items: []oem.Incident{{
				ID:          "INC-1",
				DisplayID:   42,
				Message:     "Database target is down",
				TimeCreated: "2026-06-14T12:30:15.123456Z",
				TimeUpdated: "2026-06-14T12:45:16.789123Z",
				AgeInHours:  0.25,
				IsOpen:      true,
				Status:      "Open",
				Owner:       "SYSMAN",
				Severity:    "Critical",
				Targets: []oem.IncidentTarget{{
					ID:              "target-1",
					Name:            "cdbp51bc",
					TypeName:        "rac_database",
					TypeDisplayName: "Cluster Database",
				}},
				Extra: map[string]any{"priority": "High"},
			}},
		}},
	}
	sink := &recordingLogSink{}
	logger := &recordingLogger{}
	poller, err := New(Options{Client: client, Logs: sink, Logger: logger})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := poller.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce() error = %v", err)
	}
	if result.Seen != 1 || result.New != 1 || result.Duplicates != 0 {
		t.Fatalf("PollOnce() result = %#v, want one new incident", result)
	}
	if client.ageWindows[0] != DefaultAgeWindow {
		t.Fatalf("age window = %d, want default %d", client.ageWindows[0], DefaultAgeWindow)
	}

	records := sink.recordsSnapshot()
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	record := records[0]
	if record.MetricName != "oem_incident" {
		t.Fatalf("MetricName = %q, want oem_incident", record.MetricName)
	}
	if record.TargetID != "target-1" || record.SeriesID != "INC-1" {
		t.Fatalf("target/series = %q/%q, want target-1/INC-1", record.TargetID, record.SeriesID)
	}
	if record.Body != "Database target is down" {
		t.Fatalf("Body = %q, want incident message", record.Body)
	}

	wantCreated := time.Date(2026, 6, 14, 9, 30, 15, 123456000, time.UTC)
	if !record.Timestamp.Equal(wantCreated) {
		t.Fatalf("Timestamp = %s, want %s", record.Timestamp, wantCreated)
	}
	if got := record.Attributes["timeCreated"]; got != "2026-06-14T09:30:15.123Z" {
		t.Fatalf("timeCreated attr = %#v, want corrected legacy timestamp", got)
	}
	if got := record.Attributes["timeUpdated"]; got != "2026-06-14T09:45:16.789Z" {
		t.Fatalf("timeUpdated attr = %#v, want corrected legacy timestamp", got)
	}
	if got := record.Attributes["id"]; got != "INC-1" {
		t.Fatalf("id attr = %#v, want INC-1", got)
	}
	if got := record.Attributes["displayId"]; got != 42 {
		t.Fatalf("displayId attr = %#v, want 42", got)
	}
	if got := record.Attributes["target_name"]; got != "cdbp51bc" {
		t.Fatalf("target_name attr = %#v, want cdbp51bc", got)
	}
	if got := record.Attributes["priority"]; got != "High" {
		t.Fatalf("priority attr = %#v, want High", got)
	}
	if _, ok := record.Attributes["message"]; ok {
		t.Fatalf("message should be exported as body, got attrs %#v", record.Attributes)
	}
	if len(logger.infosSnapshot()) != 1 {
		t.Fatalf("info logs = %#v, want one summary", logger.infosSnapshot())
	}
}

func TestPollOnceSkipsDuplicateIncidentID(t *testing.T) {
	incident := oem.Incident{ID: "INC-1", Message: "same incident", TimeCreated: "2026-06-14T12:00:00.000Z"}
	client := &fakeIncidentClient{
		pages: []oem.Page[oem.Incident]{
			{Items: []oem.Incident{incident}},
			{Items: []oem.Incident{incident}},
		},
	}
	sink := &recordingLogSink{}
	poller, err := New(Options{Client: client, Logs: sink})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := poller.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("first PollOnce() error = %v", err)
	}
	second, err := poller.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("second PollOnce() error = %v", err)
	}

	if first.New != 1 || first.Duplicates != 0 {
		t.Fatalf("first result = %#v, want one new incident", first)
	}
	if second.New != 0 || second.Duplicates != 1 {
		t.Fatalf("second result = %#v, want duplicate skipped", second)
	}
	if got := len(sink.recordsSnapshot()); got != 1 {
		t.Fatalf("records len = %d, want duplicate not exported", got)
	}
}

func TestRunPollsImmediatelyThenAtConfiguredInterval(t *testing.T) {
	client := &fakeIncidentClient{
		pages: []oem.Page[oem.Incident]{
			{},
			{},
		},
	}
	sink := &recordingLogSink{}
	poller, err := New(Options{
		Client:       client,
		Logs:         sink,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- poller.Run(ctx)
	}()

	deadline := time.After(time.Second)
	for {
		if client.calls() >= 2 {
			cancel()
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for periodic incident polling")
		case <-time.After(time.Millisecond):
		}
	}

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestNewAppliesLegacyDefaultsAndValidatesDependencies(t *testing.T) {
	poller, err := New(Options{Client: &fakeIncidentClient{}, Logs: &recordingLogSink{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if poller.pollInterval != DefaultPollInterval {
		t.Fatalf("pollInterval = %s, want %s", poller.pollInterval, DefaultPollInterval)
	}
	if poller.ageWindowHours != DefaultAgeWindow {
		t.Fatalf("ageWindowHours = %d, want %d", poller.ageWindowHours, DefaultAgeWindow)
	}

	if _, err := New(Options{Logs: &recordingLogSink{}}); err == nil || !strings.Contains(err.Error(), "Client") {
		t.Fatalf("New() without client error = %v, want client validation", err)
	}
	if _, err := New(Options{Client: &fakeIncidentClient{}}); err == nil || !strings.Contains(err.Error(), "LogSink") {
		t.Fatalf("New() without sink error = %v, want sink validation", err)
	}
	if _, err := New(Options{Client: &fakeIncidentClient{}, Logs: &recordingLogSink{}, PollInterval: -time.Second}); err == nil {
		t.Fatal("New() with negative poll interval error = nil, want validation")
	}
	if _, err := New(Options{Client: &fakeIncidentClient{}, Logs: &recordingLogSink{}, AgeWindowHours: -1}); err == nil {
		t.Fatal("New() with negative age window error = nil, want validation")
	}
}

type fakeIncidentClient struct {
	mu         sync.Mutex
	pages      []oem.Page[oem.Incident]
	ageWindows []int
}

func (f *fakeIncidentClient) Incidents(_ context.Context, ageWindow int) (oem.Page[oem.Incident], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ageWindows = append(f.ageWindows, ageWindow)
	if len(f.pages) == 0 {
		return oem.Page[oem.Incident]{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakeIncidentClient) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ageWindows)
}

type recordingLogSink struct {
	mu      sync.Mutex
	records []transform.LogRecord
}

func (r *recordingLogSink) Add(records ...transform.LogRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, records...)
}

func (r *recordingLogSink) recordsSnapshot() []transform.LogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]transform.LogRecord, len(r.records))
	copy(out, r.records)
	return out
}

type recordingLogger struct {
	mu    sync.Mutex
	infos []string
	warns []string
}

func (r *recordingLogger) InfoContext(_ context.Context, msg string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, msg)
}

func (r *recordingLogger) WarnContext(_ context.Context, msg string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, msg)
}

func (r *recordingLogger) infosSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.infos))
	copy(out, r.infos)
	return out
}
