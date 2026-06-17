package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oem-ingest-new/internal/config"
)

func TestBuildJobsCreatesOneJobPerSiteTargetMetricGroup(t *testing.T) {
	cfg := config.Config{
		Sites: []config.SiteConfig{
			{
				Name:     "oraemc",
				Endpoint: "http://oem-a.example",
				Targets: []config.TargetConfig{
					target("host-id", "dbhost01.example", "host"),
					target("db-id", "orcl1", "oracle_database"),
					target("unknown-id", "ignored", "oracle_listener"),
				},
			},
			{
				Name:     "backup",
				Endpoint: "http://oem-b.example",
				Targets: []config.TargetConfig{
					target("host-b-id", "dbhost02.example", "host"),
				},
			},
		},
		Metrics: config.MetricsConfig{
			"host": {
				{Freq: 5, MetricGroupName: "Load"},
				{Freq: 10, MetricGroupName: "Filesystems"},
			},
			"oracle_database": {
				{Freq: 2, MetricGroupName: "Response"},
			},
		},
	}

	jobs, err := BuildJobs(cfg)
	if err != nil {
		t.Fatalf("BuildJobs returned error: %v", err)
	}

	if len(jobs) != 7 {
		t.Fatalf("expected 7 jobs, got %d: %#v", len(jobs), jobs)
	}
	assertJob(t, jobs[0], "http://oem-a.example", "dbhost01.example", "host", "Load", 5*time.Minute)
	assertJob(t, jobs[1], "http://oem-a.example", "dbhost01.example", "host", "Filesystems", 10*time.Minute)
	assertJob(t, jobs[2], "http://oem-a.example", "dbhost01.example", "host", "Response", 3*time.Minute)
	if !jobs[2].MetricGroup.Bodyless {
		t.Fatalf("host Response custom job should be bodyless: %#v", jobs[2])
	}
	assertJob(t, jobs[3], "http://oem-a.example", "orcl1", "oracle_database", "Response", 2*time.Minute)
	assertJob(t, jobs[4], "http://oem-b.example", "dbhost02.example", "host", "Load", 5*time.Minute)
	assertJob(t, jobs[5], "http://oem-b.example", "dbhost02.example", "host", "Filesystems", 10*time.Minute)
	assertJob(t, jobs[6], "http://oem-b.example", "dbhost02.example", "host", "Response", 3*time.Minute)
	if !jobs[6].MetricGroup.Bodyless {
		t.Fatalf("host Response custom job should be bodyless: %#v", jobs[6])
	}
	if jobs[0].ID == "" || jobs[0].ID == jobs[1].ID {
		t.Fatalf("expected stable unique job IDs, got %q and %q", jobs[0].ID, jobs[1].ID)
	}
}

func TestBuildJobsAddsMissingLegacyCustomGroups(t *testing.T) {
	cfg := config.Config{
		Sites: []config.SiteConfig{{
			Name:     "oraemc",
			Endpoint: "http://oem-a.example",
			Targets: []config.TargetConfig{
				target("rac-id", "rac1", "rac_database"),
				target("pdb-id", "pdb1", "oracle_pdb"),
			},
		}},
		Metrics: config.MetricsConfig{
			"rac_database": {{Freq: 15, MetricGroupName: "service_performance"}},
			"oracle_pdb":   {{Freq: 1440, MetricGroupName: "DATABASE_SIZE"}},
		},
	}

	jobs, err := BuildJobs(cfg)
	if err != nil {
		t.Fatalf("BuildJobs returned error: %v", err)
	}

	if len(jobs) != 5 {
		t.Fatalf("expected 5 jobs, got %d: %#v", len(jobs), jobs)
	}
	assertJob(t, jobs[0], "http://oem-a.example", "rac1", "rac_database", "service_performance", 15*time.Minute)
	assertJob(t, jobs[1], "http://oem-a.example", "rac1", "rac_database", "Availability", 3*time.Minute)
	if !jobs[1].MetricGroup.Bodyless {
		t.Fatalf("rac Availability custom job should be bodyless: %#v", jobs[1])
	}
	assertJob(t, jobs[2], "http://oem-a.example", "pdb1", "oracle_pdb", "DATABASE_SIZE", 1440*time.Minute)
	assertJob(t, jobs[3], "http://oem-a.example", "pdb1", "oracle_pdb", "DBService", 10*time.Minute)
	assertJob(t, jobs[4], "http://oem-a.example", "pdb1", "oracle_pdb", "Response", 3*time.Minute)
	if !jobs[4].MetricGroup.Bodyless {
		t.Fatalf("pdb Response custom job should be bodyless: %#v", jobs[4])
	}
}

func TestBuildJobsRejectsInvalidFrequency(t *testing.T) {
	_, err := BuildJobs(config.Config{
		Sites: []config.SiteConfig{{
			Name:     "oraemc",
			Endpoint: "http://oem.example",
			Targets:  []config.TargetConfig{target("host-id", "dbhost01", "host")},
		}},
		Metrics: config.MetricsConfig{
			"host": {{Freq: 0, MetricGroupName: "Load"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "freq deve ser maior") {
		t.Fatalf("expected invalid frequency error, got %v", err)
	}
}

func TestNewUsesDefaultJitterUnlessDisabled(t *testing.T) {
	runner := New(Options{})
	if runner.jitter != DefaultJitter {
		t.Fatalf("expected default jitter %s, got %s", DefaultJitter, runner.jitter)
	}

	runner = New(Options{Jitter: -1})
	if runner.jitter != 0 {
		t.Fatalf("expected disabled jitter, got %s", runner.jitter)
	}
}

func TestRunnerExecutesJobOnScheduleAndStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := &recordingLogger{}
	runner := New(Options{Logger: logger, Jitter: -1})
	job := runtimeJob(20 * time.Millisecond)
	started := make(chan time.Time, 2)
	var calls atomic.Int32

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx, []Job{job}, func(context.Context, Job) error {
			started <- time.Now()
			if calls.Add(1) == 2 {
				cancel()
			}
			return nil
		})
	}()

	first := waitTime(t, started)
	second := waitTime(t, started)
	if second.Sub(first) < 15*time.Millisecond {
		t.Fatalf("expected job interval to respect frequency, got %s", second.Sub(first))
	}
	if err := waitErr(t, done); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !logger.containsInfo("job de coleta registrado", "Load") {
		t.Fatalf("expected job registration log, got %#v", logger.infos)
	}
}

func TestRunnerAppliesInitialJitter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := New(Options{
		JitterFunc: func(Job) time.Duration {
			return 30 * time.Millisecond
		},
	})
	job := runtimeJob(time.Hour)
	start := time.Now()
	elapsed := make(chan time.Duration, 1)
	done := make(chan error, 1)

	go func() {
		done <- runner.Run(ctx, []Job{job}, func(context.Context, Job) error {
			elapsed <- time.Since(start)
			cancel()
			return nil
		})
	}()

	if got := waitDuration(t, elapsed); got < 25*time.Millisecond {
		t.Fatalf("expected initial jitter before first execution, got %s", got)
	}
	if err := waitErr(t, done); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunnerDoesNotOverlapSameJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := &recordingLogger{}
	runner := New(Options{Logger: logger, Jitter: -1})
	job := runtimeJob(5 * time.Millisecond)
	started := make(chan struct{})
	release := make(chan struct{})
	var starts atomic.Int32

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx, []Job{job}, func(context.Context, Job) error {
			if starts.Add(1) == 1 {
				close(started)
			}
			<-release
			return nil
		})
	}()

	waitClosed(t, started)
	time.Sleep(40 * time.Millisecond)
	if got := starts.Load(); got != 1 {
		t.Fatalf("expected no overlapping starts while first execution is active, got %d", got)
	}
	if !logger.containsWarn("execucao ainda ativa", "Load") {
		t.Fatalf("expected skipped-job warning, got %#v", logger.warnings)
	}

	cancel()
	close(release)
	if err := waitErr(t, done); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunnerLogsHandlerFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := &recordingLogger{}
	runner := New(Options{Logger: logger, Jitter: -1})
	job := runtimeJob(time.Hour)
	done := make(chan error, 1)

	go func() {
		done <- runner.Run(ctx, []Job{job}, func(context.Context, Job) error {
			time.AfterFunc(10*time.Millisecond, cancel)
			return errors.New("latestData falhou")
		})
	}()

	if err := waitErr(t, done); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !logger.containsError("falha em job de coleta", "Load") {
		t.Fatalf("expected handler failure log, got %#v", logger.errors)
	}
}

func TestRunnerRejectsInvalidJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := New(Options{})
	job := runtimeJob(time.Minute)
	job.Frequency = 0

	err := runner.Run(ctx, []Job{job}, func(context.Context, Job) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "frequencia invalida") {
		t.Fatalf("expected invalid job error, got %v", err)
	}
}

func assertJob(t *testing.T, job Job, endpoint, targetName, targetType, group string, frequency time.Duration) {
	t.Helper()
	if job.Endpoint != endpoint || job.Target.Name != targetName || job.Target.TypeName != targetType ||
		job.MetricGroupName != group || job.MetricGroup.MetricGroupName != group || job.Frequency != frequency {
		t.Fatalf("unexpected job: %#v", job)
	}
}

func target(id, name, targetType string) config.TargetConfig {
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

func runtimeJob(frequency time.Duration) Job {
	return Job{
		ID:              "job-load",
		SiteName:        "oraemc",
		Endpoint:        "http://oem.example",
		Target:          target("host-id", "dbhost01", "host"),
		MetricGroup:     config.MetricGroupConfig{Freq: 1, MetricGroupName: "Load"},
		MetricGroupName: "Load",
		Frequency:       frequency,
	}
}

type recordingLogger struct {
	mu       sync.Mutex
	infos    []string
	warnings []string
	errors   []string
}

func (r *recordingLogger) InfoContext(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, formatLog(msg, args...))
}

func (r *recordingLogger) WarnContext(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, formatLog(msg, args...))
}

func (r *recordingLogger) ErrorContext(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, formatLog(msg, args...))
}

func (r *recordingLogger) containsInfo(parts ...string) bool {
	return containsAll(r.snapshot(r.infos), parts...)
}

func (r *recordingLogger) containsWarn(parts ...string) bool {
	return containsAll(r.snapshot(r.warnings), parts...)
}

func (r *recordingLogger) containsError(parts ...string) bool {
	return containsAll(r.snapshot(r.errors), parts...)
}

func (r *recordingLogger) snapshot(values []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(values))
	copy(out, values)
	return out
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

func containsAll(logs []string, parts ...string) bool {
	for _, log := range logs {
		matched := true
		for _, part := range parts {
			if !strings.Contains(log, part) {
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

func waitTime(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for time")
		return time.Time{}
	}
}

func waitDuration(t *testing.T, ch <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for duration")
		return 0
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner")
		return nil
	}
}
