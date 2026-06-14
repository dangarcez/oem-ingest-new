package oem

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oem-ingest-new/internal/auth"
)

func TestClientEndpointsApplyBasicAuthAndDecodeResponses(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "sysman" || password != "secret" {
			http.Error(w, "missing basic auth", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/em/api":
			writeJSON(t, w, map[string]any{"version": "13c"})
		case "/em/api/targets":
			writeJSON(t, w, Page[Target]{Items: []Target{{ID: "target-1", Name: "db1", TypeName: "oracle_database"}}})
		case "/em/api/targets/target-1/properties":
			writeJSON(t, w, Page[Property]{Items: []Property{{ID: "MachineName", Value: "dbhost"}}})
		case "/em/api/targets/target-1/metricGroups":
			writeJSON(t, w, Page[MetricGroup]{Items: []MetricGroup{{Name: "Response"}}})
		case "/em/api/targets/target-1/metricGroups/Response":
			writeJSON(t, w, MetricGroup{
				Name:    "Response",
				Keys:    []MetricKey{{Name: "instance"}},
				Metrics: []MetricDefinition{{Name: "Status", DataType: "NUMBER"}},
			})
		case "/em/api/targets/target-1/metricGroups/Response/latestData":
			if got := r.URL.Query().Get("limit"); got != "200" {
				t.Fatalf("limit = %q, want 200", got)
			}
			writeJSON(t, w, LatestData{
				MetricGroupName: "Response",
				Items:           []map[string]any{{"Status": 1}},
			})
		case "/em/api/incidents/":
			if got := r.URL.Query().Get("ageInHoursLessThanOrEqualTo"); got != "1" {
				t.Fatalf("ageInHoursLessThanOrEqualTo = %q, want 1", got)
			}
			writeJSON(t, w, Page[Incident]{Items: []Incident{{ID: "incident-1", Message: "alert"}}})
		case "/em/api/incidents/incident-1":
			writeJSON(t, w, Incident{ID: "incident-1", Status: "Closed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	api, err := client.API(ctx)
	if err != nil {
		t.Fatalf("API returned error: %v", err)
	}
	if api["version"] != "13c" {
		t.Fatalf("unexpected API response: %#v", api)
	}

	targets, err := client.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets returned error: %v", err)
	}
	if len(targets.Items) != 1 || targets.Items[0].ID != "target-1" {
		t.Fatalf("unexpected targets: %#v", targets)
	}

	properties, err := client.TargetProperties(ctx, "target-1")
	if err != nil {
		t.Fatalf("TargetProperties returned error: %v", err)
	}
	if properties.Items[0].Value != "dbhost" {
		t.Fatalf("unexpected properties: %#v", properties)
	}

	groups, err := client.TargetMetricGroups(ctx, "target-1")
	if err != nil {
		t.Fatalf("TargetMetricGroups returned error: %v", err)
	}
	if groups.Items[0].Name != "Response" {
		t.Fatalf("unexpected groups: %#v", groups)
	}

	group, err := client.MetricGroup(ctx, "target-1", "Response")
	if err != nil {
		t.Fatalf("MetricGroup returned error: %v", err)
	}
	if group.Keys[0].Name != "instance" || group.Metrics[0].DataType != "NUMBER" {
		t.Fatalf("unexpected group metadata: %#v", group)
	}

	latest, err := client.LatestData(ctx, "target-1", "Response")
	if err != nil {
		t.Fatalf("LatestData returned error: %v", err)
	}
	if latest.MetricGroupName != "Response" || len(latest.Items) != 1 {
		t.Fatalf("unexpected latest data: %#v", latest)
	}

	incidents, err := client.Incidents(ctx, 1)
	if err != nil {
		t.Fatalf("Incidents returned error: %v", err)
	}
	if incidents.Items[0].ID != "incident-1" {
		t.Fatalf("unexpected incidents: %#v", incidents)
	}

	incident, err := client.Incident(ctx, "incident-1")
	if err != nil {
		t.Fatalf("Incident returned error: %v", err)
	}
	if incident.Status != "Closed" {
		t.Fatalf("unexpected incident detail: %#v", incident)
	}

	stats := client.SnapshotStats()
	if stats.RequestsTotal != 8 || stats.RequestErrorsTotal != 0 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestPathSegmentsRemainEscaped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		if !strings.HasPrefix(r.RequestURI, "/em/api/targets/target%2F1/metricGroups/Metric%20Group%2FA") {
			t.Fatalf("request URI = %q, want escaped target and metric group segments", r.RequestURI)
		}
		writeJSON(t, w, MetricGroup{Name: "Metric Group/A"})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	group, err := client.MetricGroup(context.Background(), "target/1", "Metric Group/A")
	if err != nil {
		t.Fatalf("MetricGroup returned error: %v", err)
	}
	if group.Name != "Metric Group/A" {
		t.Fatalf("unexpected group: %#v", group)
	}
}

func TestListTargetsFollowsNextLinks(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		switch calls.Add(1) {
		case 1:
			if r.URL.Path != "/em/api/targets" {
				t.Fatalf("first path = %s", r.URL.Path)
			}
			writeJSON(t, w, Page[Target]{
				Links: Links{"next": {Href: "/em/api/targets?page=2"}},
				Items: []Target{{ID: "target-1"}},
			})
		case 2:
			if r.URL.Path != "/em/api/targets" || r.URL.Query().Get("page") != "2" {
				t.Fatalf("second request = %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			writeJSON(t, w, Page[Target]{
				Links: Links{"self": {Href: "/em/api/targets?page=2"}},
				Items: []Target{{ID: "target-2"}},
			})
		default:
			t.Fatalf("unexpected extra request %s", r.URL.String())
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	targets, err := client.ListTargets(context.Background())
	if err != nil {
		t.Fatalf("ListTargets returned error: %v", err)
	}
	if len(targets.Items) != 2 || targets.Items[0].ID != "target-1" || targets.Items[1].ID != "target-2" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
	if targets.Count != 2 {
		t.Fatalf("Count = %d, want 2", targets.Count)
	}
	if _, ok := targets.Links["next"]; ok {
		t.Fatalf("merged links should not retain next: %#v", targets.Links)
	}
}

func TestListTargetsFollowsQueryOnlyNextLink(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		switch calls.Add(1) {
		case 1:
			if r.URL.Path != "/em/api/targets" {
				t.Fatalf("first path = %s", r.URL.Path)
			}
			writeJSON(t, w, Page[Target]{
				Links: Links{"next": {Href: "?page=2"}},
				Items: []Target{{ID: "target-1"}},
			})
		case 2:
			if r.URL.Path != "/em/api/targets" || r.URL.Query().Get("page") != "2" {
				t.Fatalf("second request = %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			writeJSON(t, w, Page[Target]{Items: []Target{{ID: "target-2"}}})
		default:
			t.Fatalf("unexpected extra request %s", r.URL.String())
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	targets, err := client.ListTargets(context.Background())
	if err != nil {
		t.Fatalf("ListTargets returned error: %v", err)
	}
	if len(targets.Items) != 2 || targets.Items[1].ID != "target-2" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestListTargetsRejectsCyclicNextLink(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		calls.Add(1)
		writeJSON(t, w, Page[Target]{
			Links: Links{"next": {Href: "/em/api/targets?page=loop"}},
			Items: []Target{{ID: "target-loop"}},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.ListTargets(context.Background())
	if err == nil {
		t.Fatal("expected cyclic pagination error")
	}
	if !strings.Contains(err.Error(), "paginacao OEM ciclica") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestLatestDataFollowsNextLinksAndKeepsJSONNumbers(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		switch calls.Add(1) {
		case 1:
			if r.URL.Query().Get("limit") != "200" {
				t.Fatalf("limit = %q, want 200", r.URL.Query().Get("limit"))
			}
			writeJSON(t, w, LatestData{
				MetricGroupName: "Load",
				Links:           Links{"next": {Href: "/em/api/targets/target-1/metricGroups/Load/latestData?page=2"}},
				Items:           []map[string]any{{"value": 1.5}},
			})
		case 2:
			if r.URL.Query().Get("page") != "2" {
				t.Fatalf("page = %q, want 2", r.URL.Query().Get("page"))
			}
			writeJSON(t, w, LatestData{
				MetricGroupName: "Load",
				Items:           []map[string]any{{"value": 2.5}},
			})
		default:
			t.Fatalf("unexpected extra request %s", r.URL.String())
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	latest, err := client.LatestData(context.Background(), "target-1", "Load")
	if err != nil {
		t.Fatalf("LatestData returned error: %v", err)
	}
	if len(latest.Items) != 2 || latest.Count != 2 {
		t.Fatalf("unexpected latest data: %#v", latest)
	}
	value, ok := latest.Items[0]["value"].(json.Number)
	if !ok || value.String() != "1.5" {
		t.Fatalf("value should decode as json.Number, got %#v", latest.Items[0]["value"])
	}
}

func TestIncidentPreservesUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		writeJSON(t, w, map[string]any{
			"id":         "incident-1",
			"message":    "alert",
			"status":     "Open",
			"priority":   "P1",
			"eventCount": 3,
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	incident, err := client.Incident(context.Background(), "incident-1")
	if err != nil {
		t.Fatalf("Incident returned error: %v", err)
	}
	if incident.Message != "alert" || incident.Status != "Open" {
		t.Fatalf("unexpected incident fields: %#v", incident)
	}
	if _, ok := incident.Extra["message"]; ok {
		t.Fatalf("known fields should not be duplicated in Extra: %#v", incident.Extra)
	}
	if incident.Extra["priority"] != "P1" {
		t.Fatalf("priority extra = %#v, want P1", incident.Extra["priority"])
	}
	eventCount, ok := incident.Extra["eventCount"].(json.Number)
	if !ok || eventCount.String() != "3" {
		t.Fatalf("eventCount extra = %#v, want json.Number(3)", incident.Extra["eventCount"])
	}
}

func TestHTTPErrorForUnauthorizedAndNotFound(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized},
		{name: "not_found", statusCode: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "failure", tc.statusCode)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			_, err := client.API(context.Background())
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("expected HTTPError, got %T %v", err, err)
			}
			if httpErr.StatusCode != tc.statusCode {
				t.Fatalf("StatusCode = %d, want %d", httpErr.StatusCode, tc.statusCode)
			}
			if strings.Contains(httpErr.Error(), "secret") || strings.Contains(httpErr.Body, "secret") {
				t.Fatalf("HTTPError leaked credential: %v body=%q", httpErr, httpErr.Body)
			}
			if calls.Load() != 1 {
				t.Fatalf("non-retryable status should be called once, got %d", calls.Load())
			}
			stats := client.SnapshotStats()
			if stats.RequestsTotal != 1 || stats.RequestErrorsTotal != 1 {
				t.Fatalf("unexpected stats: %#v", stats)
			}
		})
	}
}

func TestRetryGetOnTransientStatus(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		if calls.Add(1) < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, map[string]any{"ok": true})
	}))
	defer server.Close()

	client, err := New(Options{
		Endpoint:     server.URL,
		Credentials:  testCredentials(),
		MaxRetries:   2,
		RetryBackoff: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	api, err := client.API(context.Background())
	if err != nil {
		t.Fatalf("API returned error: %v", err)
	}
	if api["ok"] != true {
		t.Fatalf("unexpected API response: %#v", api)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
	stats := client.SnapshotStats()
	if stats.RequestsTotal != 3 || stats.RequestErrorsTotal != 2 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestInsecureTLSCanBeConfigured(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkBasicAuth(t, r)
		writeJSON(t, w, map[string]any{"ok": true})
	}))
	defer server.Close()

	client, err := New(Options{
		Endpoint:              server.URL,
		Credentials:           testCredentials(),
		InsecureSkipTLSVerify: true,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.API(context.Background()); err != nil {
		t.Fatalf("API with insecure TLS returned error: %v", err)
	}
}

func TestLinksDecodeArrayForm(t *testing.T) {
	var links Links
	if err := json.Unmarshal([]byte(`[{"rel":"next","href":"/next"},{"name":"self","href":"/self"}]`), &links); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if links.NextHref() != "/next" || links["self"].Href != "/self" {
		t.Fatalf("unexpected links: %#v", links)
	}
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := New(Options{
		Endpoint:    endpoint,
		Credentials: testCredentials(),
		MaxRetries:  3,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return client
}

func testCredentials() auth.Credentials {
	return auth.Credentials{User: "sysman", Password: "secret"}
}

func checkBasicAuth(t *testing.T, r *http.Request) {
	t.Helper()
	user, password, ok := r.BasicAuth()
	if !ok || user != "sysman" || password != "secret" {
		t.Fatalf("unexpected basic auth user=%q password=%q ok=%v", user, password, ok)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
}
