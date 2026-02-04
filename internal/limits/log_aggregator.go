package limits

import (
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
)

// maxLogAggregatorEntries caps the number of unique log entries to prevent unbounded growth.
const maxLogAggregatorEntries = 10000

// LogAggregator aggregates similar log messages and outputs them periodically.
type LogAggregator struct {
	mu            sync.Mutex
	entries       map[string]*aggregatedEntry
	dropped       int64
	flushInterval time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// aggregatedEntry represents an aggregated log entry.
type aggregatedEntry struct {
	level     string
	message   string
	fields    map[string]interface{}
	count     int64
	firstSeen time.Time
	lastSeen  time.Time
	totalDPs  int64 // Accumulated datapoints for this entry type
}

// NewLogAggregator creates a new log aggregator with the specified flush interval.
func NewLogAggregator(flushInterval time.Duration) *LogAggregator {
	if flushInterval <= 0 {
		flushInterval = 10 * time.Second
	}
	la := &LogAggregator{
		entries:       make(map[string]*aggregatedEntry),
		flushInterval: flushInterval,
		stopCh:        make(chan struct{}),
	}
	la.wg.Add(1)
	go la.flushLoop()
	return la
}

// Stop stops the log aggregator and flushes any remaining entries.
func (la *LogAggregator) Stop() {
	close(la.stopCh)
	la.wg.Wait()
	la.flush() // Final flush
}

// Warn aggregates a warning log message.
func (la *LogAggregator) Warn(key string, message string, fields map[string]interface{}, datapoints int64) {
	la.add("warn", key, message, fields, datapoints)
}

// Info aggregates an info log message.
func (la *LogAggregator) Info(key string, message string, fields map[string]interface{}, datapoints int64) {
	la.add("info", key, message, fields, datapoints)
}

// Error aggregates an error log message.
func (la *LogAggregator) Error(key string, message string, fields map[string]interface{}, datapoints int64) {
	la.add("error", key, message, fields, datapoints)
}

func (la *LogAggregator) add(level, key, message string, fields map[string]interface{}, datapoints int64) {
	la.mu.Lock()
	defer la.mu.Unlock()

	now := time.Now()
	if entry, ok := la.entries[key]; ok {
		entry.count++
		entry.lastSeen = now
		entry.totalDPs += datapoints
	} else {
		// Cap entries to prevent unbounded growth between flushes
		if len(la.entries) >= maxLogAggregatorEntries {
			la.dropped++
			return
		}
		la.entries[key] = &aggregatedEntry{
			level:     level,
			message:   message,
			fields:    fields,
			count:     1,
			firstSeen: now,
			lastSeen:  now,
			totalDPs:  datapoints,
		}
	}
}

func (la *LogAggregator) flushLoop() {
	defer la.wg.Done()
	ticker := time.NewTicker(la.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			la.flush()
		case <-la.stopCh:
			return
		}
	}
}

func (la *LogAggregator) flush() {
	la.mu.Lock()
	entries := la.entries
	dropped := la.dropped
	la.entries = make(map[string]*aggregatedEntry)
	la.dropped = 0
	la.mu.Unlock()

	if dropped > 0 {
		logging.Warn("log aggregator dropped entries due to size cap", map[string]interface{}{
			"dropped": dropped,
			"max":     maxLogAggregatorEntries,
		})
	}

	for _, entry := range entries {
		// Create a copy of fields with aggregation info
		fields := make(map[string]interface{})
		for k, v := range entry.fields {
			fields[k] = v
		}
		fields["occurrences"] = entry.count
		fields["total_datapoints"] = entry.totalDPs
		if entry.count > 1 {
			fields["first_seen"] = entry.firstSeen.Format(time.RFC3339)
			fields["last_seen"] = entry.lastSeen.Format(time.RFC3339)
		}

		switch entry.level {
		case "warn":
			logging.Warn(entry.message, fields)
		case "info":
			logging.Info(entry.message, fields)
		case "error":
			logging.Error(entry.message, fields)
		}
	}
}
