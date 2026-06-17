package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"oem-ingest-new/internal/config"
)

const DefaultJitter = 60 * time.Second

// Job identifies one periodic collection task for a site, target and metric
// group. Actual collection is injected through HandlerFunc by later layers.
type Job struct {
	ID              string
	SiteName        string
	Site            string
	Endpoint        string
	Target          config.TargetConfig
	MetricGroup     config.MetricGroupConfig
	Frequency       time.Duration
	MetricGroupName string
}

// HandlerFunc is called whenever a collection job is due.
type HandlerFunc func(context.Context, Job) error

// Logger is intentionally compatible with slog.Logger.
type Logger interface {
	InfoContext(context.Context, string, ...any)
	WarnContext(context.Context, string, ...any)
	ErrorContext(context.Context, string, ...any)
}

// JitterFunc returns the delay added before a job execution. Tests can inject a
// deterministic function; production uses a random value up to Options.Jitter.
type JitterFunc func(Job) time.Duration

// Options configures Runner. Jitter defaults to DefaultJitter; set it to a
// negative duration to disable random delays for deterministic runs.
type Options struct {
	Logger     Logger
	Jitter     time.Duration
	JitterFunc JitterFunc
}

// Runner coordinates periodic collection jobs.
type Runner struct {
	logger     Logger
	jitter     time.Duration
	jitterFunc JitterFunc
}

// New creates a scheduler runner.
func New(opts Options) *Runner {
	jitter := DefaultJitter
	if opts.Jitter != 0 {
		jitter = opts.Jitter
	}
	if opts.Jitter < 0 {
		jitter = 0
	}
	return &Runner{
		logger:     opts.Logger,
		jitter:     jitter,
		jitterFunc: opts.JitterFunc,
	}
}

// ContextWithSignals returns a context canceled by SIGINT or SIGTERM.
func ContextWithSignals(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// BuildJobs creates one job for every configured site + target + metric group.
func BuildJobs(cfg config.Config) ([]Job, error) {
	var jobs []Job
	for siteIndex, site := range cfg.Sites {
		for targetIndex, target := range site.Targets {
			groups := groupsForTarget(target.TypeName, cfg.Metrics[target.TypeName])
			if len(groups) == 0 {
				continue
			}
			for groupIndex, group := range groups {
				if group.Freq <= 0 {
					return nil, fmt.Errorf("target %q grupo %q: freq deve ser maior que zero minutos", target.Name, group.MetricGroupName)
				}
				if strings.TrimSpace(group.MetricGroupName) == "" {
					return nil, fmt.Errorf("target %q: metric_group_name obrigatorio", target.Name)
				}
				jobs = append(jobs, Job{
					ID:              buildJobID(siteIndex, targetIndex, groupIndex, site, target, group),
					SiteName:        site.Name,
					Site:            site.Site,
					Endpoint:        site.Endpoint,
					Target:          target,
					MetricGroup:     group,
					Frequency:       time.Duration(group.Freq) * time.Minute,
					MetricGroupName: group.MetricGroupName,
				})
			}
		}
	}
	return jobs, nil
}

func groupsForTarget(targetType string, configured []config.MetricGroupConfig) []config.MetricGroupConfig {
	if len(configured) == 0 {
		return nil
	}

	groups := append([]config.MetricGroupConfig(nil), configured...)
	for _, required := range legacyCustomGroups(targetType) {
		if hasMetricGroup(groups, required.MetricGroupName) {
			continue
		}
		groups = append(groups, required)
	}
	return groups
}

func legacyCustomGroups(targetType string) []config.MetricGroupConfig {
	switch strings.TrimSpace(targetType) {
	case "rac_database":
		return []config.MetricGroupConfig{
			{Freq: 10, MetricGroupName: "service_performance"},
			{Freq: 3, MetricGroupName: "Availability", Bodyless: true},
		}
	case "oracle_database":
		return []config.MetricGroupConfig{
			{Freq: 3, MetricGroupName: "Response", Bodyless: true},
		}
	case "oracle_pdb":
		return []config.MetricGroupConfig{
			{Freq: 10, MetricGroupName: "DBService"},
			{Freq: 3, MetricGroupName: "Response", Bodyless: true},
		}
	case "host":
		return []config.MetricGroupConfig{
			{Freq: 3, MetricGroupName: "Response", Bodyless: true},
		}
	default:
		return nil
	}
}

func hasMetricGroup(groups []config.MetricGroupConfig, groupName string) bool {
	for _, group := range groups {
		if strings.EqualFold(strings.TrimSpace(group.MetricGroupName), groupName) {
			return true
		}
	}
	return false
}

// Run starts all jobs and blocks until ctx is canceled. It waits for already
// running handlers to return, passing the canceled context to them.
func (r *Runner) Run(ctx context.Context, jobs []Job, handler HandlerFunc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if handler == nil {
		return errors.New("scheduler: HandlerFunc obrigatorio")
	}

	var wg sync.WaitGroup
	for _, job := range jobs {
		if err := validateJob(job); err != nil {
			return err
		}
		r.logJobRegistered(ctx, job)
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runJob(ctx, job, handler)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

func (r *Runner) runJob(ctx context.Context, job Job, handler HandlerFunc) {
	var running atomic.Bool
	var active sync.WaitGroup
	defer active.Wait()

	delay := r.nextJitter(job)
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if !running.CompareAndSwap(false, true) {
				r.warn(ctx, "job de coleta ignorado por execucao ainda ativa", job, nil)
			} else {
				active.Add(1)
				go func() {
					defer active.Done()
					defer running.Store(false)
					if err := handler(ctx, job); err != nil && ctx.Err() == nil {
						r.error(ctx, "falha em job de coleta", job, err)
					}
				}()
			}
			timer.Reset(job.Frequency + r.nextJitter(job))
		}
	}
}

func (r *Runner) nextJitter(job Job) time.Duration {
	if r.jitterFunc != nil {
		jitter := r.jitterFunc(job)
		if jitter < 0 {
			return 0
		}
		return jitter
	}
	if r.jitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(r.jitter) + 1))
}

func validateJob(job Job) error {
	if strings.TrimSpace(job.ID) == "" {
		return errors.New("scheduler: job sem ID")
	}
	if strings.TrimSpace(job.Endpoint) == "" {
		return fmt.Errorf("scheduler: job %q sem endpoint", job.ID)
	}
	if strings.TrimSpace(job.Target.ID) == "" {
		return fmt.Errorf("scheduler: job %q sem target ID", job.ID)
	}
	if strings.TrimSpace(job.Target.Name) == "" {
		return fmt.Errorf("scheduler: job %q sem target name", job.ID)
	}
	if strings.TrimSpace(job.Target.TypeName) == "" {
		return fmt.Errorf("scheduler: job %q sem target type", job.ID)
	}
	if strings.TrimSpace(job.MetricGroupName) == "" {
		return fmt.Errorf("scheduler: job %q sem grupo de metrica", job.ID)
	}
	if job.Frequency <= 0 {
		return fmt.Errorf("scheduler: job %q com frequencia invalida", job.ID)
	}
	return nil
}

func (r *Runner) logJobRegistered(ctx context.Context, job Job) {
	if r.logger == nil {
		return
	}
	r.logger.InfoContext(ctx, "job de coleta registrado", jobLogAttrs(job, nil)...)
}

func (r *Runner) warn(ctx context.Context, msg string, job Job, err error) {
	if r.logger == nil {
		return
	}
	r.logger.WarnContext(ctx, msg, jobLogAttrs(job, err)...)
}

func (r *Runner) error(ctx context.Context, msg string, job Job, err error) {
	if r.logger == nil {
		return
	}
	r.logger.ErrorContext(ctx, msg, jobLogAttrs(job, err)...)
}

func jobLogAttrs(job Job, err error) []any {
	attrs := []any{
		"job_id", job.ID,
		"site", job.SiteName,
		"endpoint", job.Endpoint,
		"target_name", job.Target.Name,
		"target_type", job.Target.TypeName,
		"target_id", job.Target.ID,
		"metric_group", job.MetricGroupName,
		"frequency", job.Frequency.String(),
	}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	return attrs
}

func buildJobID(siteIndex, targetIndex, groupIndex int, site config.SiteConfig, target config.TargetConfig, group config.MetricGroupConfig) string {
	sitePart := site.Name
	if sitePart == "" {
		sitePart = site.Endpoint
	}
	parts := []string{
		fmt.Sprintf("site-%d", siteIndex),
		slug(sitePart),
		fmt.Sprintf("target-%d", targetIndex),
		slug(target.Name),
		slug(target.TypeName),
		fmt.Sprintf("group-%d", groupIndex),
		slug(group.MetricGroupName),
	}
	return strings.Join(nonEmpty(parts), "-")
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
