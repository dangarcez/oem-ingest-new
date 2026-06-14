package validate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
)

const (
	targetTypeDBSystem = "oracle_dbsys"
	targetTypeRAC      = "rac_database"
	targetTypePDB      = "oracle_pdb"
	targetTypeDatabase = "oracle_database"
	targetTypeHost     = "host"
	targetTypeListener = "oracle_listener"
)

const (
	WarningTargetAdded        WarningCode = "target_added"
	WarningTargetTagsChanged  WarningCode = "target_tags_changed"
	WarningTargetProperties   WarningCode = "target_properties_warning"
	WarningCorrelationSkipped WarningCode = "correlation_skipped"
)

// TargetInventory is implemented by the OEM client endpoints required to
// validate target relationships and tags.
type TargetInventory interface {
	TargetLister
	TargetProperties(ctx context.Context, targetID string) (oem.Page[oem.Property], error)
}

// TargetInventoryFactory returns the OEM inventory client for one configured
// site.
type TargetInventoryFactory func(site config.SiteConfig) (TargetInventory, error)

// CorrelationValidationOptions controls the optional correlation validation
// step.
type CorrelationValidationOptions struct {
	Enabled bool
	Logger  Logger
}

// CorrelationValidationResult contains the corrected in-memory target
// configuration and a machine-readable summary of correlation changes.
type CorrelationValidationResult struct {
	Sites          []config.SiteConfig
	TargetAdds     []TargetAddition
	TagCorrections []TagCorrection
	Warnings       []Warning
}

// Changed reports whether validation added targets or corrected tags in memory.
func (r CorrelationValidationResult) Changed() bool {
	return len(r.TargetAdds) > 0 || len(r.TagCorrections) > 0
}

// TargetAddition describes one related target added to the in-memory config.
type TargetAddition struct {
	SiteIndex      int
	TargetIndex    int
	SiteName       string
	TargetName     string
	TargetType     string
	SourceRootName string
	SourceRootType string
}

// TagCorrection describes one existing target whose structural tags were
// corrected to match the legacy mapping rules.
type TagCorrection struct {
	SiteIndex   int
	TargetIndex int
	SiteName    string
	TargetName  string
	TargetType  string
}

// ValidateTargetCorrelations expands and retags related database targets using
// the legacy OEM mapping rules. It never writes files and never mutates input.
func ValidateTargetCorrelations(ctx context.Context, sites []config.SiteConfig, factory TargetInventoryFactory, opts CorrelationValidationOptions) (CorrelationValidationResult, error) {
	corrected := cloneSites(sites)
	result := CorrelationValidationResult{Sites: corrected}
	if !opts.Enabled {
		return result, nil
	}
	if factory == nil {
		return CorrelationValidationResult{}, errors.New("validacao de correlacao exige uma fabrica de clientes OEM")
	}

	for siteIndex := range corrected {
		if err := ctx.Err(); err != nil {
			return CorrelationValidationResult{}, err
		}

		site := corrected[siteIndex]
		client, err := factory(site)
		if err != nil {
			return CorrelationValidationResult{}, fmt.Errorf("site[%d] %q: criar cliente OEM: %w", siteIndex, site.Name, err)
		}

		targets, err := client.ListTargets(ctx)
		if err != nil {
			return CorrelationValidationResult{}, fmt.Errorf("site[%d] %q: listar targets OEM: %w", siteIndex, site.Name, err)
		}
		inventory := newTargetInventory(targets.Items)

		roots := append([]config.TargetConfig(nil), site.Targets...)
		for _, root := range roots {
			if !isExpandableRoot(root.TypeName) {
				continue
			}

			generated, err := buildRelatedTargets(ctx, client, inventory, root, func(warning Warning) {
				warning.SiteIndex = siteIndex
				warning.SiteName = site.Name
				result.addWarning(ctx, opts.Logger, warning)
			})
			if err != nil {
				return CorrelationValidationResult{}, err
			}
			for _, target := range generated {
				applyGeneratedTarget(ctx, opts.Logger, &result, siteIndex, root, target)
			}
		}
	}

	result.Sites = corrected
	return result, nil
}

func (r *CorrelationValidationResult) addWarning(ctx context.Context, logger Logger, warning Warning) {
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
	}
	if warning.ConfigID != "" {
		args = append(args, "config_id", warning.ConfigID)
	}
	logger.WarnContext(ctx, warning.Message, args...)
}

func applyGeneratedTarget(ctx context.Context, logger Logger, result *CorrelationValidationResult, siteIndex int, root config.TargetConfig, generated config.TargetConfig) {
	site := &result.Sites[siteIndex]
	targetIndex := findConfigTargetIndex(site.Targets, generated)
	if targetIndex < 0 {
		site.Targets = append(site.Targets, generated)
		targetIndex = len(site.Targets) - 1
		addition := TargetAddition{
			SiteIndex:      siteIndex,
			TargetIndex:    targetIndex,
			SiteName:       site.Name,
			TargetName:     generated.Name,
			TargetType:     generated.TypeName,
			SourceRootName: root.Name,
			SourceRootType: root.TypeName,
		}
		result.TargetAdds = append(result.TargetAdds, addition)
		result.addWarning(ctx, logger, Warning{
			Code:        WarningTargetAdded,
			SiteIndex:   siteIndex,
			TargetIndex: targetIndex,
			SiteName:    site.Name,
			TargetName:  generated.Name,
			TargetType:  generated.TypeName,
			ConfigID:    generated.ID,
			Message:     fmt.Sprintf("target relacionado %q tipo %q adicionado em memoria", generated.Name, generated.TypeName),
		})
		return
	}

	existing := site.Targets[targetIndex]
	mergedTags := mergeGeneratedTags(generated.Tags, existing.Tags)
	if existing.ID == generated.ID && existing.Name == generated.Name && existing.TypeName == generated.TypeName && equalStringMap(existing.Tags, mergedTags) {
		return
	}

	site.Targets[targetIndex].ID = generated.ID
	site.Targets[targetIndex].Name = generated.Name
	site.Targets[targetIndex].TypeName = generated.TypeName
	site.Targets[targetIndex].Tags = mergedTags
	correction := TagCorrection{
		SiteIndex:   siteIndex,
		TargetIndex: targetIndex,
		SiteName:    site.Name,
		TargetName:  generated.Name,
		TargetType:  generated.TypeName,
	}
	result.TagCorrections = append(result.TagCorrections, correction)
	result.addWarning(ctx, logger, Warning{
		Code:        WarningTargetTagsChanged,
		SiteIndex:   siteIndex,
		TargetIndex: targetIndex,
		SiteName:    site.Name,
		TargetName:  generated.Name,
		TargetType:  generated.TypeName,
		ConfigID:    generated.ID,
		Message:     fmt.Sprintf("tags do target %q tipo %q corrigidas em memoria", generated.Name, generated.TypeName),
	})
}

func buildRelatedTargets(ctx context.Context, client TargetInventory, inventory targetInventory, root config.TargetConfig, warn func(Warning)) ([]config.TargetConfig, error) {
	if !inventory.hasTarget(root.Name, root.TypeName) {
		warn(Warning{
			Code:       WarningCorrelationSkipped,
			TargetName: root.Name,
			TargetType: root.TypeName,
			ConfigID:   root.ID,
			Message:    fmt.Sprintf("correlacao do target %q tipo %q ignorada: target raiz nao encontrado na API OEM", root.Name, root.TypeName),
		})
		return nil, nil
	}

	roots := buildRelatedTree(ctx, client, inventory, root, warn)
	externalTags := externalTargetTags(root.Tags)
	return taggedTargetList(roots, externalTags), nil
}

func buildRelatedTree(ctx context.Context, client TargetInventory, inventory targetInventory, root config.TargetConfig, warn func(Warning)) []*relatedTarget {
	rootName := strings.TrimSpace(root.Name)
	racName := rootName
	isPDB := root.TypeName == targetTypePDB
	if isPDB {
		racName = rootRACName(rootName)
	}
	standbyName := strings.ReplaceAll(racName, "p", "s")

	primarySearches := []targetSearch{
		{patterns: exactPatterns(racName + "_sys"), targetType: targetTypeDBSystem, unique: true},
		{patterns: exactPatterns(racName), targetType: targetTypeRAC, unique: true},
		{patterns: databasePatterns(racName), targetType: targetTypeDatabase},
	}
	standbySearches := []targetSearch{
		{patterns: exactPatterns(standbyName + "_sys"), targetType: targetTypeDBSystem, unique: true},
		{patterns: exactPatterns(standbyName, standbyName+"_1"), targetType: targetTypeRAC, unique: true},
		{patterns: databasePatterns(standbyName), targetType: targetTypeDatabase},
	}
	if isPDB {
		primarySearches = insertSearch(primarySearches, 2, targetSearch{patterns: exactPatterns(rootName), targetType: targetTypePDB, unique: true})
		standbySearches = insertSearch(standbySearches, 2, targetSearch{patterns: exactPatterns(strings.ReplaceAll(rootName, "p", "s")), targetType: targetTypePDB, unique: true})
	}

	var heads []*relatedTarget
	for _, searches := range [][]targetSearch{primarySearches, standbySearches} {
		if head := buildSearchHead(ctx, client, inventory, searches, warn); head != nil {
			heads = append(heads, head)
		}
	}
	if len(heads) <= 1 {
		return heads
	}
	if heads[0].TypeName == targetTypeDBSystem {
		heads[0].Children = append(heads[0].Children, heads[1])
		return []*relatedTarget{heads[0]}
	}
	if heads[1].TypeName == targetTypeDBSystem {
		heads[1].Children = append(heads[1].Children, heads[0])
		return []*relatedTarget{heads[1]}
	}
	return heads
}

func buildSearchHead(ctx context.Context, client TargetInventory, inventory targetInventory, searches []targetSearch, warn func(Warning)) *relatedTarget {
	var head *relatedTarget
	var parent *relatedTarget
	for _, search := range searches {
		results := inventory.find(search)
		var firstResult *relatedTarget
		for _, target := range results {
			node := newRelatedTarget(ctx, client, inventory, target, warn)
			if firstResult == nil {
				firstResult = node
			}
			if parent == nil {
				head = node
				parent = node
				continue
			}
			parent.Children = append(parent.Children, node)
		}
		if firstResult != nil {
			parent = firstResult
		}
	}
	return head
}

func newRelatedTarget(ctx context.Context, client TargetInventory, inventory targetInventory, target oem.Target, warn func(Warning)) *relatedTarget {
	node := &relatedTarget{
		ID:       strings.TrimSpace(target.ID),
		Name:     strings.TrimSpace(target.Name),
		TypeName: strings.TrimSpace(target.TypeName),
	}
	if node.TypeName != targetTypeDatabase {
		return node
	}

	properties, err := client.TargetProperties(ctx, node.ID)
	if err != nil {
		if ctx.Err() == nil {
			warn(Warning{
				Code:       WarningTargetProperties,
				TargetName: node.Name,
				TargetType: node.TypeName,
				ConfigID:   node.ID,
				Message:    fmt.Sprintf("nao foi possivel consultar propriedades do target %q", node.Name),
			})
		}
		return node
	}

	node.DGRole = propertyValue(properties.Items, "DataGuardStatus")
	if node.DGRole == "" {
		node.DGRole = "unknown"
	}
	machineName := strings.ReplaceAll(strings.TrimSpace(propertyValue(properties.Items, "MachineName")), "-vip", "")
	if machineName == "" {
		return node
	}

	if hosts := inventory.find(targetSearch{patterns: exactPatterns(machineName), targetType: targetTypeHost, unique: true}); len(hosts) > 0 {
		host := hosts[0]
		node.MachineName = strings.TrimSpace(host.Name)
		node.Children = append(node.Children, &relatedTarget{
			ID:       strings.TrimSpace(host.ID),
			Name:     strings.TrimSpace(host.Name),
			TypeName: strings.TrimSpace(host.TypeName),
		})
	}
	listenerName := "LISTENER_" + machineName
	if listeners := inventory.find(targetSearch{patterns: exactPatterns(listenerName), targetType: targetTypeListener, unique: true}); len(listeners) > 0 {
		listener := listeners[0]
		node.ListenerName = strings.TrimSpace(listener.Name)
		node.Children = append(node.Children, &relatedTarget{
			ID:       strings.TrimSpace(listener.ID),
			Name:     strings.TrimSpace(listener.Name),
			TypeName: strings.TrimSpace(listener.TypeName),
		})
	}
	return node
}

type relatedTarget struct {
	ID           string
	Name         string
	TypeName     string
	DGRole       string
	MachineName  string
	ListenerName string
	Children     []*relatedTarget
}

type targetSearch struct {
	patterns   []*regexp.Regexp
	targetType string
	unique     bool
}

type targetInventory struct {
	targets []oem.Target
}

func newTargetInventory(targets []oem.Target) targetInventory {
	out := make([]oem.Target, 0, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.ID) == "" || strings.TrimSpace(target.Name) == "" || strings.TrimSpace(target.TypeName) == "" {
			continue
		}
		out = append(out, target)
	}
	return targetInventory{targets: out}
}

func (i targetInventory) hasTarget(name, targetType string) bool {
	return len(i.find(targetSearch{patterns: exactPatterns(name), targetType: targetType, unique: true})) > 0
}

func (i targetInventory) find(search targetSearch) []oem.Target {
	for _, pattern := range search.patterns {
		var matches []oem.Target
		for _, target := range i.targets {
			if strings.TrimSpace(target.TypeName) != search.targetType {
				continue
			}
			if !fullRegexpMatch(pattern, strings.TrimSpace(target.Name)) {
				continue
			}
			matches = append(matches, target)
			if search.unique {
				return matches
			}
		}
		if len(matches) > 0 {
			return matches
		}
	}
	return nil
}

func exactPatterns(values ...string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, len(values))
	for _, value := range values {
		patterns = append(patterns, regexp.MustCompile(regexp.QuoteMeta(value)))
	}
	return patterns
}

func databasePatterns(racName string) []*regexp.Regexp {
	quoted := regexp.QuoteMeta(racName)
	return []*regexp.Regexp{regexp.MustCompile(`^` + quoted + `(?:_\d+)?_` + quoted + `\d*$`)}
}

func fullRegexpMatch(pattern *regexp.Regexp, value string) bool {
	match := pattern.FindStringIndex(value)
	return match != nil && match[0] == 0 && match[1] == len(value)
}

func insertSearch(searches []targetSearch, index int, search targetSearch) []targetSearch {
	out := append([]targetSearch(nil), searches[:index]...)
	out = append(out, search)
	out = append(out, searches[index:]...)
	return out
}

func taggedTargetList(roots []*relatedTarget, inherited map[string]string) []config.TargetConfig {
	seen := make(map[string]struct{})
	var out []config.TargetConfig
	for _, root := range roots {
		out = append(out, taggedTarget(root, inherited, seen)...)
	}
	return out
}

func taggedTarget(target *relatedTarget, inherited map[string]string, seen map[string]struct{}) []config.TargetConfig {
	if target == nil {
		return nil
	}
	key := target.ID
	if key == "" {
		key = targetKey(target.Name, target.TypeName)
	}
	if _, ok := seen[key]; ok {
		return nil
	}
	seen[key] = struct{}{}

	tags := make(map[string]string)
	if !isAdjacentTarget(target.TypeName) {
		for key, value := range inherited {
			tags[key] = value
		}
	}
	tags["target_name"] = target.Name
	tags["target_type"] = target.TypeName
	tags[target.TypeName] = target.Name
	if target.DGRole != "" {
		tags["dg_role"] = target.DGRole
	}
	switch target.TypeName {
	case targetTypeHost:
		host := shortHostName(target.Name)
		tags["target_name"] = host
		tags[targetTypeHost] = host
	case targetTypeListener:
		listener := shortListenerName(target.Name)
		tags["target_name"] = listener
		tags[targetTypeListener] = listener
	}
	if target.MachineName != "" {
		tags["machine_name"] = shortHostName(target.MachineName)
	}
	if target.ListenerName != "" {
		tags["listener_name"] = shortListenerName(target.ListenerName)
	}

	configTarget := config.TargetConfig{
		ID:       target.ID,
		Name:     target.Name,
		TypeName: target.TypeName,
		Tags:     tags,
	}
	out := []config.TargetConfig{configTarget}
	for _, child := range target.Children {
		out = append(out, taggedTarget(child, tags, seen)...)
	}
	return out
}

func propertyValue(properties []oem.Property, key string) string {
	for _, property := range properties {
		if strings.EqualFold(strings.TrimSpace(property.ID), key) || strings.EqualFold(strings.TrimSpace(property.Name), key) {
			return strings.TrimSpace(property.Value)
		}
	}
	return ""
}

func rootRACName(rootName string) string {
	if before, _, ok := strings.Cut(rootName, "_"); ok && before != "" {
		return before
	}
	return rootName
}

func isExpandableRoot(targetType string) bool {
	return targetType == targetTypeRAC || targetType == targetTypePDB
}

func isAdjacentTarget(targetType string) bool {
	return targetType == targetTypeHost || targetType == targetTypeListener
}

func shortHostName(name string) string {
	name = strings.TrimSpace(name)
	if before, _, ok := strings.Cut(name, "."); ok {
		return before
	}
	return name
}

func shortListenerName(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "LISTENER_")
	return shortHostName(name) + "_lstnr"
}

func externalTargetTags(tags map[string]string) map[string]string {
	out := make(map[string]string)
	for key, value := range tags {
		if isStructuralTag(key) {
			continue
		}
		out[key] = value
	}
	return out
}

func isStructuralTag(key string) bool {
	switch key {
	case "target_name", "target_type", "dg_role", "machine_name", "listener_name",
		targetTypeDBSystem, targetTypeRAC, targetTypePDB, targetTypeDatabase, targetTypeHost, targetTypeListener:
		return true
	default:
		return false
	}
}

func mergeGeneratedTags(generated, existing map[string]string) map[string]string {
	out := make(map[string]string, len(generated)+len(existing))
	for key, value := range generated {
		out[key] = value
	}
	for key, value := range existing {
		if isStructuralTag(key) {
			continue
		}
		out[key] = value
	}
	return out
}

func findConfigTargetIndex(targets []config.TargetConfig, wanted config.TargetConfig) int {
	if strings.TrimSpace(wanted.ID) != "" {
		for index, target := range targets {
			if strings.TrimSpace(target.ID) == wanted.ID {
				return index
			}
		}
	}
	for index, target := range targets {
		if target.Name == wanted.Name && target.TypeName == wanted.TypeName {
			return index
		}
	}
	return -1
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		if right[key] != leftValue {
			return false
		}
	}
	return true
}
