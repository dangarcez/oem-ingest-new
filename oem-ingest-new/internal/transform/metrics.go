package transform

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"oem-ingest-new/internal/collect"
)

// MetricPoint is a normalized numeric OEM metric ready to become an OTLP
// gauge datapoint.
type MetricPoint struct {
	Name            string
	MetricGroupName string
	MetricName      string
	TargetID        string
	SeriesID        string
	Value           float64
	Attributes      Attributes
	Timestamp       time.Time
}

// LogRecord is a normalized textual OEM metric ready to become an OTLP log.
// Body is the textual value. Attributes include "metric", matching the legacy
// log exporter convention.
type LogRecord struct {
	MetricName string
	TargetID   string
	SeriesID   string
	Body       string
	Attributes Attributes
	Timestamp  time.Time
	Continuous bool
}

// Output contains the normalized products of one collection result.
type Output struct {
	Metrics []MetricPoint
	Logs    []LogRecord
}

// Options controls metric/log transformation behavior.
type Options struct {
	// Continuous marks textual metrics as always exportable by the future log
	// exporter. It mirrors the legacy "continua" configuration flag.
	Continuous bool
}

// FromCollection transforms one successful latestData collection into numeric
// gauges and textual logs. Key fields identify series and are never emitted as
// metrics.
func FromCollection(result collect.Result, opts Options) Output {
	keys := keySet(result.Metadata.Keys)
	out := Output{}

	for _, item := range result.LatestData.Items {
		seriesID := buildSeriesID(result.Job.Target.ID, result.Metadata.Keys, item)
		attrs := BuildAttributes(result.Job.Target, result.Metadata, item)

		for _, metricName := range sortedItemFields(item) {
			rawValue := item[metricName]
			if _, isKey := keys[metricName]; isKey {
				continue
			}
			if rawValue == nil {
				continue
			}

			exportName := NormalizeMetricName(result.Metadata.MetricGroupName, metricName)
			if exportName == "" {
				continue
			}

			if number, ok := numericValue(rawValue, result.Metadata, metricName); ok {
				out.Metrics = append(out.Metrics, MetricPoint{
					Name:            exportName,
					MetricGroupName: result.Metadata.MetricGroupName,
					MetricName:      metricName,
					TargetID:        result.Job.Target.ID,
					SeriesID:        seriesID,
					Value:           number,
					Attributes:      attrs.Clone(),
					Timestamp:       result.CollectedAt,
				})
				continue
			}

			if text, ok := textualValue(rawValue, result.Metadata, metricName); ok {
				logAttrs := attrs.Clone()
				logAttrs["metric"] = exportName
				out.Logs = append(out.Logs, LogRecord{
					MetricName: exportName,
					TargetID:   result.Job.Target.ID,
					SeriesID:   seriesID,
					Body:       text,
					Attributes: logAttrs,
					Timestamp:  result.CollectedAt,
					Continuous: opts.Continuous,
				})
			}
		}
	}

	return out
}

func sortedItemFields(item map[string]any) []string {
	fields := make([]string, 0, len(item))
	for field := range item {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// NormalizeMetricName applies the legacy metric name shape and the new
// lowercase export contract.
func NormalizeMetricName(groupName, metricName string) string {
	groupName = strings.TrimSpace(groupName)
	metricName = strings.TrimSpace(metricName)
	if groupName == "" || metricName == "" {
		return ""
	}
	name := "oem_" + groupName + "_" + metricName
	name = strings.ReplaceAll(name, " ", "_")
	return strings.ToLower(name)
}

// Clone returns a shallow copy of attributes so each transformed series can be
// safely amended by later pipeline stages.
func (a Attributes) Clone() Attributes {
	if len(a) == 0 {
		return Attributes{}
	}
	out := make(Attributes, len(a))
	for key, value := range a {
		out[key] = value
	}
	return out
}

func keySet(keys []string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		out[key] = struct{}{}
	}
	return out
}

func buildSeriesID(targetID string, keys []string, item map[string]any) string {
	if len(keys) == 0 {
		return targetID
	}
	var b strings.Builder
	b.WriteString(targetID)
	for _, key := range keys {
		b.WriteByte(0)
		b.WriteString(fmt.Sprint(item[key]))
	}
	return b.String()
}

func numericValue(value any, metadata collect.GroupMetadata, metricName string) (float64, bool) {
	dataType, hasDataType := metadata.DataType(metricName)
	if hasDataType {
		if isNumericDataType(dataType) {
			return coerceNumber(value)
		}
		return 0, false
	}
	return coerceNativeNumber(value)
}

func textualValue(value any, metadata collect.GroupMetadata, metricName string) (string, bool) {
	dataType, hasDataType := metadata.DataType(metricName)
	if hasDataType {
		if isNumericDataType(dataType) {
			return "", false
		}
		return fmt.Sprint(value), true
	}
	if text, ok := value.(string); ok {
		return text, true
	}
	return "", false
}

func isNumericDataType(dataType string) bool {
	switch strings.ToUpper(strings.TrimSpace(dataType)) {
	case "NUMBER", "NUMERIC", "INTEGER", "INT", "FLOAT", "DOUBLE":
		return true
	default:
		return false
	}
}

func coerceNumber(value any) (float64, bool) {
	var parsed float64
	var ok bool

	switch v := value.(type) {
	case bool:
		if v {
			parsed, ok = 1, true
		} else {
			parsed, ok = 0, true
		}
	case json.Number:
		parsed, ok = parseFiniteFloat(v.String())
	case float64:
		parsed, ok = v, true
	case float32:
		parsed, ok = float64(v), true
	case int:
		parsed, ok = float64(v), true
	case int8:
		parsed, ok = float64(v), true
	case int16:
		parsed, ok = float64(v), true
	case int32:
		parsed, ok = float64(v), true
	case int64:
		parsed, ok = float64(v), true
	case uint:
		parsed, ok = float64(v), true
	case uint8:
		parsed, ok = float64(v), true
	case uint16:
		parsed, ok = float64(v), true
	case uint32:
		parsed, ok = float64(v), true
	case uint64:
		parsed, ok = float64(v), true
	case string:
		parsed, ok = parseFiniteFloat(strings.TrimSpace(v))
	default:
		return 0, false
	}

	if !ok || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func coerceNativeNumber(value any) (float64, bool) {
	switch value.(type) {
	case string:
		return 0, false
	default:
		return coerceNumber(value)
	}
}

func parseFiniteFloat(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}
