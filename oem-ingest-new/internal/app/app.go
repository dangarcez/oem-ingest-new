package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"oem-ingest-new/internal/auth"
	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/exporter"
	"oem-ingest-new/internal/incidents"
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
	LookupEnv              func(string) (string, bool)
	Logger                 Logger
	TargetInventoryFactory validate.TargetInventoryFactory
}

const runtimeShutdownTimeout = 10 * time.Second

type startupValidationResult struct {
	IDs         validate.IDValidationResult
	Correlation validate.CorrelationValidationResult
	OutputPath  string
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
				"validacao de configuracao concluida: %d correcoes de ID, %d targets adicionados, %d tags corrigidas, %d avisos\n",
				len(result.IDs.IDCorrections),
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
		}
	}
	if strings.TrimSpace(env.OTELExportURL) != "" {
		cfg, err := loadRuntimeConfig(env, validatedSites)
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
	if err := config.WriteTargets(env.ValidatedConfigOutput, correlationResult.Sites); err != nil {
		return startupValidationResult{}, err
	}
	logValidationSummary(ctx, opts.Logger, env.ValidatedConfigOutput, idResult, correlationResult)

	return startupValidationResult{IDs: idResult, Correlation: correlationResult, OutputPath: env.ValidatedConfigOutput, Sites: correlationResult.Sites}, nil
}

func loadRuntimeConfig(env config.Env, validatedSites []config.SiteConfig) (config.Config, error) {
	if len(validatedSites) == 0 {
		return config.Load(env.TargetsPath, env.MetricsPath)
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

	if err := state.runInitialCollections(runtimeCtx, jobs); err != nil {
		return err
	}
	state.pollIncidentsOnce(runtimeCtx)
	state.enqueueRuntimeMetrics(time.Now())
	state.flush(runtimeCtx)

	runner := scheduler.New(scheduler.Options{Logger: opts.Logger})
	schedulerErrCh := make(chan error, 1)
	go func() {
		schedulerErrCh <- runner.Run(runtimeCtx, jobs, state.collectAndBuffer)
	}()
	incidentErrCh := state.startIncidentPollers(runtimeCtx)

	ticker := time.NewTicker(env.ExportInterval)
	defer ticker.Stop()

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
		case <-ticker.C:
			state.enqueueRuntimeMetrics(time.Now())
			state.flush(runtimeCtx)
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
	metricsExporter, err := exporter.NewMetricsExporter(env.OTELExportURL, exporter.MetricsExporterOptions{
		Logger:   logger,
		Observer: selfMetrics,
	})
	if err != nil {
		return nil, err
	}
	logsExporter, err := exporter.NewLogsExporter(env.OTELExportURL, exporter.LogsExporterOptions{
		Logger:   logger,
		Observer: selfMetrics,
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

	for _, site := range cfg.Sites {
		if _, exists := state.clientsByEndpoint[site.Endpoint]; exists {
			continue
		}
		client, err := oem.New(oem.Options{
			Endpoint:       site.Endpoint,
			Credentials:    credentials,
			Timeout:        env.HTTPTimeout,
			ConnectTimeout: env.HTTPConnectTimeout,
			MaxRetries:     env.HTTPMaxRetries,
			Limiter:        oemLimiter,
		})
		if err != nil {
			return nil, err
		}
		if _, err := client.API(ctx); err != nil {
			return nil, fmt.Errorf("validar conexao OEM %q: %w", site.Endpoint, err)
		}
		state.clientsByEndpoint[site.Endpoint] = client
		state.collectorsByEndpoint[site.Endpoint] = collect.NewCollector(client, collect.CollectorOptions{
			Logger:          logger,
			ResponseMonitor: state.responseMonitor,
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
		if logger != nil {
			logger.InfoContext(ctx, "conexao OEM validada", "endpoint", site.Endpoint)
		}
	}
	return state, nil
}

func (s *runtimeState) runInitialCollections(ctx context.Context, jobs []scheduler.Job) error {
	var successes int
	var failures int
	for _, job := range jobs {
		if err := s.collectAndBuffer(ctx, job); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			failures++
			if s.logger != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "falha na coleta inicial", "job_id", job.ID, "target_name", job.Target.Name, "metric_group", job.MetricGroupName, "err", err)
			}
			continue
		}
		successes++
	}
	if successes == 0 && failures > 0 && s.logger != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "nenhuma coleta inicial concluiu com sucesso; scheduler continuara tentando", "jobs", len(jobs), "failures", failures)
	}
	return nil
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
	collector := s.collectorsByEndpoint[job.Endpoint]
	if collector == nil {
		return fmt.Errorf("cliente OEM nao encontrado para endpoint %q", job.Endpoint)
	}

	result, err := collector.Collect(ctx, job)
	if err != nil {
		return err
	}

	out := transform.FromCollection(result, transform.Options{})
	s.metricsExporter.Add(out.Metrics...)
	s.logsExporter.Add(out.Logs...)

	if point, ok := transform.FromMonitorStatus(result, s.responseMonitor, s.env.MonitorResponseTolerance); ok {
		s.metricsExporter.Add(point)
	}
	serviceStatus := transform.FromServiceStatus(result)
	s.metricsExporter.Add(serviceStatus.Metrics...)
	s.logsExporter.Add(serviceStatus.Logs...)

	return nil
}

func (s *runtimeState) enqueueRuntimeMetrics(observedAt time.Time) {
	s.metricsExporter.Add(transform.FromResponseMonitor(s.cfg.Sites, s.responseMonitor, s.env.MonitorResponseTolerance, observedAt)...)
	s.metricsExporter.Add(selfmetrics.FromSnapshot(selfmetrics.SnapshotInput{
		Sites:             s.cfg.Sites,
		ResponseMonitor:   s.responseMonitor,
		ResponseTolerance: s.env.MonitorResponseTolerance,
		OEM:               s.oemStats(),
		Collector:         s.collectorStats(),
		Exporter:          s.selfMetrics.SnapshotStats(),
		ObservedAt:        observedAt,
	})...)
}

func (s *runtimeState) flush(ctx context.Context) {
	if _, err := s.metricsExporter.Export(ctx); err != nil && s.logger != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "falha ao exportar metricas pendentes", "err", err)
	}
	if _, err := s.logsExporter.Export(ctx); err != nil && s.logger != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "falha ao exportar logs pendentes", "err", err)
	}
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
			Endpoint:       site.Endpoint,
			Credentials:    credentials,
			Timeout:        env.HTTPTimeout,
			ConnectTimeout: env.HTTPConnectTimeout,
			MaxRetries:     env.HTTPMaxRetries,
			Limiter:        oemLimiter,
		})
	}, nil
}

func targetListerFactory(factory validate.TargetInventoryFactory) validate.TargetListerFactory {
	return func(site config.SiteConfig) (validate.TargetLister, error) {
		return factory(site)
	}
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

type infoLogger interface {
	InfoContext(ctx context.Context, msg string, args ...any)
}

func logValidationSummary(ctx context.Context, logger validate.Logger, outputPath string, ids validate.IDValidationResult, correlation validate.CorrelationValidationResult) {
	if logger == nil {
		return
	}
	info, ok := logger.(infoLogger)
	if !ok {
		return
	}
	info.InfoContext(ctx, "configuracao validada escrita",
		"output", outputPath,
		"id_corrections", len(ids.IDCorrections),
		"targets_added", len(correlation.TargetAdds),
		"tag_corrections", len(correlation.TagCorrections),
		"warnings", len(ids.Warnings)+len(correlation.Warnings),
	)
}
