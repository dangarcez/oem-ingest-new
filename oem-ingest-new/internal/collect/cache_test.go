package collect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"oem-ingest-new/internal/oem"
)

func TestMetadataCacheReusesMetricGroupMetadata(t *testing.T) {
	client := &fakeMetricGroupClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Filesystems": {
				Name: "Filesystems",
				Keys: []oem.MetricKey{
					{Name: "MountPoint"},
					{Name: "FileSystem"},
				},
				Metrics: []oem.MetricDefinition{
					{Name: "SpaceUsedPct", DataType: "NUMBER"},
					{Name: "Status", DataType: "STRING"},
				},
			},
		},
	}
	cache := NewMetadataCache(client, MetadataCacheOptions{})
	req := MetadataRequest{
		TargetID:        "target-1",
		TargetName:      "dbhost01",
		MetricGroupName: "Filesystems",
	}

	first, err := cache.Get(context.Background(), req)
	if err != nil {
		t.Fatalf("first Get returned error: %v", err)
	}
	first.Keys[0] = "mutated"
	first.MetricByName["Status"] = oem.MetricDefinition{Name: "Status", DataType: "BROKEN"}

	second, err := cache.Get(context.Background(), req)
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}

	if client.callsFor("target-1", "Filesystems") != 1 {
		t.Fatalf("metadata calls = %d, want 1", client.callsFor("target-1", "Filesystems"))
	}
	if got, want := strings.Join(second.Keys, ","), "MountPoint,FileSystem"; got != want {
		t.Fatalf("keys = %q, want %q", got, want)
	}
	if dataType, ok := second.DataType("SpaceUsedPct"); !ok || dataType != "NUMBER" {
		t.Fatalf("SpaceUsedPct data type = %q, %v; want NUMBER, true", dataType, ok)
	}
	if dataType, ok := second.DataType("Status"); !ok || dataType != "STRING" {
		t.Fatalf("Status data type = %q, %v; want STRING, true", dataType, ok)
	}
}

func TestMetadataCacheKeepsTargetGroupPairsSeparate(t *testing.T) {
	client := &fakeMetricGroupClient{
		groups: map[string]oem.MetricGroup{
			"target-1\x00Load": {Keys: []oem.MetricKey{{Name: "cpu"}}},
			"target-2\x00Load": {Keys: []oem.MetricKey{{Name: "disk"}}},
		},
	}
	cache := NewMetadataCache(client, MetadataCacheOptions{})

	first, err := cache.Get(context.Background(), MetadataRequest{TargetID: "target-1", MetricGroupName: "Load"})
	if err != nil {
		t.Fatalf("first Get returned error: %v", err)
	}
	second, err := cache.Get(context.Background(), MetadataRequest{TargetID: "target-2", MetricGroupName: "Load"})
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}

	if first.Keys[0] != "cpu" || second.Keys[0] != "disk" {
		t.Fatalf("unexpected keys: first=%#v second=%#v", first.Keys, second.Keys)
	}
}

func TestMetadataCacheAllowsBodylessMetricsWithEmptyKeys(t *testing.T) {
	client := &fakeMetricGroupClient{}
	cache := NewMetadataCache(client, MetadataCacheOptions{})

	metadata, err := cache.Get(context.Background(), MetadataRequest{
		TargetID:        "target-1",
		MetricGroupName: "oem_monitor_response",
		Bodyless:        true,
	})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	again, err := cache.Get(context.Background(), MetadataRequest{
		TargetID:        "target-1",
		MetricGroupName: "oem_monitor_response",
		Bodyless:        true,
	})
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}

	if len(metadata.Keys) != 0 || len(again.Keys) != 0 || !metadata.Bodyless {
		t.Fatalf("expected empty bodyless metadata, got first=%#v second=%#v", metadata, again)
	}
	if got := client.totalCalls(); got != 0 {
		t.Fatalf("bodyless metadata should not call OEM, got %d calls", got)
	}
}

func TestMetadataCacheWarnsAndCachesNotFoundAsUnavailable(t *testing.T) {
	client := &fakeMetricGroupClient{
		errors: map[string]error{
			"target-1\x00MissingGroup": &oem.HTTPError{StatusCode: http.StatusNotFound, Method: http.MethodGet, URL: "http://oem.example"},
		},
	}
	logger := &recordingLogger{}
	cache := NewMetadataCache(client, MetadataCacheOptions{Logger: logger})
	req := MetadataRequest{
		TargetID:        "target-1",
		TargetName:      "dbhost01",
		MetricGroupName: "MissingGroup",
	}

	for i := 0; i < 2; i++ {
		_, err := cache.Get(context.Background(), req)
		if !errors.Is(err, ErrMetricGroupUnavailable) {
			t.Fatalf("Get #%d error = %T %v, want ErrMetricGroupUnavailable", i+1, err, err)
		}
		var unavailable *MetadataUnavailableError
		if !errors.As(err, &unavailable) || unavailable.StatusCode != http.StatusNotFound {
			t.Fatalf("Get #%d did not return not-found MetadataUnavailableError: %T %v", i+1, err, err)
		}
	}

	if client.callsFor("target-1", "MissingGroup") != 1 {
		t.Fatalf("metadata 404 should be cached, calls = %d", client.callsFor("target-1", "MissingGroup"))
	}
	if !logger.containsWarn("metadata de grupo de metrica indisponivel", "target-1", "MissingGroup", "404") {
		t.Fatalf("expected warning for unavailable metadata, got %#v", logger.warnings)
	}
}

func TestMetadataCacheReturnsTransientMetadataErrorsWithoutCaching(t *testing.T) {
	client := &fakeMetricGroupClient{
		errors: map[string]error{
			"target-1\x00Load": &oem.HTTPError{StatusCode: http.StatusInternalServerError, Method: http.MethodGet, URL: "http://oem.example"},
		},
	}
	cache := NewMetadataCache(client, MetadataCacheOptions{})
	req := MetadataRequest{TargetID: "target-1", MetricGroupName: "Load"}

	if _, err := cache.Get(context.Background(), req); err == nil {
		t.Fatal("expected first transient error")
	}
	client.setGroup("target-1", "Load", oem.MetricGroup{Keys: []oem.MetricKey{{Name: "cpu"}}})
	delete(client.errors, "target-1\x00Load")
	metadata, err := cache.Get(context.Background(), req)
	if err != nil {
		t.Fatalf("second Get returned error after transient recovery: %v", err)
	}

	if client.callsFor("target-1", "Load") != 2 {
		t.Fatalf("transient error should not be cached, calls = %d", client.callsFor("target-1", "Load"))
	}
	if len(metadata.Keys) != 1 || metadata.Keys[0] != "cpu" {
		t.Fatalf("unexpected metadata after recovery: %#v", metadata)
	}
}

type fakeMetricGroupClient struct {
	mu     sync.Mutex
	groups map[string]oem.MetricGroup
	errors map[string]error
	calls  map[string]int
}

func (f *fakeMetricGroupClient) MetricGroup(_ context.Context, targetID, groupName string) (oem.MetricGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	key := cacheTestKey(targetID, groupName)
	f.calls[key]++
	if f.errors != nil && f.errors[key] != nil {
		return oem.MetricGroup{}, f.errors[key]
	}
	group, ok := f.groups[key]
	if !ok {
		return oem.MetricGroup{}, fmt.Errorf("unexpected MetricGroup call for %s/%s", targetID, groupName)
	}
	return group, nil
}

func (f *fakeMetricGroupClient) callsFor(targetID, groupName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[cacheTestKey(targetID, groupName)]
}

func (f *fakeMetricGroupClient) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, calls := range f.calls {
		total += calls
	}
	return total
}

func (f *fakeMetricGroupClient) setGroup(targetID, groupName string, group oem.MetricGroup) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.groups == nil {
		f.groups = make(map[string]oem.MetricGroup)
	}
	f.groups[cacheTestKey(targetID, groupName)] = group
}

func cacheTestKey(targetID, groupName string) string {
	return targetID + "\x00" + groupName
}

type recordingLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (r *recordingLogger) WarnContext(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, formatLog(msg, args...))
}

func (r *recordingLogger) containsWarn(parts ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, warning := range r.warnings {
		matched := true
		for _, part := range parts {
			if !strings.Contains(warning, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func formatLog(msg string, args ...any) string {
	var b strings.Builder
	b.WriteString(msg)
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(arg))
	}
	return b.String()
}
