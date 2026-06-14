package transform

import (
	"reflect"
	"testing"

	"oem-ingest-new/internal/collect"
	"oem-ingest-new/internal/config"
)

func TestBuildAttributesMergesTargetTagsAndMetricKeys(t *testing.T) {
	target := config.TargetConfig{
		ID:       "target-1",
		Name:     "host01",
		TypeName: "host",
		Tags: map[string]string{
			"target_name": "host01",
			"target_type": "host",
			"sistema":     "pix",
			"torre":       "cartoes",
		},
	}
	metadata := collect.GroupMetadata{
		Keys: []string{"MountPoint", "FileSystem"},
	}
	item := map[string]any{
		"MountPoint":   "/",
		"FileSystem":   "/dev/vda1",
		"SpaceUsedPct": 39.18,
		"pctAvailable": "60.82",
	}

	attrs := BuildAttributes(target, metadata, item)

	want := Attributes{
		"target_name": "host01",
		"target_type": "host",
		"sistema":     "pix",
		"torre":       "cartoes",
		"MountPoint":  "/",
		"FileSystem":  "/dev/vda1",
	}
	if !reflect.DeepEqual(attrs, want) {
		t.Fatalf("attributes = %#v, want %#v", attrs, want)
	}
}

func TestBuildAttributesAppliesLegacyConflictRenames(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]string
		item     map[string]any
		keys     []string
		expected Attributes
	}{
		{
			name: "instance tag becomes _instance",
			tags: map[string]string{
				"target_name": "db1",
				"target_type": "oracle_database",
				"instance":    "db1_1",
			},
			expected: Attributes{
				"target_name": "db1",
				"target_type": "oracle_database",
				"_instance":   "db1_1",
			},
		},
		{
			name: "name tag becomes name_",
			tags: map[string]string{
				"target_name": "svc",
				"target_type": "oracle_database",
				"name":        "legacy-name",
			},
			expected: Attributes{
				"target_name": "svc",
				"target_type": "oracle_database",
				"name_":       "legacy-name",
			},
		},
		{
			name: "service_name follows legacy service_name then name conflict order",
			tags: map[string]string{
				"target_name":  "svc",
				"target_type":  "rac_database",
				"service_name": "app-service",
			},
			expected: Attributes{
				"target_name": "svc",
				"target_type": "rac_database",
				"name_":       "app-service",
			},
		},
		{
			name: "Username_machine derives user and pod",
			tags: map[string]string{
				"target_name":      "audit",
				"target_type":      "oracle_database",
				"Username_machine": "APPUSER_pod-7_extra",
			},
			expected: Attributes{
				"target_name":      "audit",
				"target_type":      "oracle_database",
				"Username_machine": "APPUSER_pod-7_extra",
				"user":             "APPUSER",
				"pod":              "pod-7",
			},
		},
		{
			name: "metric keys use same conflict rules",
			tags: map[string]string{
				"target_name": "db1",
				"target_type": "oracle_database",
			},
			keys: []string{"instance", "Username_machine"},
			item: map[string]any{
				"instance":         "db1_2",
				"Username_machine": "OPS_pod-a",
				"value":            1,
			},
			expected: Attributes{
				"target_name":      "db1",
				"target_type":      "oracle_database",
				"_instance":        "db1_2",
				"Username_machine": "OPS_pod-a",
				"user":             "OPS",
				"pod":              "pod-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := config.TargetConfig{
				ID:       "target-1",
				Name:     tt.tags["target_name"],
				TypeName: tt.tags["target_type"],
				Tags:     tt.tags,
			}
			attrs := BuildAttributes(target, collect.GroupMetadata{Keys: tt.keys}, tt.item)
			if !reflect.DeepEqual(attrs, tt.expected) {
				t.Fatalf("attributes = %#v, want %#v", attrs, tt.expected)
			}
		})
	}
}

func TestBuildAttributesDoesNotMutateTargetTags(t *testing.T) {
	target := config.TargetConfig{
		ID:       "target-1",
		Name:     "db1",
		TypeName: "oracle_database",
		Tags: map[string]string{
			"target_name": "db1",
			"target_type": "oracle_database",
			"instance":    "db1_1",
		},
	}
	original := map[string]string{
		"target_name": "db1",
		"target_type": "oracle_database",
		"instance":    "db1_1",
	}

	attrs := BuildAttributes(target, collect.GroupMetadata{}, nil)
	attrs["target_name"] = "changed"

	if !reflect.DeepEqual(target.Tags, original) {
		t.Fatalf("target tags mutated: %#v, want %#v", target.Tags, original)
	}
}
