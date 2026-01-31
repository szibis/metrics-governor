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
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
	LevelFatal Level = "fatal"
)

// Logger provides JSON structured logging.
type Logger struct {
	mu     sync.Mutex
	output io.Writer
}

// LogEntry represents a single log entry.
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     Level                  `json:"level"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

var defaultLogger = &Logger{output: os.Stdout}

// SetOutput sets the output writer for the default logger.
func SetOutput(w io.Writer) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.output = w
}

// log writes a structured log entry.
func (l *Logger) log(level Level, msg string, fields map[string]interface{}) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Message:   msg,
		Fields:    fields,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	data, _ := json.Marshal(entry)
	_, _ = l.output.Write(data)
	_, _ = l.output.Write([]byte("\n"))
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
