package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"oem-ingest-new/internal/auth"
	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/exporter"
	"oem-ingest-new/internal/incidents"
	"oem-ingest-new/internal/logging"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/scheduler"
	"oem-ingest-new/internal/selfmetrics"
	"oem-ingest-new/internal/transform"
	"oem-ingest-new/internal/validate"
)

// Logger is the process logger surface used by the runtime wiring.
type Logger interface {
	InfoContext(context.Context, string, ...any)
	WarnContext(context.Context, string, ...any)
	ErrorContext(context.Context, string, ...any)
}

// Options holds process-level dependencies for the application entry point.
type Options struct {
	Output                 io.Writer
	LogOutput              io.Writer
	LookupEnv              func(string) (string, bool)
	Logger                 Logger
	TargetInventoryFactory validate.TargetInventoryFactory
}

const runtimeShutdownTimeout = 10 * time.Second

type startupValidationResult struct {
	IDs         validate.IDValidationResult
	Correlation validate.CorrelationValidationResult
	OutputPath  string
	ReportPath  string
	Sites       []config.SiteConfig
}

// Run is the application entry point. Without OTEL_EXPORT_URL it only performs
// explicit startup validation, keeping local metadata commands side-effect free.
// When OTEL_EXPORT_URL is set, it starts the collector/exporter runtime.
func Run(ctx context.Context, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	env, err := config.ReadEnv(lookupEnv(opts.LookupEnv))
	if err != nil {
		return err
	}
	logger, err := loggerFromOptions(env, opts)
	if err != nil {
		return err
	}
	opts.Logger = logger

	var validatedSites []config.SiteConfig
	if env.ValidateConfig {
		result, err := validateStartupTargets(ctx, env, opts)
		if err != nil {
			return err
		}
		validatedSites = result.Sites
		if opts.Output != nil {
			_, err := fmt.Fprintf(
				opts.Output,
				"validacao de configuracao concluida: %d correcoes de ID, %d targets removidos, %d sites removidos, %d targets adicionados, %d tags corrigidas, %d avisos\n",
				len(result.IDs.IDCorrections),
				len(result.IDs.TargetRemovals),
				len(result.IDs.SiteRemovals),
				len(result.Correlation.TargetAdds),
				len(result.Correlation.TagCorrections),
				len(result.IDs.Warnings)+len(result.Correlation.Warnings),
			)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(opts.Output, "configuracao validada escrita em %s\n", result.OutputPath); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(opts.Output, "relatorio de validacao escrito em %s\n", result.ReportPath); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(env.OTELExportURL) != "" {
		cfg, err := loadRuntimeConfig(env, validatedSites, env.ValidateConfig)
		if err != nil {
			return err
		}
		return runCollector(ctx, env, cfg, opts)
	}
	if opts.Output != nil {
		_, err := fmt.Fprintln(opts.Output, "oem-ingest: scaffold inicializado; coleta nao iniciada sem OTEL_EXPORT_URL")
		return err
	}
	return nil
}

func loggerFromOptions(env config.Env, opts Options) (Logger, error) {
	if opts.Logger != nil {
		return opts.Logger, nil
	}
	if opts.LogOutput == nil {
		return nil, nil
	}
	return logging.NewTextLogger(opts.LogOutput, env.LogLevel)
}

func validateStartupTargets(ctx context.Context, env config.Env, opts Options) (startupValidationResult, error) {
	sites, err := config.LoadTargets(env.TargetsPath)
	if err != nil {
		return startupValidationResult{}, err
	}

	factory := opts.TargetInventoryFactory
	if factory == nil {
		factory, err = targetInventoryFactory(env)
		if err != nil {
			return startupValidationResult{}, err
		}
	}

	idResult, err := validate.ValidateTargetIDs(ctx, sites, targetListerFactory(factory), validate.IDValidationOptions{
		Enabled: true,
		Logger:  opts.Logger,
	})
	if err != nil {
		return startupValidationResult{}, err
	}

	correlationResult, err := validate.ValidateTargetCorrelations(ctx, idResult.Sites, factory, validate.CorrelationValidationOptions{
		Enabled: true,
		Logger:  opts.Logger,
	})
	if err != nil {
		return startupValidationResult{}, err
	}

	if err := validateOutputPath(env.TargetsPath, env.ValidatedConfigOutput); err != nil {
		return startupValidationResult{}, err
	}
	if err := validateReportOutputPath(env.TargetsPath, env.ValidatedConfigOutput, env.ValidationReportOutput); err != nil {
		return startupValidationResult{}, err
	}
	if err := config.WriteTargets(env.ValidatedConfigOutput, correlationResult.Sites); err != nil {
		return startupValidationResult{}, err
	}
	reportEvents := validate.NewStartupValidationEvents(env.TargetsPath, env.ValidatedConfigOutput, time.Now(), idResult, correlationResult)
	if err := validate.WriteValidationReport(env.ValidationReportOutput, reportEvents); err != nil {
		return startupValidationResult{}, err
	}
	logValidationSummary(ctx, opts.Logger, env.ValidatedConfigOutput, env.ValidationReportOutput, idResult, correlationResult)

	return startupValidationResult{
		IDs:         idResult,
		Correlation: correlationResult,
		OutputPath:  env.ValidatedConfigOutput,
		ReportPath:  env.ValidationReportOutput,
		Sites:       correlationResult.Sites,
	}, nil
}

func loadRuntimeConfig(env config.Env, validatedSites []config.SiteConfig, validationRan bool) (config.Config, error) {
	if !validationRan {
		return config.Load(env.TargetsPath, env.MetricsPath)
	}
	if len(validatedSites) == 0 {
		return config.Config{}, errors.New("validacao removeu todos os targets; coleta nao iniciada")
	}
	metrics, err := config.LoadMetrics(env.MetricsPath)
	if err != nil {
		return config.Config{}, err
	}
	return config.Config{Sites: validatedSites, Metrics: metrics}, nil
}

type runtimeState struct {
	cfg                  config.Config
	env                  config.Env
	logger               Logger
	responseMonitor      *collect.ResponseMonitor
	warmupMu             sync.RWMutex
	monitorWarmupUntil   time.Time
	runtimeTargets       *runtimeTargetState
	selfMetrics          *selfmetrics.Registry
	metricsExporter      *exporter.MetricsExporter
	logsExporter         *exporter.LogsExporter
	incidentPollers      []*incidents.Poller
	clientsByEndpoint    map[string]*oem.Client
	collectorsByEndpoint map[string]*collect.Collector
}

func runCollector(ctx context.Context, env config.Env, cfg config.Config, opts Options) error {
	runtimeCtx, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()

	state, err := newRuntimeState(runtimeCtx, env, cfg, opts.Logger)
	if err != nil {
		return err
	}

	jobs, err := scheduler.BuildJobs(cfg)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return errors.New("nenhum job de coleta foi criado a partir da configuracao")
	}
	if opts.Output != nil {
		if _, err := fmt.Fprintf(opts.Output, "oem-ingest: coleta iniciada com %d jobs\n", len(jobs)); err != nil {
			return err
		}
	}
	state.logRuntimeStarted(runtimeCtx, len(jobs))

	initialErrCh := make(chan error, 1)
	go func() {
		initialErrCh <- state.runInitialCollections(runtimeCtx, jobs)
	}()
	state.enqueueRuntimeMetrics(time.Now())
	state.flush(runtimeCtx)
	if env.DiagnosticsInterval > 0 {
		state.logDiagnostics(runtimeCtx, len(jobs))
	}

	var schedulerErrCh <-chan error
	startScheduler := func() {
		if schedulerErrCh != nil {
			return
		}
		runner := scheduler.New(schedulerOptions(env, opts.Logger))
		ch := make(chan error, 1)
		schedulerErrCh = ch
		go func() {
			ch <- runner.Run(runtimeCtx, jobs, state.collectAndBuffer)
		}()
	}
	incidentErrCh := state.startIncidentPollers(runtimeCtx)

	ticker := time.NewTicker(env.ExportInterval)
	defer ticker.Stop()
	var monitorWarmupTimer *time.Timer
	var monitorWarmupC <-chan time.Time
	defer func() {
		if monitorWarmupTimer != nil {
			monitorWarmupTimer.Stop()
		}
	}()
	var diagnosticsC <-chan time.Time
	if env.DiagnosticsInterval > 0 {
		diagnosticsTicker := time.NewTicker(env.DiagnosticsInterval)
		defer diagnosticsTicker.Stop()
		diagnosticsC = diagnosticsTicker.C
	}

	for {
		select {
		case <-runtimeCtx.Done():
			schedulerErr := waitRuntimeStop(schedulerErrCh, runtimeShutdownTimeout)
			incidentErr := waitIncidentPollers(incidentErrCh, runtimeShutdownTimeout)
			state.flushWithTimeout(runtimeShutdownTimeout)
			return firstRuntimeError(schedulerErr, incidentErr)
		case err := <-schedulerErrCh:
			if err == nil || isContextDone(err) {
				cancelRuntime()
				_ = waitIncidentPollers(incidentErrCh, runtimeShutdownTimeout)
				state.flushWithTimeout(runtimeShutdownTimeout)
				return nil
			}
			cancelRuntime()
			_ = waitIncidentPollers(incidentErrCh, runtimeShutdownTimeout)
			state.flushWithTimeout(runtimeShutdownTimeout)
			return err
		case err, ok := <-incidentErrCh:
			if !ok {
				incidentErrCh = nil
				continue
			}
			if err == nil || isContextDone(err) {
				continue
			}
			cancelRuntime()
			_ = waitRuntimeStop(schedulerErrCh, runtimeShutdownTimeout)
			state.flushWithTimeout(runtimeShutdownTimeout)
			return err
		case err := <-initialErrCh:
			initialErrCh = nil
			if err != nil && !isContextDone(err) {
				cancelRuntime()
				_ = waitIncidentPollers(incidentErrCh, runtimeShutdownTimeout)
				state.flushWithTimeout(runtimeShutdownTimeout)
				return err
			}
			monitorWarmupTimer = state.finishMonitorStatusWarmupAfterInitial(runtimeCtx, time.Now())
			if monitorWarmupTimer != nil {
				monitorWarmupC = monitorWarmupTimer.C
			}
			startScheduler()
		case <-monitorWarmupC:
			monitorWarmupC = nil
			monitorWarmupTimer = nil
			state.logMonitorStatusWarmupEnded(runtimeCtx, time.Now())
		case <-ticker.C:
			state.enqueueRuntimeMetrics(time.Now())
			state.flush(runtimeCtx)
		case <-diagnosticsC:
			state.logDiagnostics(runtimeCtx, len(jobs))
		}
	}
}

func newRuntimeState(ctx context.Context, env config.Env, cfg config.Config, logger Logger) (*runtimeState, error) {
	credentials, err := auth.Resolve(auth.Options{
		User:          env.User,
		Password:      env.Password,
		Token:         env.Token,
		TokenHashFile: env.AuthTokenHashFile,
	})
	if err != nil {
		return nil, err
	}

	selfMetrics := selfmetrics.NewRegistry()
	exportHTTPClient := &http.Client{Timeout: env.OTELExportTimeout}
	metricsExporter, err := exporter.NewMetricsExporter(env.OTELExportURL, exporter.MetricsExporterOptions{
		HTTPClient: exportHTTPClient,
		Logger:     logger,
		Observer:   selfMetrics,
	})
	if err != nil {
		return nil, err
	}
	logsExporter, err := exporter.NewLogsExporter(env.OTELExportURL, exporter.LogsExporterOptions{
		HTTPClient: exportHTTPClient,
		Logger:     logger,
		Observer:   selfMetrics,
	})
	if err != nil {
		return nil, err
	}
	oemLimiter, err := oem.NewConcurrencyLimiter(env.MaxConcurrentRequests)
	if err != nil {
		return nil, err
	}

	state := &runtimeState{
		cfg:                  cfg,
		env:                  env,
		logger:               logger,
		responseMonitor:      collect.NewResponseMonitor(),
		selfMetrics:          selfMetrics,
		metricsExporter:      metricsExporter,
		logsExporter:         logsExporter,
		clientsByEndpoint:    make(map[string]*oem.Client),
		collectorsByEndpoint: make(map[string]*collect.Collector),
	}
	if env.ValidateConfig {
		state.runtimeTargets = newRuntimeTargetState(cfg, env, state.responseMonitor, state.clientsByEndpoint, logger)
	}

	for _, site := range cfg.Sites {
		if _, exists := state.clientsByEndpoint[site.Endpoint]; exists {
			continue
		}
		client, err := oem.New(oem.Options{
			Endpoint:              site.Endpoint,
			Credentials:           credentials,
			Timeout:               env.HTTPTimeout,
			ConnectTimeout:        env.HTTPConnectTimeout,
			MaxRetries:            env.HTTPMaxRetries,
			InsecureSkipTLSVerify: !env.TLSVerify,
			Limiter:               oemLimiter,
		})
		if err != nil {
			return nil, err
		}
		if _, err := client.API(ctx); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if isAcceptedStartupAPIError(err) {
				if logger != nil {
					logger.InfoContext(ctx, "conexao OEM validada", "endpoint", site.Endpoint, "http_status", http.StatusNotFound)
				}
			} else if !isRecoverableStartupAPIError(err) {
				return nil, fmt.Errorf("validar conexao OEM %q: %w", site.Endpoint, err)
			} else if logger != nil {
				logger.WarnContext(ctx, "falha temporaria ao validar conexao OEM; runtime continuara tentando", "endpoint", site.Endpoint, "err", err)
			}
		} else if logger != nil {
			logger.InfoContext(ctx, "conexao OEM validada", "endpoint", site.Endpoint)
		}
		state.clientsByEndpoint[site.Endpoint] = client
		state.collectorsByEndpoint[site.Endpoint] = collect.NewCollector(client, collect.CollectorOptions{
			Logger:          logger,
			ResponseMonitor: state.responseMonitor,
			IDRepairer:      state.runtimeTargets,
		})
		poller, err := incidents.New(incidents.Options{
			Client: client,
			Logs:   logsExporter,
			Logger: logger,
		})
		if err != nil {
			return nil, err
		}
		state.incidentPollers = append(state.incidentPollers, poller)
	}
	return state, nil
}

func isAcceptedStartupAPIError(err error) bool {
	var httpErr *oem.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func isRecoverableStartupAPIError(err error) bool {
	var httpErr *oem.HTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	switch httpErr.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (s *runtimeState) runInitialCollections(ctx context.Context, jobs []scheduler.Job) error {
	workers := s.env.MaxConcurrentRequests
	if workers <= 0 {
		workers = 1
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if s.logger != nil && ctx.Err() == nil {
		s.logger.InfoContext(ctx, "coleta inicial iniciada", "jobs", len(jobs), "workers", workers)
	}

	var successes atomic.Int64
	var failures atomic.Int64
	jobsCh := make(chan scheduler.Job)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsCh {
				if err := s.collectAndBuffer(ctx, job); err != nil {
					if ctx.Err() != nil {
						return
					}
					failures.Add(1)
					if s.logger != nil {
						s.logger.WarnContext(ctx, "falha na coleta inicial", "job_id", job.ID, "target_name", job.Target.Name, "metric_group", job.MetricGroupName, "err", err)
					}
					continue
				}
				successes.Add(1)
			}
		}()
	}

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			close(jobsCh)
			wg.Wait()
			return ctx.Err()
		case jobsCh <- job:
		}
	}
	close(jobsCh)
	wg.Wait()

	successCount := int(successes.Load())
	failureCount := int(failures.Load())
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if successCount == 0 && failureCount > 0 && s.logger != nil {
		s.logger.WarnContext(ctx, "nenhuma coleta inicial concluiu com sucesso; scheduler continuara tentando", "jobs", len(jobs), "failures", failureCount)
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "coleta inicial concluida", "jobs", len(jobs), "successes", successCount, "failures", failureCount, "workers", workers)
	}
	return nil
}

func (s *runtimeState) monitorStatusWarmupActive(observedAt time.Time) bool {
	if s == nil {
		return false
	}
	s.warmupMu.RLock()
	until := s.monitorWarmupUntil
	s.warmupMu.RUnlock()
	if until.IsZero() {
		return true
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	return observedAt.Before(until)
}

func (s *runtimeState) finishMonitorStatusWarmupAfterInitial(ctx context.Context, completedAt time.Time) *time.Timer {
	if s == nil {
		return nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	until := completedAt.Add(s.env.MonitorStatusWarmup)
	s.setMonitorStatusWarmupUntil(until)
	delay := time.Until(until)
	if delay <= 0 {
		s.logMonitorStatusWarmupEnded(ctx, completedAt)
		return nil
	}
	return time.NewTimer(delay)
}

func (s *runtimeState) setMonitorStatusWarmupUntil(until time.Time) {
	s.warmupMu.Lock()
	defer s.warmupMu.Unlock()
	s.monitorWarmupUntil = until
}

func (s *runtimeState) monitorStatusWarmupUntil() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.warmupMu.RLock()
	defer s.warmupMu.RUnlock()
	return s.monitorWarmupUntil
}

func (s *runtimeState) logMonitorStatusWarmupEnded(ctx context.Context, endedAt time.Time) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.InfoContext(ctx, "warm-up de oem_monitor_stus concluido",
		"ended_at", endedAt,
		"warmup_until", s.monitorStatusWarmupUntil(),
		"warmup_extra", s.env.MonitorStatusWarmup,
	)
}

func (s *runtimeState) pollIncidentsOnce(ctx context.Context) {
	for _, poller := range s.incidentPollers {
		if _, err := poller.PollOnce(ctx); err != nil {
			if s.logger != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "falha ao consultar incidentes OEM", "err", err)
			}
		}
	}
}

func (s *runtimeState) startIncidentPollers(ctx context.Context) <-chan error {
	if len(s.incidentPollers) == 0 {
		return nil
	}

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var once sync.Once
	for _, poller := range s.incidentPollers {
		poller := poller
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := poller.Run(ctx); err != nil && !isContextDone(err) {
				once.Do(func() {
					errCh <- err
				})
			}
		}()
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()
	return errCh
}

func (s *runtimeState) collectAndBuffer(ctx context.Context, job scheduler.Job) error {
	if s.runtimeTargets != nil {
		job = s.runtimeTargets.ResolveJob(job)
	}
	collector := s.collectorsByEndpoint[job.Endpoint]
	if collector == nil {
		return fmt.Errorf("cliente OEM nao encontrado para endpoint %q", job.Endpoint)
	}

	result, err := collector.Collect(ctx, job)
	if err != nil {
		return err
	}

	if !result.Metadata.Bodyless {
		out := transform.FromCollection(result, transform.Options{})
		s.metricsExporter.Add(out.Metrics...)
		s.logsExporter.Add(out.Logs...)
	}

	if point, ok := transform.FromMonitorStatusWithOptions(result, s.responseMonitor, transform.MonitorStatusOptions{
		ResponseTolerance: s.env.MonitorResponseTolerance,
		WarmupActive:      s.monitorStatusWarmupActive(result.CollectedAt),
	}); ok {
		s.metricsExporter.Add(point)
	}
	serviceStatus := transform.FromServiceStatus(result)
	s.metricsExporter.Add(serviceStatus.Metrics...)
	s.logsExporter.Add(serviceStatus.Logs...)

	return nil
}

func (s *runtimeState) enqueueRuntimeMetrics(observedAt time.Time) {
	sites := s.runtimeSites()
	s.metricsExporter.Add(transform.FromResponseMonitor(sites, s.responseMonitor, s.env.MonitorResponseTolerance, observedAt)...)
	s.metricsExporter.Add(selfmetrics.FromSnapshot(selfmetrics.SnapshotInput{
		Sites:             sites,
		ResponseMonitor:   s.responseMonitor,
		ResponseTolerance: s.env.MonitorResponseTolerance,
		OEM:               s.oemStats(),
		Collector:         s.collectorStats(),
		Exporter:          s.selfMetrics.SnapshotStats(),
		ObservedAt:        observedAt,
	})...)
}

func (s *runtimeState) runtimeSites() []config.SiteConfig {
	if s != nil && s.runtimeTargets != nil {
		return s.runtimeTargets.Sites()
	}
	return s.cfg.Sites
}

func (s *runtimeState) flush(ctx context.Context) {
	if result, err := s.metricsExporter.Export(ctx); err != nil && s.logger != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "falha ao exportar metricas pendentes",
			"endpoint", s.metricsExporter.Endpoint(),
			"datapoints", result.Datapoints,
			"payload_bytes", result.PayloadBytes,
			"pending_metrics", s.metricsExporter.Pending(),
			"err", err,
		)
	}
	if result, err := s.logsExporter.Export(ctx); err != nil && s.logger != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "falha ao exportar logs pendentes",
			"endpoint", s.logsExporter.Endpoint(),
			"logs", result.Logs,
			"payload_bytes", result.PayloadBytes,
			"pending_logs", s.logsExporter.Pending(),
			"err", err,
		)
	}
}

func (s *runtimeState) logRuntimeStarted(ctx context.Context, jobs int) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.InfoContext(ctx, "runtime de coleta iniciado",
		"jobs", jobs,
		"sites", len(s.cfg.Sites),
		"oem_endpoints", len(s.clientsByEndpoint),
		"otel_metrics_endpoint", s.metricsExporter.Endpoint(),
		"otel_logs_endpoint", s.logsExporter.Endpoint(),
		"export_interval", s.env.ExportInterval,
		"otel_export_timeout", s.env.OTELExportTimeout,
		"scheduler_jitter", s.env.SchedulerJitter,
		"diagnostics_interval", s.env.DiagnosticsInterval,
	)
}

func (s *runtimeState) logDiagnostics(ctx context.Context, jobs int) {
	if s == nil || s.logger == nil || ctx.Err() != nil {
		return
	}
	oemStats := s.oemStats()
	collectorStats := s.collectorStats()
	exporterStats := s.selfMetrics.SnapshotStats()
	s.logger.InfoContext(ctx, "diagnostico runtime",
		"jobs", jobs,
		"sites", len(s.cfg.Sites),
		"oem_endpoints", len(s.clientsByEndpoint),
		"pending_metrics", s.metricsExporter.Pending(),
		"pending_logs", s.logsExporter.Pending(),
		"oem_requests_total", oemStats.RequestsTotal,
		"oem_request_errors_total", oemStats.RequestErrorsTotal,
		"datapoints_collected_total", collectorStats.DatapointsCollectedTotal,
		"collection_errors_total", collectorStats.CollectionErrorsTotal,
		"unavailable_groups_total", collectorStats.UnavailableGroupsTotal,
		"datapoints_exported_total", exporterStats.DatapointsExportedTotal,
		"logs_exported_total", exporterStats.LogsExportedTotal,
		"export_failures_total", exporterStats.ExportFailuresTotal,
		"last_export_payload_bytes", exporterStats.ExportPayloadBytes,
		"last_export_duration_seconds", exporterStats.ExportDurationSeconds,
	)
}

func (s *runtimeState) flushWithTimeout(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.flush(ctx)
}

func (s *runtimeState) oemStats() oem.Stats {
	var stats oem.Stats
	for _, client := range s.clientsByEndpoint {
		snapshot := client.SnapshotStats()
		stats.RequestsTotal += snapshot.RequestsTotal
		stats.RequestErrorsTotal += snapshot.RequestErrorsTotal
	}
	return stats
}

func (s *runtimeState) collectorStats() collect.Stats {
	var stats collect.Stats
	for _, collector := range s.collectorsByEndpoint {
		snapshot := collector.SnapshotStats()
		stats.DatapointsCollectedTotal += snapshot.DatapointsCollectedTotal
		stats.CollectionErrorsTotal += snapshot.CollectionErrorsTotal
		stats.UnavailableGroupsTotal += snapshot.UnavailableGroupsTotal
	}
	return stats
}

func isContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func waitRuntimeStop(errCh <-chan error, timeout time.Duration) error {
	if errCh == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-errCh:
		if err == nil || isContextDone(err) {
			return nil
		}
		return err
	case <-timer.C:
		return errors.New("scheduler nao encerrou dentro do timeout")
	}
}

func waitIncidentPollers(errCh <-chan error, timeout time.Duration) error {
	if errCh == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case err, ok := <-errCh:
			if !ok {
				return nil
			}
			if err != nil && !isContextDone(err) {
				return err
			}
		case <-timer.C:
			return errors.New("polling de incidentes nao encerrou dentro do timeout")
		}
	}
}

func firstRuntimeError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func targetInventoryFactory(env config.Env) (validate.TargetInventoryFactory, error) {
	credentials, err := auth.Resolve(auth.Options{
		User:          env.User,
		Password:      env.Password,
		Token:         env.Token,
		TokenHashFile: env.AuthTokenHashFile,
	})
	if err != nil {
		return nil, err
	}
	oemLimiter, err := oem.NewConcurrencyLimiter(env.MaxConcurrentRequests)
	if err != nil {
		return nil, err
	}

	return func(site config.SiteConfig) (validate.TargetInventory, error) {
		return oem.New(oem.Options{
			Endpoint:              site.Endpoint,
			Credentials:           credentials,
			Timeout:               env.HTTPTimeout,
			ConnectTimeout:        env.HTTPConnectTimeout,
			MaxRetries:            env.HTTPMaxRetries,
			InsecureSkipTLSVerify: !env.TLSVerify,
			Limiter:               oemLimiter,
		})
	}, nil
}

func targetListerFactory(factory validate.TargetInventoryFactory) validate.TargetListerFactory {
	return func(site config.SiteConfig) (validate.TargetLister, error) {
		return factory(site)
	}
}

func schedulerOptions(env config.Env, logger Logger) scheduler.Options {
	jitter := env.SchedulerJitter
	if jitter == 0 {
		jitter = -1
	}
	return scheduler.Options{Logger: logger, Jitter: jitter}
}

func lookupEnv(lookup func(string) (string, bool)) func(string) (string, bool) {
	if lookup != nil {
		return lookup
	}
	return os.LookupEnv
}

func validateOutputPath(targetsPath, outputPath string) error {
	targetsAbs, err := filepath.Abs(targetsPath)
	if err != nil {
		return fmt.Errorf("resolver OEM_CONFIG_TARGETS %q: %w", targetsPath, err)
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolver OEM_VALIDATED_CONFIG_OUTPUT %q: %w", outputPath, err)
	}
	if filepath.Clean(targetsAbs) == filepath.Clean(outputAbs) {
		return fmt.Errorf("OEM_VALIDATED_CONFIG_OUTPUT %q deve ser diferente de OEM_CONFIG_TARGETS %q para preservar o arquivo original", outputPath, targetsPath)
	}
	targetsInfo, err := os.Stat(targetsAbs)
	if err != nil {
		return fmt.Errorf("verificar OEM_CONFIG_TARGETS %q: %w", targetsPath, err)
	}
	outputInfo, err := os.Stat(outputAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("verificar OEM_VALIDATED_CONFIG_OUTPUT %q: %w", outputPath, err)
	}
	if os.SameFile(targetsInfo, outputInfo) {
		return fmt.Errorf("OEM_VALIDATED_CONFIG_OUTPUT %q aponta para o mesmo arquivo de OEM_CONFIG_TARGETS %q; use outro caminho para preservar o arquivo original", outputPath, targetsPath)
	}
	return nil
}

func validateReportOutputPath(targetsPath, validatedPath, reportPath string) error {
	if strings.TrimSpace(reportPath) == "" {
		return errors.New("OEM_VALIDATION_REPORT_OUTPUT: caminho do relatorio de validacao nao informado")
	}
	if samePathReference(reportPath, targetsPath) {
		return fmt.Errorf("OEM_VALIDATION_REPORT_OUTPUT %q deve ser diferente de OEM_CONFIG_TARGETS %q para preservar o arquivo original", reportPath, targetsPath)
	}
	if samePathReference(reportPath, validatedPath) {
		return fmt.Errorf("OEM_VALIDATION_REPORT_OUTPUT %q deve ser diferente de OEM_VALIDATED_CONFIG_OUTPUT %q", reportPath, validatedPath)
	}
	return nil
}

func samePathReference(candidatePath, targetPath string) bool {
	candidateAbs, err := filepath.Abs(candidatePath)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}
	candidateAbs = filepath.Clean(candidateAbs)
	targetAbs = filepath.Clean(targetAbs)
	if candidateAbs == targetAbs {
		return true
	}

	candidateInfo, candidateErr := os.Stat(candidateAbs)
	targetInfo, targetErr := os.Stat(targetAbs)
	if candidateErr == nil && targetErr == nil && os.SameFile(candidateInfo, targetInfo) {
		return true
	}

	linkTarget, err := os.Readlink(candidateAbs)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(candidateAbs), linkTarget)
	}
	linkAbs, err := filepath.Abs(linkTarget)
	if err != nil {
		return false
	}
	return filepath.Clean(linkAbs) == targetAbs
}

type infoLogger interface {
	InfoContext(ctx context.Context, msg string, args ...any)
}

func logValidationSummary(ctx context.Context, logger validate.Logger, outputPath, reportPath string, ids validate.IDValidationResult, correlation validate.CorrelationValidationResult) {
	if logger == nil {
		return
	}
	info, ok := logger.(infoLogger)
	if !ok {
		return
	}
	info.InfoContext(ctx, "configuracao validada escrita",
		"output", outputPath,
		"report", reportPath,
		"id_corrections", len(ids.IDCorrections),
		"targets_removed", len(ids.TargetRemovals),
		"sites_removed", len(ids.SiteRemovals),
		"targets_added", len(correlation.TargetAdds),
		"tag_corrections", len(correlation.TagCorrections),
		"warnings", len(ids.Warnings)+len(correlation.Warnings),
	)
}
