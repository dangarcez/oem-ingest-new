package transform

import (
	"fmt"
	"strings"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
)

// Attributes contains OTLP attributes produced from target tags and metric
// group keys. Values are kept typed because later OTLP conversion can preserve
// numeric and boolean key values returned by OEM.
type Attributes map[string]any

// BuildAttributes reproduces the legacy build_tags/_buildAttributes behavior:
// target tags are copied first, metric group keys from the latestData item are
// merged over them, and conflicting names are rewritten in the same order used
// by the Python collector.
func BuildAttributes(target config.TargetConfig, metadata collect.GroupMetadata, item map[string]any) Attributes {
	attrs := make(Attributes, len(target.Tags)+len(metadata.Keys)+2)
	for key, value := range target.Tags {
		attrs[key] = value
	}
	for _, key := range metadata.Keys {
		if value, ok := item[key]; ok {
			attrs[key] = value
		}
	}

	applyLegacyConflicts(attrs)
	return attrs
}

func applyLegacyConflicts(attrs Attributes) {
	if value, ok := attrs["instance"]; ok {
		attrs["_instance"] = value
		delete(attrs, "instance")
	}
	if value, ok := attrs["service_name"]; ok {
		attrs["name"] = value
		delete(attrs, "service_name")
	}
	if value, ok := attrs["name"]; ok {
		attrs["name_"] = value
		delete(attrs, "name")
	}
	if value, ok := attrs["Username_machine"]; ok {
		parts := strings.Split(fmt.Sprint(value), "_")
		if len(parts) >= 2 {
			attrs["user"] = parts[0]
			attrs["pod"] = parts[1]
		}
	}
}
