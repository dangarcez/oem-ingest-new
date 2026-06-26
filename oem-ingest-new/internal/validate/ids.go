package validate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
)

// Logger is the subset of slog.Logger used by validation. It keeps this
// package easy to test and avoids forcing a concrete logging implementation.
type Logger interface {
	WarnContext(ctx context.Context, msg string, args ...any)
}

// InfoLogger is the optional INFO-level logger surface used for progress logs.
// Callers can omit it, or pass a warn-only Logger, without changing validation
// behavior.
type InfoLogger interface {
	InfoContext(ctx context.Context, msg string, args ...any)
}

// TargetLister is implemented by the OEM client endpoint used in this phase.
type TargetLister interface {
	ListTargets(ctx context.Context) (oem.Page[oem.Target], error)
}

// TargetListerFactory returns the OEM client for one configured site.
type TargetListerFactory func(site config.SiteConfig) (TargetLister, error)

// IDValidationOptions controls the optional ID validation step.
type IDValidationOptions struct {
	Enabled    bool
	Logger     Logger
	InfoLogger InfoLogger
}

// IDValidationResult contains the corrected in-memory target configuration and
// a machine-readable summary of warnings and corrections.
type IDValidationResult struct {
	Sites          []config.SiteConfig
	IDCorrections  []IDCorrection
	TargetRemovals []TargetRemoval
	SiteRemovals   []SiteRemoval
	Warnings       []Warning
}

// Changed reports whether validation updated target IDs or removed targets.
func (r IDValidationResult) Changed() bool {
	return len(r.IDCorrections) > 0 || len(r.TargetRemovals) > 0 || len(r.SiteRemovals) > 0
}

// IDCorrection describes one target ID changed to match the current OEM API.
type IDCorrection struct {
	SiteIndex   int    `yaml:"siteIndex"`
	TargetIndex int    `yaml:"targetIndex"`
	SiteName    string `yaml:"siteName"`
	TargetName  string `yaml:"targetName"`
	TargetType  string `yaml:"targetType"`
	OldID       string `yaml:"oldID"`
	NewID       string `yaml:"newID"`
}

// TargetRemoval describes one configured target excluded because it was not
// found in the current OEM API target list.
type TargetRemoval struct {
	SiteIndex   int         `yaml:"siteIndex"`
	TargetIndex int         `yaml:"targetIndex"`
	SiteName    string      `yaml:"siteName"`
	TargetName  string      `yaml:"targetName"`
	TargetType  string      `yaml:"targetType"`
	ConfigID    string      `yaml:"configID"`
	Reason      WarningCode `yaml:"reason"`
}

// SiteRemoval describes one site omitted from the validated configuration
// because all its configured targets were removed.
type SiteRemoval struct {
	SiteIndex      int    `yaml:"siteIndex"`
	SiteName       string `yaml:"siteName"`
	Endpoint       string `yaml:"endpoint"`
	RemovedTargets int    `yaml:"removedTargets"`
}

// WarningCode identifies a validation warning class.
type WarningCode string

const (
	WarningTargetMissing   WarningCode = "target_missing"
	WarningTargetDuplicate WarningCode = "target_duplicate"
	WarningIDDivergent     WarningCode = "id_divergent"
)

// Warning is emitted when validation finds a non-fatal configuration issue.
type Warning struct {
	Code        WarningCode `yaml:"code"`
	SiteIndex   int         `yaml:"siteIndex"`
	TargetIndex int         `yaml:"targetIndex"`
	SiteName    string      `yaml:"siteName"`
	TargetName  string      `yaml:"targetName"`
	TargetType  string      `yaml:"targetType"`
	ConfigID    string      `yaml:"configID,omitempty"`
	CurrentID   string      `yaml:"currentID,omitempty"`
	Count       int         `yaml:"count,omitempty"`
	Message     string      `yaml:"message"`
}

// ValidateTargetIDs checks configured target IDs against the current OEM target
// list for each site. It never writes files and never mutates the input slice.
func ValidateTargetIDs(ctx context.Context, sites []config.SiteConfig, factory TargetListerFactory, opts IDValidationOptions) (IDValidationResult, error) {
	corrected := cloneSites(sites)
	result := IDValidationResult{Sites: corrected}
	if !opts.Enabled {
		return result, nil
	}
	if factory == nil {
		return IDValidationResult{}, errors.New("validacao de IDs exige uma fabrica de clientes OEM")
	}
	infoLogger := validationInfoLogger(opts.Logger, opts.InfoLogger)

	for siteIndex, site := range corrected {
		if err := ctx.Err(); err != nil {
			return IDValidationResult{}, err
		}

		lister, err := factory(site)
		if err != nil {
			return IDValidationResult{}, fmt.Errorf("site[%d] %q: criar cliente OEM: %w", siteIndex, site.Name, err)
		}

		if infoLogger != nil {
			infoLogger.InfoContext(ctx, "validacao de IDs: listando targets OEM",
				"site_index", siteIndex,
				"site", site.Name,
				"endpoint", site.Endpoint,
				"configured_targets", len(site.Targets),
			)
		}
		targets, err := lister.ListTargets(ctx)
		if err != nil {
			return IDValidationResult{}, fmt.Errorf("site[%d] %q: listar targets OEM: %w", siteIndex, site.Name, err)
		}
		if infoLogger != nil {
			infoLogger.InfoContext(ctx, "validacao de IDs: targets OEM listados",
				"site_index", siteIndex,
				"site", site.Name,
				"endpoint", site.Endpoint,
				"targets", len(targets.Items),
			)
		}
		index := indexTargetsByNameAndType(targets.Items)

		keptTargets := make([]config.TargetConfig, 0, len(site.Targets))
		for targetIndex, target := range site.Targets {
			key := targetKey(target.Name, target.TypeName)
			matches := index[key]
			switch len(matches) {
			case 0:
				result.TargetRemovals = append(result.TargetRemovals, TargetRemoval{
					SiteIndex:   siteIndex,
					TargetIndex: targetIndex,
					SiteName:    site.Name,
					TargetName:  target.Name,
					TargetType:  target.TypeName,
					ConfigID:    target.ID,
					Reason:      WarningTargetMissing,
				})
				warning := Warning{
					Code:        WarningTargetMissing,
					SiteIndex:   siteIndex,
					TargetIndex: targetIndex,
					SiteName:    site.Name,
					TargetName:  target.Name,
					TargetType:  target.TypeName,
					ConfigID:    target.ID,
					Message:     fmt.Sprintf("target %q tipo %q nao encontrado na API OEM", target.Name, target.TypeName),
				}
				result.addWarning(ctx, opts.Logger, warning)
			case 1:
				current := matches[0]
				currentID := strings.TrimSpace(current.ID)
				if target.ID != currentID {
					correction := IDCorrection{
						SiteIndex:   siteIndex,
						TargetIndex: targetIndex,
						SiteName:    site.Name,
						TargetName:  target.Name,
						TargetType:  target.TypeName,
						OldID:       target.ID,
						NewID:       currentID,
					}
					target.ID = currentID
					result.IDCorrections = append(result.IDCorrections, correction)
					warning := Warning{
						Code:        WarningIDDivergent,
						SiteIndex:   siteIndex,
						TargetIndex: targetIndex,
						SiteName:    site.Name,
						TargetName:  target.Name,
						TargetType:  target.TypeName,
						ConfigID:    correction.OldID,
						CurrentID:   currentID,
						Message:     fmt.Sprintf("ID do target %q tipo %q diverge da API OEM", target.Name, target.TypeName),
					}
					result.addWarning(ctx, opts.Logger, warning)
				}
				keptTargets = append(keptTargets, target)
			default:
				warning := Warning{
					Code:        WarningTargetDuplicate,
					SiteIndex:   siteIndex,
					TargetIndex: targetIndex,
					SiteName:    site.Name,
					TargetName:  target.Name,
					TargetType:  target.TypeName,
					ConfigID:    target.ID,
					Count:       len(matches),
					Message:     fmt.Sprintf("target %q tipo %q retornou %d correspondencias na API OEM", target.Name, target.TypeName, len(matches)),
				}
				result.addWarning(ctx, opts.Logger, warning)
				keptTargets = append(keptTargets, target)
			}
		}
		corrected[siteIndex].Targets = keptTargets
	}

	result.Sites = result.removeEmptySites(corrected)
	return result, nil
}

func (r *IDValidationResult) removeEmptySites(sites []config.SiteConfig) []config.SiteConfig {
	removedTargetsBySite := make(map[int]int, len(r.TargetRemovals))
	for _, removal := range r.TargetRemovals {
		removedTargetsBySite[removal.SiteIndex]++
	}

	filtered := make([]config.SiteConfig, 0, len(sites))
	for siteIndex, site := range sites {
		if len(site.Targets) > 0 {
			filtered = append(filtered, site)
			continue
		}
		r.SiteRemovals = append(r.SiteRemovals, SiteRemoval{
			SiteIndex:      siteIndex,
			SiteName:       site.Name,
			Endpoint:       site.Endpoint,
			RemovedTargets: removedTargetsBySite[siteIndex],
		})
	}
	return filtered
}

func (r *IDValidationResult) addWarning(ctx context.Context, logger Logger, warning Warning) {
	r.Warnings = append(r.Warnings, warning)
	if logger == nil {
		return
	}
	args := []any{
		"code", string(warning.Code),
		"site_index", warning.SiteIndex,
		"site", warning.SiteName,
		"target_index", warning.TargetIndex,
		"target_name", warning.TargetName,
		"target_type", warning.TargetType,
		"config_id", warning.ConfigID,
	}
	if warning.CurrentID != "" {
		args = append(args, "current_id", warning.CurrentID)
	}
	if warning.Count > 0 {
		args = append(args, "count", warning.Count)
	}
	logger.WarnContext(ctx, warning.Message, args...)
}

func indexTargetsByNameAndType(targets []oem.Target) map[string][]oem.Target {
	index := make(map[string][]oem.Target, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.ID) == "" {
			continue
		}
		key := targetKey(target.Name, target.TypeName)
		if key == "" {
			continue
		}
		index[key] = append(index[key], target)
	}
	return index
}

func targetKey(name, typeName string) string {
	name = strings.TrimSpace(name)
	typeName = strings.TrimSpace(typeName)
	if name == "" || typeName == "" {
		return ""
	}
	return name + "\x00" + typeName
}

func cloneSites(sites []config.SiteConfig) []config.SiteConfig {
	out := make([]config.SiteConfig, len(sites))
	for siteIndex, site := range sites {
		out[siteIndex] = site
		out[siteIndex].Targets = make([]config.TargetConfig, len(site.Targets))
		for targetIndex, target := range site.Targets {
			out[siteIndex].Targets[targetIndex] = target
			if target.Tags != nil {
				tags := make(map[string]string, len(target.Tags))
				for key, value := range target.Tags {
					tags[key] = value
				}
				out[siteIndex].Targets[targetIndex].Tags = tags
			}
		}
	}
	return out
}
