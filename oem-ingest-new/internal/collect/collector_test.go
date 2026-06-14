package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"oem-ingest-new/internal/auth"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/scheduler"
)

func TestCollectorCollectsPayloadWithItemsAndUpdatesResponseMonitor(t *testing.T) {
	now := time.Unix(1700000000, 0)
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {
				Keys:    []oem.MetricKey{{Name: "cpuName"}},
				Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}},
			},
		},
		latest: map[string]oem.LatestData{
			"target-1\x00Load": {
				MetricGroupName: "Load",
				Items:           []map[string]any{{"cpuName": "cpu0", "value": 42}},
			},
		},
	}
	monitor := NewResponseMonitor()
	collector := NewCollector(client, CollectorOptions{
		Clock:           func() time.Time { return now },
		ResponseMonitor: monitor,
	})

	result, err := collector.Collect(context.Background(), collectJob())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if result.Datapoints() != 1 || result.LatestData.Items[0]["value"] != 42 {
		t.Fatalf("unexpected latestData result: %#v", result)
	}
	if result.Metadata.Keys[0] != "cpuName" {
		t.Fatalf("metadata keys = %#v, want cpuName", result.Metadata.Keys)
	}
	if got, ok := monitor.Last("target-1"); !ok || !got.Equal(now) {
		t.Fatalf("last useful collection = %s, %v; want %s, true", got, ok, now)
	}
	if got := collector.SnapshotStats(); got.DatapointsCollectedTotal != 1 || got.CollectionErrorsTotal != 0 || got.UnavailableGroupsTotal != 0 {
		t.Fatalf("unexpected collector stats: %#v", got)
	}
	if client.metricGroupCalls("target-1", "Load") != 1 || client.latestCallsFor("target-1", "Load") != 1 {
		t.Fatalf("unexpected calls: metadata=%d latest=%d", client.metricGroupCalls("target-1", "Load"), client.latestCallsFor("target-1", "Load"))
	}
}

func TestCollectorAllowsEmptyLatestDataWithoutUpdatingResponseMonitor(t *testing.T) {
	now := time.Unix(1700000100, 0)
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}}},
		},
		latest: map[string]oem.LatestData{
			"target-1\x00Load": {MetricGroupName: "Load", Items: []map[string]any{}},
		},
	}
	monitor := NewResponseMonitor()
	collector := NewCollector(client, CollectorOptions{
		Clock:           func() time.Time { return now },
		ResponseMonitor: monitor,
	})

	result, err := collector.Collect(context.Background(), collectJob())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if result.Datapoints() != 0 {
		t.Fatalf("Datapoints = %d, want 0", result.Datapoints())
	}
	if got, ok := monitor.Last("target-1"); ok {
		t.Fatalf("empty payload should not update response monitor, got %s", got)
	}
	if got := collector.SnapshotStats(); got.DatapointsCollectedTotal != 0 || got.CollectionErrorsTotal != 0 {
		t.Fatalf("unexpected collector stats: %#v", got)
	}
}

func TestCollectorCountsOnlyNonKeyValuesAsDatapoints(t *testing.T) {
	now := time.Unix(1700000200, 0)
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {
				Keys: []oem.MetricKey{{Name: "cpuName"}},
				Metrics: []oem.MetricDefinition{
					{Name: "value", DataType: "NUMBER"},
					{Name: "state", DataType: "STRING"},
				},
			},
		},
		latest: map[string]oem.LatestData{
			"target-1\x00Load": {
				MetricGroupName: "Load",
				Items: []map[string]any{
					{"cpuName": "cpu0", "value": 42, "state": "ok"},
					{"cpuName": "cpu1"},
				},
			},
		},
	}
	monitor := NewResponseMonitor()
	collector := NewCollector(client, CollectorOptions{
		Clock:           func() time.Time { return now },
		ResponseMonitor: monitor,
	})

	result, err := collector.Collect(context.Background(), collectJob())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if got := result.Datapoints(); got != 2 {
		t.Fatalf("Datapoints = %d, want 2 non-key values", got)
	}
	if got := collector.SnapshotStats(); got.DatapointsCollectedTotal != 2 {
		t.Fatalf("DatapointsCollectedTotal = %d, want 2", got.DatapointsCollectedTotal)
	}
	if got, ok := monitor.Last("target-1"); !ok || !got.Equal(now) {
		t.Fatalf("last useful collection = %s, %v; want %s, true", got, ok, now)
	}
}

func TestCollectorDoesNotTreatItemsWithOnlyKeysAsUseful(t *testing.T) {
	now := time.Unix(1700000300, 0)
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {
				Keys:    []oem.MetricKey{{Name: "cpuName"}},
				Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}},
			},
		},
		latest: map[string]oem.LatestData{
			"target-1\x00Load": {
				MetricGroupName: "Load",
				Items:           []map[string]any{{"cpuName": "cpu0"}},
			},
		},
	}
	monitor := NewResponseMonitor()
	collector := NewCollector(client, CollectorOptions{
		Clock:           func() time.Time { return now },
		ResponseMonitor: monitor,
	})

	result, err := collector.Collect(context.Background(), collectJob())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if got := result.Datapoints(); got != 0 {
		t.Fatalf("Datapoints = %d, want 0", got)
	}
	if _, ok := monitor.Last("target-1"); ok {
		t.Fatal("item with only keys should not update response monitor")
	}
	if got := collector.SnapshotStats(); got.DatapointsCollectedTotal != 0 {
		t.Fatalf("DatapointsCollectedTotal = %d, want 0", got.DatapointsCollectedTotal)
	}
}

func TestCollectorUsesNormalizedIdentityForLatestData(t *testing.T) {
	now := time.Unix(1700000400, 0)
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}}},
		},
		latest: map[string]oem.LatestData{
			"target-1\x00Load": {
				MetricGroupName: "Load",
				Items:           []map[string]any{{"value": 42}},
			},
		},
	}
	monitor := NewResponseMonitor()
	collector := NewCollector(client, CollectorOptions{
		Clock:           func() time.Time { return now },
		ResponseMonitor: monitor,
	})
	job := collectJob()
	job.Target.ID = " target-1 "
	job.MetricGroupName = " Load "
	job.MetricGroup.MetricGroupName = " Load "

	result, err := collector.Collect(context.Background(), job)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if result.Job.Target.ID != "target-1" || result.Job.MetricGroupName != "Load" {
		t.Fatalf("result job identity = %q/%q, want normalized target-1/Load", result.Job.Target.ID, result.Job.MetricGroupName)
	}
	if client.metricGroupCalls("target-1", "Load") != 1 || client.latestCallsFor("target-1", "Load") != 1 {
		t.Fatalf("expected normalized calls, metadata=%d latest=%d", client.metricGroupCalls("target-1", "Load"), client.latestCallsFor("target-1", "Load"))
	}
	if got, ok := monitor.Last("target-1"); !ok || !got.Equal(now) {
		t.Fatalf("normalized response monitor = %s, %v; want %s, true", got, ok, now)
	}
	if _, ok := monitor.Last(" target-1 "); ok {
		t.Fatal("response monitor should not keep raw whitespace target ID")
	}
}

func TestCollectorAppliesLatestDataPaginationThroughOEMClient(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "sysman" || password != "secret" {
			http.Error(w, "missing basic auth", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		requests = append(requests, r.URL.String())
		mu.Unlock()

		switch r.URL.Path {
		case "/em/api/targets/target-1/metricGroups/Load":
			writeCollectJSON(t, w, oem.MetricGroup{
				Name:    "Load",
				Keys:    []oem.MetricKey{{Name: "cpuName"}},
				Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}},
			})
		case "/em/api/targets/target-1/metricGroups/Load/latestData":
			switch r.URL.Query().Get("page") {
			case "":
				if got := r.URL.Query().Get("limit"); got != "200" {
					t.Fatalf("limit = %q, want 200", got)
				}
				writeCollectJSON(t, w, oem.LatestData{
					MetricGroupName: "Load",
					Links:           oem.Links{"next": {Href: "?page=2"}},
					Items:           []map[string]any{{"cpuName": "cpu0", "value": 1}},
				})
			case "2":
				writeCollectJSON(t, w, oem.LatestData{
					MetricGroupName: "Load",
					Items:           []map[string]any{{"cpuName": "cpu1", "value": 2}},
				})
			default:
				t.Fatalf("unexpected latestData query: %s", r.URL.RawQuery)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := oem.New(oem.Options{
		Endpoint:    server.URL,
		Credentials: auth.Credentials{User: "sysman", Password: "secret"},
		MaxRetries:  0,
	})
	if err != nil {
		t.Fatalf("oem.New returned error: %v", err)
	}
	collector := NewCollector(client, CollectorOptions{})

	result, err := collector.Collect(context.Background(), collectJob())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if result.Datapoints() != 2 || result.LatestData.Count != 2 {
		t.Fatalf("unexpected paged latestData result: %#v", result.LatestData)
	}
	secondValue, ok := result.LatestData.Items[1]["value"].(json.Number)
	if !ok || secondValue.String() != "2" {
		t.Fatalf("second value = %#v, want json.Number(2)", result.LatestData.Items[1]["value"])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("requests = %#v, want metadata + two latestData pages", requests)
	}
}

func TestCollectorTreatsLatestDataNotFoundAsUnavailable(t *testing.T) {
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}}},
		},
		latestErrors: map[string]error{
			"target-1\x00Load": &oem.HTTPError{StatusCode: http.StatusNotFound, Method: http.MethodGet, URL: "http://oem.example/latestData"},
		},
	}
	logger := &recordingLogger{}
	collector := NewCollector(client, CollectorOptions{Logger: logger})

	_, err := collector.Collect(context.Background(), collectJob())
	if !errors.Is(err, ErrMetricGroupUnavailable) {
		t.Fatalf("Collect error = %T %v, want ErrMetricGroupUnavailable", err, err)
	}
	var unavailable *LatestDataUnavailableError
	if !errors.As(err, &unavailable) || unavailable.StatusCode != http.StatusNotFound {
		t.Fatalf("Collect did not return latestData unavailable 404: %T %v", err, err)
	}
	if !logger.containsWarn("latestData de grupo de metrica indisponivel", "target-1", "Load", "404") {
		t.Fatalf("expected unavailable latestData warning, got %#v", logger.warnings)
	}
	if got := collector.SnapshotStats(); got.UnavailableGroupsTotal != 1 || got.CollectionErrorsTotal != 0 {
		t.Fatalf("unexpected collector stats: %#v", got)
	}
}

func TestCollectorCountsMetadataNotFoundAsUnavailable(t *testing.T) {
	client := &fakeCollectClient{
		metadataErrors: map[string]error{
			"target-1\x00Load": &oem.HTTPError{StatusCode: http.StatusNotFound, Method: http.MethodGet, URL: "http://oem.example/metricGroups/Load"},
		},
	}
	logger := &recordingLogger{}
	collector := NewCollector(client, CollectorOptions{Logger: logger})

	_, err := collector.Collect(context.Background(), collectJob())
	if !errors.Is(err, ErrMetricGroupUnavailable) {
		t.Fatalf("Collect error = %T %v, want ErrMetricGroupUnavailable", err, err)
	}
	if !logger.containsWarn("metadata de grupo de metrica indisponivel", "target-1", "Load", "404") {
		t.Fatalf("expected unavailable metadata warning, got %#v", logger.warnings)
	}
	if got := collector.SnapshotStats(); got.UnavailableGroupsTotal != 1 || got.CollectionErrorsTotal != 0 {
		t.Fatalf("unexpected collector stats: %#v", got)
	}
	if client.latestCallsFor("target-1", "Load") != 0 {
		t.Fatalf("latestData calls = %d, want 0 after metadata 404", client.latestCallsFor("target-1", "Load"))
	}
}

func TestCollectorLogsAndCountsTransientLatestDataErrors(t *testing.T) {
	client := &fakeCollectClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {Metrics: []oem.MetricDefinition{{Name: "value", DataType: "NUMBER"}}},
		},
		latestErrors: map[string]error{
			"target-1\x00Load": &oem.HTTPError{StatusCode: http.StatusInternalServerError, Method: http.MethodGet, URL: "http://oem.example/latestData"},
		},
	}
	logger := &recordingLogger{}
	monitor := NewResponseMonitor()
	collector := NewCollector(client, CollectorOptions{
		Logger:          logger,
		ResponseMonitor: monitor,
	})

	_, err := collector.Collect(context.Background(), collectJob())
	if err == nil {
		t.Fatal("expected transient latestData error")
	}
	if errors.Is(err, ErrMetricGroupUnavailable) {
		t.Fatalf("500 should not be treated as unavailable: %v", err)
	}
	if !logger.containsWarn("falha transitoria ao coletar latestData", "target-1", "Load", "500") {
		t.Fatalf("expected transient latestData warning, got %#v", logger.warnings)
	}
	if got := collector.SnapshotStats(); got.CollectionErrorsTotal != 1 || got.UnavailableGroupsTotal != 0 || got.DatapointsCollectedTotal != 0 {
		t.Fatalf("unexpected collector stats: %#v", got)
	}
	if _, ok := monitor.Last("target-1"); ok {
		t.Fatal("failed collection should not update response monitor")
	}
}

type fakeCollectClient struct {
	mu              sync.Mutex
	groups          map[string]oem.MetricGroup
	latest          map[string]oem.LatestData
	metadataErrors  map[string]error
	latestErrors    map[string]error
	metadataCallMap map[string]int
	latestCallMap   map[string]int
}

func (f *fakeCollectClient) MetricGroup(_ context.Context, targetID, groupName string) (oem.MetricGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metadataCallMap == nil {
		f.metadataCallMap = make(map[string]int)
	}
	key := cacheTestKey(targetID, groupName)
	f.metadataCallMap[key]++
	if f.metadataErrors != nil && f.metadataErrors[key] != nil {
		return oem.MetricGroup{}, f.metadataErrors[key]
	}
	group, ok := f.groups[key]
	if !ok {
		return oem.MetricGroup{}, fmt.Errorf("unexpected MetricGroup call for %s/%s", targetID, groupName)
	}
	return group, nil
}

func (f *fakeCollectClient) LatestData(_ context.Context, targetID, groupName string) (oem.LatestData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.latestCallMap == nil {
		f.latestCallMap = make(map[string]int)
	}
	key := cacheTestKey(targetID, groupName)
	f.latestCallMap[key]++
	if f.latestErrors != nil && f.latestErrors[key] != nil {
		return oem.LatestData{}, f.latestErrors[key]
	}
	latest, ok := f.latest[key]
	if !ok {
		return oem.LatestData{}, fmt.Errorf("unexpected LatestData call for %s/%s", targetID, groupName)
	}
	return latest, nil
}

func (f *fakeCollectClient) metricGroupCalls(targetID, groupName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.metadataCallMap[cacheTestKey(targetID, groupName)]
}

func (f *fakeCollectClient) latestCallsFor(targetID, groupName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latestCallMap[cacheTestKey(targetID, groupName)]
}

func collectJob() scheduler.Job {
	return scheduler.Job{
		ID:              "job-load",
		SiteName:        "oraemc",
		Endpoint:        "http://oem.example",
		Target:          collectTarget("target-1", "dbhost01", "host"),
		MetricGroup:     config.MetricGroupConfig{Freq: 1, MetricGroupName: "Load"},
		MetricGroupName: "Load",
		Frequency:       time.Minute,
	}
}

func collectTarget(id, name, targetType string) config.TargetConfig {
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

func writeCollectJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
}
