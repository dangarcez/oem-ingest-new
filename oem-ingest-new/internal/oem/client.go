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
	"net/http/cookiejar"
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
	MaxConcurrentRequests int
	Limiter               *ConcurrencyLimiter
}

// Client performs authenticated GET requests against one OEM endpoint.
type Client struct {
	endpoint     *url.URL
	credential   auth.Credentials
	httpClient   *http.Client
	maxRetries   int
	retryBackoff time.Duration
	limiter      *ConcurrencyLimiter
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

// ConcurrencyLimiter bounds simultaneous OEM HTTP requests. A single limiter
// can be shared by several clients to enforce a process-wide request cap.
type ConcurrencyLimiter struct {
	slots chan struct{}
}

// NewConcurrencyLimiter creates a limiter with max simultaneous requests.
func NewConcurrencyLimiter(max int) (*ConcurrencyLimiter, error) {
	if max < 0 {
		return nil, errors.New("MaxConcurrentRequests nao pode ser negativo")
	}
	if max == 0 {
		return nil, nil
	}
	return &ConcurrencyLimiter{slots: make(chan struct{}, max)}, nil
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
	if opts.MaxConcurrentRequests < 0 {
		return nil, errors.New("MaxConcurrentRequests nao pode ser negativo")
	}

	retryBackoff := opts.RetryBackoff
	if retryBackoff == 0 {
		retryBackoff = defaultRetryBackoff
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient(opts)
	}
	limiter := opts.Limiter
	if limiter == nil && opts.MaxConcurrentRequests > 0 {
		limiter, err = NewConcurrencyLimiter(opts.MaxConcurrentRequests)
		if err != nil {
			return nil, err
		}
	}

	return &Client{
		endpoint:     endpoint,
		credential:   opts.Credentials,
		httpClient:   httpClient,
		maxRetries:   opts.MaxRetries,
		retryBackoff: retryBackoff,
		limiter:      limiter,
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
	jar, _ := cookiejar.New(nil)

	return &http.Client{
		Jar:     jar,
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
	seenPages := make(map[string]struct{})
	for nextPath != "" {
		if err := rememberPage(seenPages, c, nextPath); err != nil {
			return Page[T]{}, err
		}
		var page Page[T]
		if err := c.getJSON(ctx, nextPath, &page); err != nil {
			return Page[T]{}, err
		}
		if merged.Links == nil {
			merged = page
		} else {
			merged.Items = append(merged.Items, page.Items...)
		}
		nextPath = nextPagePath(nextPath, page.Links.NextHref())
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
	seenPages := make(map[string]struct{})
	for nextPath != "" {
		if err := rememberPage(seenPages, c, nextPath); err != nil {
			return LatestData{}, err
		}
		var page LatestData
		if err := c.getJSON(ctx, nextPath, &page); err != nil {
			return LatestData{}, err
		}
		if merged.Links == nil {
			merged = page
		} else {
			merged.Items = append(merged.Items, page.Items...)
		}
		nextPath = nextPagePath(nextPath, page.Links.NextHref())
	}
	if merged.Links != nil {
		delete(merged.Links, "next")
	}
	merged.Count = len(merged.Items)
	return merged, nil
}

func rememberPage(seen map[string]struct{}, c *Client, path string) error {
	pageURL, err := c.resolveURL(path)
	if err != nil {
		return err
	}
	if _, ok := seen[pageURL]; ok {
		return fmt.Errorf("paginacao OEM ciclica detectada em %s", pageURL)
	}
	seen[pageURL] = struct{}{}
	return nil
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

		release, err := c.acquireRequestSlot(ctx)
		if err != nil {
			return nil, err
		}
		atomic.AddUint64(&c.stats.requests, 1)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			release()
			atomic.AddUint64(&c.stats.errors, 1)
			lastErr = fmt.Errorf("OEM GET %s falhou: %w", reqURL, err)
			continue
		}
		resp.Body = releaseOnClose{ReadCloser: resp.Body, release: release}

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

func (c *Client) acquireRequestSlot(ctx context.Context) (func(), error) {
	if c.limiter == nil {
		return func() {}, nil
	}
	return c.limiter.acquire(ctx)
}

func (l *ConcurrencyLimiter) acquire(ctx context.Context) (func(), error) {
	if l == nil || l.slots == nil {
		return func() {}, nil
	}
	select {
	case l.slots <- struct{}{}:
		var released atomic.Bool
		return func() {
			if released.CompareAndSwap(false, true) {
				<-l.slots
			}
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type releaseOnClose struct {
	io.ReadCloser
	release func()
}

func (r releaseOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
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
	basePath := strings.TrimRight(resolved.EscapedPath(), "/")
	refPath := "/" + strings.TrimLeft(ref.EscapedPath(), "/")
	escapedPath := basePath + refPath
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", fmt.Errorf("path OEM invalido: %w", err)
	}
	resolved.Path = decodedPath
	resolved.RawPath = escapedPath
	resolved.RawQuery = ref.RawQuery
	resolved.Fragment = ""
	return resolved.String(), nil
}

func shouldRetryStatus(statusCode int) bool {
	_, ok := retryableStatusCodes[statusCode]
	return ok
}

func nextPagePath(currentPath, href string) string {
	if strings.TrimSpace(href) == "" {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	if ref.IsAbs() || ref.Host != "" || ref.Path != "" || ref.RawQuery == "" {
		return href
	}
	current, err := url.Parse(currentPath)
	if err != nil {
		return href
	}
	current.RawQuery = ref.RawQuery
	current.Fragment = ""
	return current.String()
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
