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

// TargetLister is implemented by the OEM client endpoint used in this phase.
type TargetLister interface {
	ListTargets(ctx context.Context) (oem.Page[oem.Target], error)
}

// TargetListerFactory returns the OEM client for one configured site.
type TargetListerFactory func(site config.SiteConfig) (TargetLister, error)

// IDValidationOptions controls the optional ID validation step.
type IDValidationOptions struct {
	Enabled bool
	Logger  Logger
}

// IDValidationResult contains the corrected in-memory target configuration and
// a machine-readable summary of warnings and corrections.
type IDValidationResult struct {
	Sites         []config.SiteConfig
	IDCorrections []IDCorrection
	Warnings      []Warning
}

// Changed reports whether validation updated at least one target ID in memory.
func (r IDValidationResult) Changed() bool {
	return len(r.IDCorrections) > 0
}

// IDCorrection describes one target ID changed to match the current OEM API.
type IDCorrection struct {
	SiteIndex   int
	TargetIndex int
	SiteName    string
	TargetName  string
	TargetType  string
	OldID       string
	NewID       string
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
	Code        WarningCode
	SiteIndex   int
	TargetIndex int
	SiteName    string
	TargetName  string
	TargetType  string
	ConfigID    string
	CurrentID   string
	Count       int
	Message     string
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

	for siteIndex, site := range corrected {
		if err := ctx.Err(); err != nil {
			return IDValidationResult{}, err
		}

		lister, err := factory(site)
		if err != nil {
			return IDValidationResult{}, fmt.Errorf("site[%d] %q: criar cliente OEM: %w", siteIndex, site.Name, err)
		}

		targets, err := lister.ListTargets(ctx)
		if err != nil {
			return IDValidationResult{}, fmt.Errorf("site[%d] %q: listar targets OEM: %w", siteIndex, site.Name, err)
		}
		index := indexTargetsByNameAndType(targets.Items)

		for targetIndex, target := range site.Targets {
			key := targetKey(target.Name, target.TypeName)
			matches := index[key]
			switch len(matches) {
			case 0:
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
				if target.ID == currentID {
					continue
				}
				correction := IDCorrection{
					SiteIndex:   siteIndex,
					TargetIndex: targetIndex,
					SiteName:    site.Name,
					TargetName:  target.Name,
					TargetType:  target.TypeName,
					OldID:       target.ID,
					NewID:       currentID,
				}
				corrected[siteIndex].Targets[targetIndex].ID = currentID
				result.IDCorrections = append(result.IDCorrections, correction)
				warning := Warning{
					Code:        WarningIDDivergent,
					SiteIndex:   siteIndex,
					TargetIndex: targetIndex,
					SiteName:    site.Name,
					TargetName:  target.Name,
					TargetType:  target.TypeName,
					ConfigID:    target.ID,
					CurrentID:   currentID,
					Message:     fmt.Sprintf("ID do target %q tipo %q diverge da API OEM", target.Name, target.TypeName),
				}
				result.addWarning(ctx, opts.Logger, warning)
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
			}
		}
	}

	result.Sites = corrected
	return result, nil
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
