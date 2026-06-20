package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/scheduler"
	"oem-ingest-new/internal/validate"
)

type runtimeTargetState struct {
	mu sync.RWMutex

	sites             []config.SiteConfig
	refs              map[string]runtimeTargetRef
	entries           map[string]*runtimeTargetEntry
	sourceConfig      string
	validatedConfig   string
	reportPath        string
	recheckInterval   time.Duration
	responseTolerance time.Duration
	responseMonitor   *collect.ResponseMonitor
	clientsByEndpoint map[string]*oem.Client
	logger            Logger
}

type runtimeTargetRef struct {
	siteIndex   int
	targetIndex int
}

type runtimeTargetEntry struct {
	mu          sync.Mutex
	lastChecked time.Time
}

func newRuntimeTargetState(cfg config.Config, env config.Env, monitor *collect.ResponseMonitor, clientsByEndpoint map[string]*oem.Client, logger Logger) *runtimeTargetState {
	state := &runtimeTargetState{
		sites:             cloneRuntimeSites(cfg.Sites),
		refs:              make(map[string]runtimeTargetRef),
		entries:           make(map[string]*runtimeTargetEntry),
		sourceConfig:      env.TargetsPath,
		validatedConfig:   env.ValidatedConfigOutput,
		reportPath:        env.ValidationReportOutput,
		recheckInterval:   env.RuntimeIDRecheckInterval,
		responseTolerance: env.MonitorResponseTolerance,
		responseMonitor:   monitor,
		clientsByEndpoint: clientsByEndpoint,
		logger:            logger,
	}
	for siteIndex, site := range state.sites {
		for targetIndex, target := range site.Targets {
			key := runtimeTargetKey(site.Endpoint, target.Name, target.TypeName)
			if key == "" {
				continue
			}
			state.refs[key] = runtimeTargetRef{siteIndex: siteIndex, targetIndex: targetIndex}
			state.entries[key] = &runtimeTargetEntry{}
		}
	}
	return state
}

func (s *runtimeTargetState) ResolveJob(job scheduler.Job) scheduler.Job {
	if s == nil {
		return job
	}
	key := runtimeTargetKey(job.Endpoint, job.Target.Name, job.Target.TypeName)
	if key == "" {
		return job
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	ref, ok := s.refs[key]
	if !ok || !s.validRef(ref) {
		return job
	}
	job.Target = cloneRuntimeTarget(s.sites[ref.siteIndex].Targets[ref.targetIndex])
	return job
}

func (s *runtimeTargetState) Sites() []config.SiteConfig {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRuntimeSites(s.sites)
}

func (s *runtimeTargetState) RepairTargetID(ctx context.Context, req collect.TargetIDRepairRequest) (collect.TargetIDRepairResult, error) {
	if s == nil || req.Trigger != collect.TargetIDRepairTriggerMetric404 {
		return collect.TargetIDRepairResult{}, nil
	}
	key := runtimeTargetKey(req.Job.Endpoint, req.Job.Target.Name, req.Job.Target.TypeName)
	if key == "" {
		return collect.TargetIDRepairResult{}, nil
	}

	entry := s.entry(key)
	if entry == nil {
		s.info(ctx, "revalidacao de ID ignorada: target nao faz parte da configuracao validada", "target_name", req.Job.Target.Name, "target_type", req.Job.Target.TypeName)
		return collect.TargetIDRepairResult{}, nil
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := req.CheckedAt
	if now.IsZero() {
		now = time.Now()
	}
	job := s.ResolveJob(req.Job)
	currentID := strings.TrimSpace(job.Target.ID)
	if currentID != "" && currentID != strings.TrimSpace(req.Job.Target.ID) {
		return collect.TargetIDRepairResult{Job: job, Corrected: true}, nil
	}
	if s.responseMonitor.Active(currentID, now, s.responseTolerance) {
		s.info(ctx, "revalidacao de ID ignorada: target com response ativo",
			"target_id", currentID,
			"target_name", job.Target.Name,
			"target_type", job.Target.TypeName,
			"metric_group", job.MetricGroupName,
		)
		return collect.TargetIDRepairResult{}, nil
	}
	if !entry.lastChecked.IsZero() && now.Sub(entry.lastChecked) < s.recheckInterval {
		s.info(ctx, "revalidacao de ID ignorada por cooldown",
			"target_id", currentID,
			"target_name", job.Target.Name,
			"target_type", job.Target.TypeName,
			"metric_group", job.MetricGroupName,
			"last_checked", entry.lastChecked.UTC().Format(time.RFC3339Nano),
			"recheck_interval", s.recheckInterval,
		)
		return collect.TargetIDRepairResult{}, nil
	}

	client := s.clientsByEndpoint[job.Endpoint]
	if client == nil {
		return collect.TargetIDRepairResult{}, fmt.Errorf("cliente OEM nao encontrado para endpoint %q", job.Endpoint)
	}
	targets, err := client.ListTargets(ctx)
	if err != nil {
		return collect.TargetIDRepairResult{}, fmt.Errorf("listar targets OEM para revalidar ID de %q tipo %q: %w", job.Target.Name, job.Target.TypeName, err)
	}
	entry.lastChecked = now

	newID, matches := runtimeTargetIDMatch(targets.Items, job.Target.Name, job.Target.TypeName)
	switch {
	case matches == 0:
		s.warn(ctx, "revalidacao de ID sem correcao: target nao encontrado na API OEM",
			"target_id", currentID,
			"target_name", job.Target.Name,
			"target_type", job.Target.TypeName,
			"metric_group", job.MetricGroupName,
		)
		return collect.TargetIDRepairResult{}, nil
	case matches > 1:
		s.warn(ctx, "revalidacao de ID sem correcao: target duplicado na API OEM",
			"target_id", currentID,
			"target_name", job.Target.Name,
			"target_type", job.Target.TypeName,
			"metric_group", job.MetricGroupName,
			"matches", matches,
		)
		return collect.TargetIDRepairResult{}, nil
	case strings.TrimSpace(newID) == "" || strings.TrimSpace(newID) == currentID:
		s.info(ctx, "revalidacao de ID sem correcao: ID permanece atual",
			"target_id", currentID,
			"target_name", job.Target.Name,
			"target_type", job.Target.TypeName,
			"metric_group", job.MetricGroupName,
		)
		return collect.TargetIDRepairResult{}, nil
	}

	correctedJob, err := s.applyRuntimeIDCorrection(ctx, job, currentID, strings.TrimSpace(newID), now)
	if err != nil {
		return collect.TargetIDRepairResult{}, err
	}
	return collect.TargetIDRepairResult{Job: correctedJob, Corrected: true}, nil
}

func (s *runtimeTargetState) entry(key string) *runtimeTargetEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[key]
}

func (s *runtimeTargetState) applyRuntimeIDCorrection(ctx context.Context, job scheduler.Job, oldID, newID string, at time.Time) (scheduler.Job, error) {
	key := runtimeTargetKey(job.Endpoint, job.Target.Name, job.Target.TypeName)
	s.mu.Lock()
	defer s.mu.Unlock()

	ref, ok := s.refs[key]
	if !ok || !s.validRef(ref) {
		return scheduler.Job{}, fmt.Errorf("target %q tipo %q nao encontrado no estado runtime", job.Target.Name, job.Target.TypeName)
	}
	target := &s.sites[ref.siteIndex].Targets[ref.targetIndex]
	if strings.TrimSpace(target.ID) == newID {
		job.Target = cloneRuntimeTarget(*target)
		return job, nil
	}

	previousID := target.ID
	target.ID = newID
	if err := config.WriteTargets(s.validatedConfig, s.sites); err != nil {
		target.ID = previousID
		return scheduler.Job{}, err
	}

	correction := validate.IDCorrection{
		SiteIndex:   ref.siteIndex,
		TargetIndex: ref.targetIndex,
		SiteName:    s.sites[ref.siteIndex].Name,
		TargetName:  target.Name,
		TargetType:  target.TypeName,
		OldID:       oldID,
		NewID:       newID,
	}
	event := validate.NewRuntimeIDCorrectionEvent(s.sourceConfig, s.validatedConfig, at, correction, job.MetricGroupName)
	if err := validate.AppendValidationReportEvent(s.reportPath, event); err != nil {
		s.warn(ctx, "falha ao registrar correcao runtime de ID no relatorio",
			"target_name", target.Name,
			"target_type", target.TypeName,
			"old_id", oldID,
			"new_id", newID,
			"report", s.reportPath,
			"err", err,
		)
	}

	job.Target = cloneRuntimeTarget(*target)
	s.info(ctx, "ID do target corrigido em runtime apos 404",
		"target_name", target.Name,
		"target_type", target.TypeName,
		"old_id", oldID,
		"new_id", newID,
		"metric_group", job.MetricGroupName,
	)
	return job, nil
}

func (s *runtimeTargetState) validRef(ref runtimeTargetRef) bool {
	return ref.siteIndex >= 0 &&
		ref.siteIndex < len(s.sites) &&
		ref.targetIndex >= 0 &&
		ref.targetIndex < len(s.sites[ref.siteIndex].Targets)
}

func runtimeTargetIDMatch(targets []oem.Target, name, typeName string) (string, int) {
	name = strings.TrimSpace(name)
	typeName = strings.TrimSpace(typeName)
	if name == "" || typeName == "" {
		return "", 0
	}
	key := name + "\x00" + typeName
	matches := 0
	currentID := ""
	for _, target := range targets {
		if strings.TrimSpace(target.ID) == "" {
			continue
		}
		if strings.TrimSpace(target.Name)+"\x00"+strings.TrimSpace(target.TypeName) != key {
			continue
		}
		matches++
		currentID = strings.TrimSpace(target.ID)
	}
	return currentID, matches
}

func runtimeTargetKey(endpoint, name, typeName string) string {
	endpoint = strings.TrimSpace(endpoint)
	name = strings.TrimSpace(name)
	typeName = strings.TrimSpace(typeName)
	if endpoint == "" || name == "" || typeName == "" {
		return ""
	}
	return endpoint + "\x00" + name + "\x00" + typeName
}

func cloneRuntimeSites(sites []config.SiteConfig) []config.SiteConfig {
	out := make([]config.SiteConfig, len(sites))
	for siteIndex, site := range sites {
		out[siteIndex] = site
		out[siteIndex].Targets = make([]config.TargetConfig, len(site.Targets))
		for targetIndex, target := range site.Targets {
			out[siteIndex].Targets[targetIndex] = cloneRuntimeTarget(target)
		}
	}
	return out
}

func cloneRuntimeTarget(target config.TargetConfig) config.TargetConfig {
	out := target
	if target.Tags != nil {
		out.Tags = make(map[string]string, len(target.Tags))
		for key, value := range target.Tags {
			out.Tags[key] = value
		}
	}
	return out
}

func (s *runtimeTargetState) info(ctx context.Context, msg string, args ...any) {
	if s != nil && s.logger != nil {
		s.logger.InfoContext(ctx, msg, args...)
	}
}

func (s *runtimeTargetState) warn(ctx context.Context, msg string, args ...any) {
	if s != nil && s.logger != nil {
		s.logger.WarnContext(ctx, msg, args...)
	}
}
