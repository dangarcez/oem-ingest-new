// Package oem implements the Oracle Enterprise Manager HTTP client.
package oem

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"oem-ingest-new/internal/auth"
)

const (
	DefaultLatestDataLimit  = 200
	defaultMaxIdleConns     = 50
	defaultIdleConnTimeout  = 90 * time.Second
	defaultRetryBackoff     = 500 * time.Millisecond
	defaultHTTPErrorBodyMax = 4 << 10
)

var retryableStatusCodes = map[int]struct{}{
	http.StatusTooManyRequests:     {},
	http.StatusInternalServerError: {},
	http.StatusBadGateway:          {},
	http.StatusServiceUnavailable:  {},
	http.StatusGatewayTimeout:      {},
}

// Options configures a Client.
type Options struct {
	Endpoint              string
	Credentials           auth.Credentials
	HTTPClient            *http.Client
	Timeout               time.Duration
	ConnectTimeout        time.Duration
	MaxRetries            int
	RetryBackoff          time.Duration
	InsecureSkipTLSVerify bool
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
}

// Client performs authenticated GET requests against one OEM endpoint.
type Client struct {
	endpoint     *url.URL
	credential   auth.Credentials
	httpClient   *http.Client
	maxRetries   int
	retryBackoff time.Duration
	stats        clientStats
}

type clientStats struct {
	requests uint64
	errors   uint64
}

// Stats is a snapshot of OEM HTTP counters maintained by Client.
type Stats struct {
	RequestsTotal      uint64
	RequestErrorsTotal uint64
}

// HTTPError describes a non-2xx OEM response without exposing credentials.
type HTTPError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("OEM %s %s retornou HTTP %d", e.Method, e.URL, e.StatusCode)
}

// New creates an OEM HTTP client.
func New(opts Options) (*Client, error) {
	endpoint, err := parseEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Credentials.User) == "" {
		return nil, errors.New("credencial OEM sem usuario")
	}
	if opts.MaxRetries < 0 {
		return nil, errors.New("MaxRetries nao pode ser negativo")
	}

	retryBackoff := opts.RetryBackoff
	if retryBackoff == 0 {
		retryBackoff = defaultRetryBackoff
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient(opts)
	}

	return &Client{
		endpoint:     endpoint,
		credential:   opts.Credentials,
		httpClient:   httpClient,
		maxRetries:   opts.MaxRetries,
		retryBackoff: retryBackoff,
	}, nil
}

// SnapshotStats returns the current request counters.
func (c *Client) SnapshotStats() Stats {
	return Stats{
		RequestsTotal:      atomic.LoadUint64(&c.stats.requests),
		RequestErrorsTotal: atomic.LoadUint64(&c.stats.errors),
	}
}

// API calls GET /em/api.
func (c *Client) API(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.getJSON(ctx, "/em/api", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListTargets calls GET /em/api/targets and follows links.next.
func (c *Client) ListTargets(ctx context.Context) (Page[Target], error) {
	return getPaged[Target](ctx, c, "/em/api/targets")
}

// TargetProperties calls GET /em/api/targets/{targetId}/properties and follows links.next.
func (c *Client) TargetProperties(ctx context.Context, targetID string) (Page[Property], error) {
	return getPaged[Property](ctx, c, fmt.Sprintf("/em/api/targets/%s/properties", pathSegment(targetID)))
}

// TargetMetricGroups calls GET /em/api/targets/{targetId}/metricGroups and follows links.next.
func (c *Client) TargetMetricGroups(ctx context.Context, targetID string) (Page[MetricGroup], error) {
	return getPaged[MetricGroup](ctx, c, fmt.Sprintf("/em/api/targets/%s/metricGroups", pathSegment(targetID)))
}

// MetricGroup calls GET /em/api/targets/{targetId}/metricGroups/{groupName}.
func (c *Client) MetricGroup(ctx context.Context, targetID, groupName string) (MetricGroup, error) {
	var out MetricGroup
	path := fmt.Sprintf("/em/api/targets/%s/metricGroups/%s", pathSegment(targetID), pathSegment(groupName))
	if err := c.getJSON(ctx, path, &out); err != nil {
		return MetricGroup{}, err
	}
	return out, nil
}

// LatestData calls GET /em/api/targets/{targetId}/metricGroups/{groupName}/latestData?limit=200
// and follows links.next.
func (c *Client) LatestData(ctx context.Context, targetID, groupName string) (LatestData, error) {
	path := fmt.Sprintf(
		"/em/api/targets/%s/metricGroups/%s/latestData?limit=%d",
		pathSegment(targetID),
		pathSegment(groupName),
		DefaultLatestDataLimit,
	)
	return getPagedLatestData(ctx, c, path)
}

// Incidents calls GET /em/api/incidents/?ageInHoursLessThanOrEqualTo={age} and follows links.next.
func (c *Client) Incidents(ctx context.Context, ageInHours int) (Page[Incident], error) {
	if ageInHours <= 0 {
		return Page[Incident]{}, errors.New("ageInHours deve ser maior que zero")
	}
	path := "/em/api/incidents/?ageInHoursLessThanOrEqualTo=" + strconv.Itoa(ageInHours)
	return getPaged[Incident](ctx, c, path)
}

// Incident calls GET /em/api/incidents/{id}.
func (c *Client) Incident(ctx context.Context, id string) (Incident, error) {
	var out Incident
	if err := c.getJSON(ctx, "/em/api/incidents/"+pathSegment(id), &out); err != nil {
		return Incident{}, err
	}
	return out, nil
}

func parseEndpoint(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("Endpoint OEM: campo obrigatorio")
	}
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil {
		return nil, fmt.Errorf("Endpoint OEM invalido: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Endpoint OEM deve usar http ou https")
	}
	if parsed.Host == "" {
		return nil, errors.New("Endpoint OEM deve incluir host")
	}
	return parsed, nil
}

func newHTTPClient(opts Options) *http.Client {
	connectTimeout := opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxIdleConns := opts.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = defaultMaxIdleConns
	}
	maxIdleConnsPerHost := opts.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = defaultMaxIdleConns
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   connectTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        maxIdleConns,
			MaxIdleConnsPerHost: maxIdleConnsPerHost,
			IdleConnTimeout:     defaultIdleConnTimeout,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: opts.InsecureSkipTLSVerify,
			},
		},
	}
}

func getPaged[T any](ctx context.Context, c *Client, firstPath string) (Page[T], error) {
	var merged Page[T]
	nextPath := firstPath
	for nextPath != "" {
		var page Page[T]
		if err := c.getJSON(ctx, nextPath, &page); err != nil {
			return Page[T]{}, err
		}
		if merged.Links == nil {
			merged = page
		} else {
			merged.Items = append(merged.Items, page.Items...)
		}
		nextPath = page.Links.NextHref()
	}
	if merged.Links != nil {
		delete(merged.Links, "next")
	}
	merged.Count = len(merged.Items)
	return merged, nil
}

func getPagedLatestData(ctx context.Context, c *Client, firstPath string) (LatestData, error) {
	var merged LatestData
	nextPath := firstPath
	for nextPath != "" {
		var page LatestData
		if err := c.getJSON(ctx, nextPath, &page); err != nil {
			return LatestData{}, err
		}
		if merged.Links == nil {
			merged = page
		} else {
			merged.Items = append(merged.Items, page.Items...)
		}
		nextPath = page.Links.NextHref()
	}
	if merged.Links != nil {
		delete(merged.Links, "next")
	}
	merged.Count = len(merged.Items)
	return merged, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.doGET(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decodificar resposta OEM GET %s: %w", path, err)
	}
	return nil
}

func (c *Client) doGET(ctx context.Context, path string) (*http.Response, error) {
	attempts := c.maxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, c.retryBackoff*time.Duration(1<<(attempt-1))); err != nil {
				return nil, err
			}
		}

		reqURL, err := c.resolveURL(path)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		if err := c.credential.Apply(req); err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")

		atomic.AddUint64(&c.stats.requests, 1)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			atomic.AddUint64(&c.stats.errors, 1)
			lastErr = fmt.Errorf("OEM GET %s falhou: %w", reqURL, err)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		atomic.AddUint64(&c.stats.errors, 1)
		if shouldRetryStatus(resp.StatusCode) && attempt < attempts-1 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, defaultHTTPErrorBodyMax))
		resp.Body.Close()
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     http.MethodGet,
			URL:        reqURL,
			Body:       string(body),
		}
	}
	return nil, lastErr
}

func (c *Client) resolveURL(pathOrURL string) (string, error) {
	if strings.TrimSpace(pathOrURL) == "" {
		return "", errors.New("path OEM vazio")
	}
	ref, err := url.Parse(pathOrURL)
	if err != nil {
		return "", fmt.Errorf("path OEM invalido: %w", err)
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}

	resolved := *c.endpoint
	basePath := strings.TrimRight(resolved.Path, "/")
	refPath := "/" + strings.TrimLeft(ref.Path, "/")
	resolved.Path = basePath + refPath
	resolved.RawQuery = ref.RawQuery
	resolved.Fragment = ""
	return resolved.String(), nil
}

func shouldRetryStatus(statusCode int) bool {
	_, ok := retryableStatusCodes[statusCode]
	return ok
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pathSegment(value string) string {
	return url.PathEscape(value)
}
