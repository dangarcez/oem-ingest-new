package oem

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Link represents one OEM hypermedia link.
type Link struct {
	Href string `json:"href"`
}

// Links is keyed by relation name, for example "self" or "next".
type Links map[string]Link

// NextHref returns links.next.href when present.
func (l Links) NextHref() string {
	if l == nil {
		return ""
	}
	return l["next"].Href
}

// UnmarshalJSON accepts the map form used by the legacy code and the array
// form used by some Oracle REST endpoints.
func (l *Links) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*l = nil
		return nil
	}

	var asMap map[string]Link
	if err := json.Unmarshal(data, &asMap); err == nil {
		*l = asMap
		return nil
	}

	var asArray []struct {
		Rel  string `json:"rel"`
		Name string `json:"name"`
		Href string `json:"href"`
	}
	if err := json.Unmarshal(data, &asArray); err != nil {
		return fmt.Errorf("links OEM em formato inesperado: %w", err)
	}

	out := make(map[string]Link, len(asArray))
	for _, item := range asArray {
		rel := item.Rel
		if rel == "" {
			rel = item.Name
		}
		if rel == "" {
			continue
		}
		out[rel] = Link{Href: item.Href}
	}
	*l = out
	return nil
}

// Page represents OEM list responses with an items array.
type Page[T any] struct {
	Count int   `json:"count"`
	Links Links `json:"links"`
	Items []T   `json:"items"`
}

// Target is one OEM monitored target.
type Target struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	TypeName        string `json:"typeName"`
	DisplayName     string `json:"displayName"`
	TypeDisplayName string `json:"typeDisplayName"`
	Owner           string `json:"owner"`
	Links           Links  `json:"links"`
}

// Property is one target property returned by OEM.
type Property struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Value       string `json:"value"`
}

// MetricGroup describes one OEM metric group and its metadata.
type MetricGroup struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	DisplayName       string             `json:"displayName"`
	IsMetricExtension bool               `json:"isMetricExtension"`
	Keys              []MetricKey        `json:"keys"`
	Metrics           []MetricDefinition `json:"metrics"`
	Links             Links              `json:"links"`
}

// MetricKey describes a key column in a metric group.
type MetricKey struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// MetricDefinition describes a metric column in a metric group.
type MetricDefinition struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	DataType    string `json:"dataType"`
}

// LatestData represents the latest metric values for a target/group pair.
type LatestData struct {
	TargetName      string           `json:"targetName"`
	TargetTypeName  string           `json:"targetTypeName"`
	TargetID        string           `json:"targetId"`
	MetricGroupName string           `json:"metricGroupName"`
	TimeCollected   string           `json:"timeCollected"`
	Count           int              `json:"count"`
	Links           Links            `json:"links"`
	Items           []map[string]any `json:"items"`
}

// Incident represents both incident list items and detail payloads.
type Incident struct {
	ID                  string              `json:"id"`
	DisplayID           int                 `json:"displayId"`
	Message             string              `json:"message"`
	Targets             []IncidentTarget    `json:"targets"`
	TimeCreated         string              `json:"timeCreated"`
	TimeUpdated         string              `json:"timeUpdated"`
	AgeInHours          float64             `json:"ageInHours"`
	IsOpen              bool                `json:"isOpen"`
	Status              string              `json:"status"`
	Owner               string              `json:"owner"`
	IsAcknowledged      bool                `json:"isAcknowledged"`
	IsEscalated         bool                `json:"isEscalated"`
	Severity            string              `json:"severity"`
	CanBeManuallyClosed bool                `json:"canBeManuallyClosed"`
	IsDiagnostic        bool                `json:"isDiagnosticIncident"`
	Links               Links               `json:"links"`
	Extra               map[string]any      `json:"-"`
	PresentFields       map[string]struct{} `json:"-"`
}

// UnmarshalJSON keeps unmodeled incident fields so the incident log exporter
// can preserve attributes returned by OEM without requiring a type change.
func (i *Incident) UnmarshalJSON(data []byte) error {
	type incidentAlias Incident
	var decoded incidentAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	present := make(map[string]struct{}, len(raw))
	for field := range raw {
		present[field] = struct{}{}
	}
	for _, field := range knownIncidentFields {
		delete(raw, field)
	}
	if len(raw) == 0 {
		raw = nil
	}

	*i = Incident(decoded)
	i.Extra = raw
	i.PresentFields = present
	return nil
}

// HasField reports whether a JSON incident payload contained field. Incidents
// built directly in tests keep the historical behavior of treating fields as
// present.
func (i Incident) HasField(field string) bool {
	if i.PresentFields == nil {
		return true
	}
	_, ok := i.PresentFields[field]
	return ok
}

var knownIncidentFields = []string{
	"id",
	"displayId",
	"message",
	"targets",
	"timeCreated",
	"timeUpdated",
	"ageInHours",
	"isOpen",
	"status",
	"owner",
	"isAcknowledged",
	"isEscalated",
	"severity",
	"canBeManuallyClosed",
	"isDiagnosticIncident",
	"links",
}

// IncidentTarget is a target attached to an OEM incident.
type IncidentTarget struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	TypeName        string `json:"typeName"`
	TypeDisplayName string `json:"typeDisplayName"`
}
