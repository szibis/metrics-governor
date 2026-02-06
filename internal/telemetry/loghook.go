package telemetry

import (
	"context"
	"fmt"

	"github.com/szibis/metrics-governor/internal/logging"
	otellog "go.opentelemetry.io/otel/log"
)

// NewLogHook returns a logging.LogHook that emits log records via the OTEL log SDK.
// Returns nil if telemetry is not enabled.
func (t *Telemetry) NewLogHook() logging.LogHook {
	if t == nil || t.logger == nil {
		return nil
	}

	logger := t.logger

	return func(level logging.Level, msg string, attrs map[string]interface{}) {
		var record otellog.Record

		record.SetBody(otellog.StringValue(msg))
		record.SetSeverity(toOTELSeverity(level))
		record.SetSeverityText(string(level))

		if len(attrs) > 0 {
			kvs := make([]otellog.KeyValue, 0, len(attrs))
			for k, v := range attrs {
				kvs = append(kvs, otellog.KeyValue{
					Key:   k,
					Value: toOTELValue(v),
				})
			}
			record.AddAttributes(kvs...)
		}

		logger.Emit(context.Background(), record)
	}
}

func toOTELSeverity(level logging.Level) otellog.Severity {
	switch level {
	case logging.LevelInfo:
		return otellog.SeverityInfo
	case logging.LevelWarn:
		return otellog.SeverityWarn
	case logging.LevelError:
		return otellog.SeverityError
	case logging.LevelFatal:
		return otellog.SeverityFatal
	default:
		return otellog.SeverityInfo
	}
}

func toOTELValue(v interface{}) otellog.Value {
	if v == nil {
		return otellog.StringValue("<nil>")
	}
	switch val := v.(type) {
	case string:
		return otellog.StringValue(val)
	case int:
		return otellog.IntValue(val)
	case int64:
		return otellog.Int64Value(val)
	case float64:
		return otellog.Float64Value(val)
	case bool:
		return otellog.BoolValue(val)
	default:
		return otellog.StringValue(fmt.Sprint(val))
	}
}
