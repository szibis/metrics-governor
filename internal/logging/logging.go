package logging

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

// levelOrder defines the numeric ordering for level comparison.
var levelOrder = map[Level]int{
	LevelInfo:  0,
	LevelWarn:  1,
	LevelError: 2,
	LevelFatal: 3,
}

// ParseLevel converts a string to a Level. Accepts INFO, WARN, ERROR, FATAL (case-insensitive).
// Returns LevelInfo for unrecognized values.
func ParseLevel(s string) Level {
	switch strings.ToUpper(s) {
	case "INFO":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "FATAL":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// Logger provides JSON structured logging in OTEL-compatible format.
type Logger struct {
	mu       sync.Mutex
	output   io.Writer
	resource map[string]string
	hook     LogHook
	minLevel Level // minimum level for output; metrics are always emitted
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

var (
	logMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_log_messages_total",
		Help: "Total number of log messages by severity level, component, and operation",
	}, []string{"level", "component", "operation"})

	defaultLogger = &Logger{output: os.Stdout}
)

// operationKeywords maps message keywords to operation categories.
// Used for auto-detection when callers don't provide an explicit "operation" attr.
var operationKeywords = []struct {
	keyword   string
	operation string
}{
	{"circuit breaker", "circuit_breaker"},
	{"circuit", "circuit_breaker"},
	{"retry", "retry"},
	{"backoff", "backoff"},
	{"drain", "drain"},
	{"export", "export"},
	{"queue", "queue"},
	{"receive", "receive"},
	{"decompress", "receive"},
	{"unmarshal", "receive"},
	{"config", "config"},
	{"reload", "config"},
	{"limit", "limits"},
	{"bloom", "cardinality"},
	{"cardinality", "cardinality"},
	{"sharding", "sharding"},
	{"startup", "lifecycle"},
	{"shutdown", "lifecycle"},
	{"started", "lifecycle"},
}

func init() {
	prometheus.MustRegister(logMessagesTotal)
	// Pre-initialize common combinations so they appear in /metrics immediately.
	for _, level := range []string{"INFO", "WARN", "ERROR", "FATAL"} {
		logMessagesTotal.WithLabelValues(level, "general", "general").Add(0)
	}
}

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

// SetLevel sets the minimum log level for output. Messages below this level
// are suppressed from output and hooks, but the Prometheus counter
// (metrics_governor_log_messages_total) is still incremented for all levels.
// This allows dashboards to have full visibility regardless of log verbosity.
func SetLevel(level Level) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.minLevel = level
}

// GetLevel returns the current minimum log level.
func GetLevel() Level {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	if defaultLogger.minLevel == "" {
		return LevelInfo
	}
	return defaultLogger.minLevel
}

// callerComponent returns the package name of the caller (2 frames up from log()).
// Returns "general" if detection fails.
func callerComponent() string {
	_, file, _, ok := runtime.Caller(3) // 3 frames: callerComponent -> log -> Info/Warn/Error -> caller
	if !ok {
		return "general"
	}
	dir := filepath.Base(filepath.Dir(file))
	if dir == "" || dir == "." {
		return "general"
	}
	return dir
}

// detectOperation returns an operation category by matching keywords in the message.
func detectOperation(msg string) string {
	lower := strings.ToLower(msg)
	for _, kw := range operationKeywords {
		if strings.Contains(lower, kw.keyword) {
			return kw.operation
		}
	}
	return "general"
}

// log writes a structured log entry in OTEL-compatible JSON format.
func (l *Logger) log(level Level, msg string, attrs map[string]interface{}) {
	// Determine component: explicit attr > auto-detect from caller package
	component := "general"
	operation := ""
	if attrs != nil {
		if c, ok := attrs["component"].(string); ok && c != "" {
			component = c
		}
		if o, ok := attrs["operation"].(string); ok && o != "" {
			operation = o
		}
	}
	if component == "general" {
		component = callerComponent()
	}
	if operation == "" {
		operation = detectOperation(msg)
	}
	logMessagesTotal.WithLabelValues(string(level), component, operation).Inc()

	// Check minimum level — suppress output but metrics are already counted above
	l.mu.Lock()
	minLevel := l.minLevel
	l.mu.Unlock()
	if minLevel != "" && levelOrder[level] < levelOrder[minLevel] {
		return
	}

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
