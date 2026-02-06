package logging

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents log severity level.
type Level string

const (
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
	LevelFatal Level = "FATAL"
)

// severityNumbers maps OTEL severity text to OTEL severity number.
// See https://opentelemetry.io/docs/specs/otel/logs/data-model/#severity-fields
var severityNumbers = map[Level]int{
	LevelInfo:  9,  // INFO
	LevelWarn:  13, // WARN
	LevelError: 17, // ERROR
	LevelFatal: 21, // FATAL
}

// SeverityNumber returns the OTEL severity number for a level.
func SeverityNumber(level Level) int {
	return severityNumbers[level]
}

// LogHook is called for every log entry, allowing secondary log sinks
// (e.g., OTLP log export) without the logging package importing them.
type LogHook func(level Level, msg string, attrs map[string]interface{})

// Logger provides JSON structured logging in OTEL-compatible format.
type Logger struct {
	mu       sync.Mutex
	output   io.Writer
	resource map[string]string
	hook     LogHook
}

// LogEntry represents a single log entry in OTEL-compatible JSON format.
type LogEntry struct {
	Timestamp      string                 `json:"Timestamp"`
	SeverityText   string                 `json:"SeverityText"`
	SeverityNumber int                    `json:"SeverityNumber"`
	Body           string                 `json:"Body"`
	Attributes     map[string]interface{} `json:"Attributes,omitempty"`
	Resource       map[string]string      `json:"Resource,omitempty"`
}

var defaultLogger = &Logger{output: os.Stdout}

// SetOutput sets the output writer for the default logger.
func SetOutput(w io.Writer) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.output = w
}

// SetResource sets the OTEL resource attributes (service.name, service.version, etc.)
// for the default logger. Should be called once at startup.
func SetResource(resource map[string]string) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.resource = resource
}

// SetHook registers a hook that is called for every log entry.
// Used by the telemetry package to forward logs via OTLP.
func SetHook(hook LogHook) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.hook = hook
}

// log writes a structured log entry in OTEL-compatible JSON format.
func (l *Logger) log(level Level, msg string, attrs map[string]interface{}) {
	entry := LogEntry{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		SeverityText:   string(level),
		SeverityNumber: severityNumbers[level],
		Body:           msg,
		Attributes:     attrs,
	}

	l.mu.Lock()
	if l.resource != nil {
		entry.Resource = l.resource
	}
	hook := l.hook
	data, _ := json.Marshal(entry)
	_, _ = l.output.Write(data)
	_, _ = l.output.Write([]byte("\n"))
	l.mu.Unlock()

	// Call hook outside the lock to avoid deadlocks
	if hook != nil {
		hook(level, msg, attrs)
	}
}

// Info logs an info level message.
func Info(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	defaultLogger.log(LevelInfo, msg, f)
}

// Warn logs a warning level message.
func Warn(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	defaultLogger.log(LevelWarn, msg, f)
}

// Error logs an error level message.
func Error(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	defaultLogger.log(LevelError, msg, f)
}

// Fatal logs a fatal level message and exits.
func Fatal(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	defaultLogger.log(LevelFatal, msg, f)
	os.Exit(1)
}

// F is a helper to create fields map.
func F(keyvals ...interface{}) map[string]interface{} {
	fields := make(map[string]interface{})
	for i := 0; i < len(keyvals)-1; i += 2 {
		if key, ok := keyvals[i].(string); ok {
			fields[key] = keyvals[i+1]
		}
	}
	return fields
}
