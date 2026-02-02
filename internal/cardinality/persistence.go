package cardinality

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/szibis/metrics-governor/internal/logging"
)

const (
	// metaFileName is the name of the metadata index file.
	metaFileName = "meta.json"
	// trackersDir is the subdirectory for tracker data files.
	trackersDir = "trackers"
	// indexVersion is the current persistence index version.
	indexVersion = 1
	// maxTrackerFileSize is the maximum allowed size for a single tracker file (50MB).
	maxTrackerFileSize = 50 * 1024 * 1024
	// diskSpaceBuffer is the minimum free space to maintain on disk (10MB).
	diskSpaceBuffer = 10 * 1024 * 1024
	// shutdownTimeout is the maximum time to wait for final save on shutdown.
	shutdownTimeout = 30 * time.Second
	// memoryWarningThreshold is the percentage of max memory that triggers a warning.
	memoryWarningThreshold = 0.80
	// memoryEvictionThreshold is the percentage of max memory that triggers eviction.
	memoryEvictionThreshold = 0.95
	// memoryEvictionTarget is the percentage of max memory to reach after eviction.
	memoryEvictionTarget = 0.80
)

// Prometheus metrics for persistence operations.
var (
	persistenceSavesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bloom_persistence_saves_total",
			Help: "Total save operations by status",
		},
		[]string{"status"},
	)

	persistenceLoadsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bloom_persistence_loads_total",
			Help: "Total load operations by status",
		},
		[]string{"status"},
	)

	persistenceCleanupsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "bloom_persistence_cleanups_total",
			Help: "Total cleanup runs",
		},
	)

	persistenceEvictionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bloom_persistence_evictions_total",
			Help: "Trackers removed by reason",
		},
		[]string{"reason"},
	)

	persistenceTrackersTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bloom_persistence_trackers_total",
			Help: "Current tracker count",
		},
	)

	persistenceDirtyTrackers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bloom_persistence_dirty_trackers",
			Help: "Trackers pending save",
		},
	)

	persistenceDiskUsageBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bloom_persistence_disk_usage_bytes",
			Help: "Current disk usage",
		},
	)

	persistenceDiskLimitBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bloom_persistence_disk_limit_bytes",
			Help: "Configured max disk size",
		},
	)

	persistenceMemoryUsageBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bloom_persistence_memory_usage_bytes",
			Help: "Current in-memory usage",
		},
	)

	persistenceMemoryLimitBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bloom_persistence_memory_limit_bytes",
			Help: "Configured max memory",
		},
	)

	persistenceSaveDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "bloom_persistence_save_duration_seconds",
			Help:    "Save operation latency",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
		},
	)

	persistenceLoadDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "bloom_persistence_load_duration_seconds",
			Help:    "Load operation latency",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		},
	)

	persistenceCompressionRatio = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "bloom_persistence_compression_ratio",
			Help:    "Compressed/uncompressed size ratio",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
		},
	)

	persistenceTrackerSizeBytes = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "bloom_persistence_tracker_size_bytes",
			Help:    "Individual tracker sizes",
			Buckets: prometheus.ExponentialBuckets(1024, 2, 20), // 1KB to ~1GB
		},
	)
)

// PersistenceConfig holds configuration for bloom filter persistence.
type PersistenceConfig struct {
	// Enabled enables persistence.
	Enabled bool
	// Path is the directory for persistence files.
	Path string
	// SaveInterval is the interval between periodic saves.
	SaveInterval time.Duration
	// StateTTL is the time after which unused trackers are cleaned up.
	StateTTL time.Duration
	// CleanupInterval is the interval between cleanup runs.
	CleanupInterval time.Duration
	// MaxSize is the maximum disk space for bloom state (bytes).
	MaxSize int64
	// MaxMemory is the maximum memory for in-memory bloom filters (bytes).
	MaxMemory int64
	// Compression enables gzip compression.
	Compression bool
	// CompressionLevel is the gzip compression level (1-9).
	CompressionLevel int
}

// DefaultPersistenceConfig returns default persistence configuration.
func DefaultPersistenceConfig() PersistenceConfig {
	return PersistenceConfig{
		Enabled:          false,
		Path:             "./bloom-state",
		SaveInterval:     30 * time.Second,
		StateTTL:         time.Hour,
		CleanupInterval:  5 * time.Minute,
		MaxSize:          500 * 1024 * 1024, // 500MB
		MaxMemory:        256 * 1024 * 1024, // 256MB
		Compression:      true,
		CompressionLevel: 1, // Fast compression
	}
}

// TrackerMeta holds metadata for a persisted tracker.
type TrackerMeta struct {
	Key         string    `json:"key"`
	Hash        string    `json:"hash"` // SHA256 of key for filename
	Count       int64     `json:"count"`
	LastUpdated time.Time `json:"last_updated"`
	LastSaved   time.Time `json:"last_saved"`
	CreatedAt   time.Time `json:"created_at"`
	Checksum    uint32    `json:"checksum"` // CRC32 of bloom data
	SizeBytes   int64     `json:"size_bytes"`
}

// PersistenceIndex holds index of all persisted trackers.
type PersistenceIndex struct {
	Version  int                     `json:"version"`
	Trackers map[string]*TrackerMeta `json:"trackers"`
}

// TrackerStore manages persistence for multiple trackers.
type TrackerStore struct {
	cfg         PersistenceConfig
	bloomCfg    Config
	index       *PersistenceIndex
	trackers    map[string]*PersistentTracker
	dirty       map[string]bool
	diskUsage   int64
	memoryUsage int64
	mu          sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTrackerStore creates a new tracker store.
func NewTrackerStore(cfg PersistenceConfig, bloomCfg Config) (*TrackerStore, error) {
	// Validate path
	if strings.Contains(cfg.Path, "..") {
		return nil, fmt.Errorf("invalid path: contains '..'")
	}

	// Create directories
	trackersPath := filepath.Join(cfg.Path, trackersDir)
	if err := os.MkdirAll(trackersPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create persistence directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &TrackerStore{
		cfg:      cfg,
		bloomCfg: bloomCfg,
		index: &PersistenceIndex{
			Version:  indexVersion,
			Trackers: make(map[string]*TrackerMeta),
		},
		trackers: make(map[string]*PersistentTracker),
		dirty:    make(map[string]bool),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Update static metrics
	persistenceDiskLimitBytes.Set(float64(cfg.MaxSize))
	persistenceMemoryLimitBytes.Set(float64(cfg.MaxMemory))

	logging.Info("bloom persistence initialized", logging.F(
		"path", cfg.Path,
		"save_interval", cfg.SaveInterval.String(),
		"state_ttl", cfg.StateTTL.String(),
		"max_size", formatBytes(cfg.MaxSize),
		"max_memory", formatBytes(cfg.MaxMemory),
		"compression", cfg.Compression,
	))

	return s, nil
}

// Start starts background goroutines for save and cleanup loops.
func (s *TrackerStore) Start() {
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logging.Error("bloom save loop panic", logging.F("error", fmt.Sprintf("%v", r)))
			}
		}()
		s.saveLoop()
	}()
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logging.Error("bloom cleanup loop panic", logging.F("error", fmt.Sprintf("%v", r)))
			}
		}()
		s.cleanupLoop()
	}()
}

// GetOrCreate gets an existing tracker or creates a new one.
func (s *TrackerStore) GetOrCreate(key string) *PersistentTracker {
	// Validate key
	if !isValidKey(key) {
		logging.Warn("invalid tracker key rejected", logging.F("key", key))
		// Return a non-persistent tracker as fallback
		return &PersistentTracker{
			BloomTracker: NewBloomTracker(s.bloomCfg),
			key:          key,
			store:        nil, // No persistence
			lastUpdated:  time.Now(),
			createdAt:    time.Now(),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already exists
	if t, ok := s.trackers[key]; ok {
		return t
	}

	// Check memory limit before creating
	if s.cfg.MaxMemory > 0 && s.memoryUsage >= s.cfg.MaxMemory {
		s.evictForMemoryUnlocked()
	}

	// Create new tracker
	hash := hashKey(key)
	now := time.Now()

	bt := NewBloomTracker(s.bloomCfg)
	t := &PersistentTracker{
		BloomTracker: bt,
		key:          key,
		hash:         hash,
		store:        s,
		lastUpdated:  now,
		createdAt:    now,
	}

	s.trackers[key] = t
	s.dirty[key] = true

	// Create metadata
	s.index.Trackers[key] = &TrackerMeta{
		Key:         key,
		Hash:        hash,
		Count:       0,
		LastUpdated: now,
		CreatedAt:   now,
	}

	// Update memory tracking
	s.memoryUsage += int64(bt.MemoryUsage())

	// Update metrics
	persistenceTrackersTotal.Set(float64(len(s.trackers)))
	persistenceDirtyTrackers.Set(float64(len(s.dirty)))
	persistenceMemoryUsageBytes.Set(float64(s.memoryUsage))

	return t
}

// MarkDirty marks a tracker as needing to be saved.
func (s *TrackerStore) MarkDirty(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty[key] = true
	if meta, ok := s.index.Trackers[key]; ok {
		meta.LastUpdated = time.Now()
	}
	persistenceDirtyTrackers.Set(float64(len(s.dirty)))
}

// SaveAll persists all dirty trackers to disk.
func (s *TrackerStore) SaveAll() error {
	return s.saveAllWithContext(s.ctx)
}

func (s *TrackerStore) saveAllWithContext(ctx context.Context) error {
	s.mu.Lock()
	dirtyKeys := make([]string, 0, len(s.dirty))
	for key := range s.dirty {
		dirtyKeys = append(dirtyKeys, key)
	}
	s.mu.Unlock()

	if len(dirtyKeys) == 0 {
		return nil
	}

	startTime := time.Now()
	savedCount := 0
	var lastErr error

	for _, key := range dirtyKeys {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := s.saveTracker(key); err != nil {
			logging.Warn("failed to save tracker", logging.F("key", key, "error", err.Error()))
			lastErr = err
			persistenceSavesTotal.WithLabelValues("error").Inc()
		} else {
			savedCount++
			persistenceSavesTotal.WithLabelValues("success").Inc()
		}
	}

	// Save index
	if err := s.saveIndex(); err != nil {
		logging.Warn("failed to save persistence index", logging.F("error", err.Error()))
		lastErr = err
	}

	duration := time.Since(startTime)
	persistenceSaveDuration.Observe(duration.Seconds())

	s.mu.RLock()
	diskUsage := s.diskUsage
	s.mu.RUnlock()

	logging.Info("bloom state saved", logging.F(
		"saved", savedCount,
		"duration_ms", duration.Milliseconds(),
		"disk_usage_bytes", diskUsage,
		"disk_limit_bytes", s.cfg.MaxSize,
	))

	return lastErr
}

func (s *TrackerStore) saveTracker(key string) error {
	s.mu.RLock()
	t, ok := s.trackers[key]
	meta, metaOk := s.index.Trackers[key]
	s.mu.RUnlock()

	if !ok || !metaOk {
		return nil
	}

	// Check disk space
	if !s.hasEnoughDiskSpace(int64(t.MemoryUsage())) {
		return fmt.Errorf("insufficient disk space")
	}

	// Prepare file path
	ext := ".bloom"
	if s.cfg.Compression {
		ext = ".bloom.gz"
	}
	trackersPath := filepath.Join(s.cfg.Path, trackersDir)
	finalPath := filepath.Join(trackersPath, meta.Hash+ext)
	tmpPath := finalPath + ".tmp"

	// Write to temp file
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	var gzWriter *gzip.Writer
	crcWriter := crc32.NewIEEE()
	multiWriter := io.MultiWriter(tmpFile, crcWriter)
	var w io.Writer = multiWriter

	if s.cfg.Compression {
		gzWriter, err = gzip.NewWriterLevel(multiWriter, s.cfg.CompressionLevel)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("failed to create gzip writer: %w", err)
		}
		w = gzWriter
	}

	// Get uncompressed size for ratio calculation
	uncompressedSize := int64(t.MemoryUsage())

	// Write bloom filter data
	t.mu.RLock()
	_, err = t.filter.WriteTo(w)
	count := t.count
	t.mu.RUnlock()

	if err != nil {
		if gzWriter != nil {
			gzWriter.Close()
		}
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write bloom filter: %w", err)
	}

	if gzWriter != nil {
		if err := gzWriter.Close(); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("failed to close gzip writer: %w", err)
		}
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Get file size
	fileInfo, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to stat temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Update metadata
	s.mu.Lock()
	now := time.Now()
	meta.Count = count
	meta.LastSaved = now
	meta.LastUpdated = t.lastUpdated
	meta.Checksum = crcWriter.Sum32()
	meta.SizeBytes = fileInfo.Size()

	// Update disk usage
	s.diskUsage += fileInfo.Size()
	delete(s.dirty, key)

	persistenceDirtyTrackers.Set(float64(len(s.dirty)))
	persistenceDiskUsageBytes.Set(float64(s.diskUsage))
	s.mu.Unlock()

	// Record metrics
	persistenceTrackerSizeBytes.Observe(float64(fileInfo.Size()))
	if uncompressedSize > 0 {
		persistenceCompressionRatio.Observe(float64(fileInfo.Size()) / float64(uncompressedSize))
	}

	return nil
}

func (s *TrackerStore) saveIndex() error {
	s.mu.RLock()
	indexCopy := &PersistenceIndex{
		Version:  s.index.Version,
		Trackers: make(map[string]*TrackerMeta),
	}
	for k, v := range s.index.Trackers {
		metaCopy := *v
		indexCopy.Trackers[k] = &metaCopy
	}
	s.mu.RUnlock()

	indexPath := filepath.Join(s.cfg.Path, metaFileName)
	tmpPath := indexPath + ".tmp"

	data, err := json.MarshalIndent(indexCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp index: %w", err)
	}

	if err := os.Rename(tmpPath, indexPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp index: %w", err)
	}

	return nil
}

// LoadAll restores trackers from disk on startup.
func (s *TrackerStore) LoadAll() error {
	startTime := time.Now()
	logging.Info("loading bloom state from disk", logging.F("path", s.cfg.Path))

	// Load index
	indexPath := filepath.Join(s.cfg.Path, metaFileName)
	data, err := os.ReadFile(indexPath)
	if os.IsNotExist(err) {
		logging.Info("no existing bloom state found, starting fresh")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read index: %w", err)
	}

	var index PersistenceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("failed to parse index: %w", err)
	}

	// Validate version
	if index.Version != indexVersion {
		logging.Warn("index version mismatch, starting fresh", logging.F(
			"found_version", index.Version,
			"expected_version", indexVersion,
		))
		return nil
	}

	loadedCount := 0
	skippedStale := 0
	errorCount := 0
	now := time.Now()

	for key, meta := range index.Trackers {
		// Check if stale
		age := now.Sub(meta.LastUpdated)
		if age > s.cfg.StateTTL {
			logging.Info("skipped stale tracker", logging.F(
				"key", key,
				"age", age.String(),
				"ttl", s.cfg.StateTTL.String(),
			))
			skippedStale++
			persistenceEvictionsTotal.WithLabelValues("ttl").Inc()
			continue
		}

		if err := s.loadTracker(meta); err != nil {
			logging.Warn("failed to load tracker", logging.F(
				"key", key,
				"error", err.Error(),
				"action", "skipped",
			))
			errorCount++
			persistenceLoadsTotal.WithLabelValues("error").Inc()
			continue
		}

		logging.Info("loaded tracker", logging.F(
			"key", key,
			"count", meta.Count,
			"age", age.String(),
		))
		loadedCount++
		persistenceLoadsTotal.WithLabelValues("success").Inc()
	}

	// Clean up orphaned files
	s.cleanOrphanedFiles()

	duration := time.Since(startTime)
	persistenceLoadDuration.Observe(duration.Seconds())

	logging.Info("bloom state restore complete", logging.F(
		"loaded", loadedCount,
		"skipped_stale", skippedStale,
		"errors", errorCount,
		"duration_ms", duration.Milliseconds(),
	))

	return nil
}

func (s *TrackerStore) loadTracker(meta *TrackerMeta) error {
	// Try compressed file first, then uncompressed
	trackersPath := filepath.Join(s.cfg.Path, trackersDir)
	gzPath := filepath.Join(trackersPath, meta.Hash+".bloom.gz")
	rawPath := filepath.Join(trackersPath, meta.Hash+".bloom")

	var filePath string
	var compressed bool

	if _, err := os.Stat(gzPath); err == nil {
		filePath = gzPath
		compressed = true
	} else if _, err := os.Stat(rawPath); err == nil {
		filePath = rawPath
		compressed = false
	} else {
		return fmt.Errorf("tracker file not found")
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	if fileInfo.Size() > maxTrackerFileSize {
		return fmt.Errorf("tracker file too large: %d > %d", fileInfo.Size(), maxTrackerFileSize)
	}

	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var r io.Reader = file
	if compressed {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		r = gzReader
	}

	// Create tracker and read data
	bt := NewBloomTracker(s.bloomCfg)
	bt.mu.Lock()
	_, err = bt.filter.ReadFrom(r)
	bt.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to read bloom filter: %w", err)
	}

	// Create persistent tracker
	t := &PersistentTracker{
		BloomTracker: bt,
		key:          meta.Key,
		hash:         meta.Hash,
		store:        s,
		lastUpdated:  meta.LastUpdated,
		createdAt:    meta.CreatedAt,
	}

	// Restore count from metadata (bloom filter doesn't store count)
	bt.mu.Lock()
	bt.count = meta.Count
	bt.mu.Unlock()

	// Add to store
	s.mu.Lock()
	s.trackers[meta.Key] = t
	s.index.Trackers[meta.Key] = meta
	s.diskUsage += fileInfo.Size()
	s.memoryUsage += int64(bt.MemoryUsage())
	s.mu.Unlock()

	// Update metrics
	persistenceTrackersTotal.Set(float64(len(s.trackers)))
	persistenceDiskUsageBytes.Set(float64(s.diskUsage))
	persistenceMemoryUsageBytes.Set(float64(s.memoryUsage))

	return nil
}

func (s *TrackerStore) cleanOrphanedFiles() {
	trackersPath := filepath.Join(s.cfg.Path, trackersDir)
	entries, err := os.ReadDir(trackersPath)
	if err != nil {
		return
	}

	s.mu.RLock()
	validHashes := make(map[string]bool)
	for _, meta := range s.index.Trackers {
		validHashes[meta.Hash] = true
	}
	s.mu.RUnlock()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Extract hash from filename
		hash := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".bloom")
		if !validHashes[hash] {
			filePath := filepath.Join(trackersPath, name)
			os.Remove(filePath)
			logging.Info("removed orphaned file", logging.F("file", name))
		}
	}
}

// Cleanup removes stale trackers and evicts old entries if over disk limit.
func (s *TrackerStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	persistenceCleanupsTotal.Inc()

	now := time.Now()
	evictedTTL := 0
	evictedSize := 0

	// First pass: remove stale trackers
	for key, meta := range s.index.Trackers {
		age := now.Sub(meta.LastUpdated)
		if age > s.cfg.StateTTL {
			s.removeTrackerUnlocked(key, "ttl")
			evictedTTL++
		}
	}

	// Second pass: evict oldest if over disk limit
	for s.diskUsage > s.cfg.MaxSize && len(s.trackers) > 0 {
		oldest := s.findOldestTrackerUnlocked()
		if oldest == "" {
			break
		}
		s.removeTrackerUnlocked(oldest, "size")
		evictedSize++
	}

	logging.Info("bloom state cleanup complete", logging.F(
		"evicted_ttl", evictedTTL,
		"evicted_size", evictedSize,
		"remaining", len(s.trackers),
		"disk_usage_bytes", s.diskUsage,
	))
}

func (s *TrackerStore) removeTrackerUnlocked(key, reason string) {
	meta, ok := s.index.Trackers[key]
	if !ok {
		return
	}

	// Remove from disk
	trackersPath := filepath.Join(s.cfg.Path, trackersDir)
	os.Remove(filepath.Join(trackersPath, meta.Hash+".bloom.gz"))
	os.Remove(filepath.Join(trackersPath, meta.Hash+".bloom"))

	// Update disk usage
	s.diskUsage -= meta.SizeBytes

	// Update memory usage
	if t, ok := s.trackers[key]; ok {
		s.memoryUsage -= int64(t.MemoryUsage())
	}

	// Remove from maps
	delete(s.trackers, key)
	delete(s.index.Trackers, key)
	delete(s.dirty, key)

	// Update metrics
	persistenceEvictionsTotal.WithLabelValues(reason).Inc()
	persistenceTrackersTotal.Set(float64(len(s.trackers)))
	persistenceDiskUsageBytes.Set(float64(s.diskUsage))
	persistenceMemoryUsageBytes.Set(float64(s.memoryUsage))

	logging.Info("evicted tracker", logging.F(
		"key", key,
		"reason", reason,
	))
}

func (s *TrackerStore) findOldestTrackerUnlocked() string {
	var oldestKey string
	var oldestTime time.Time

	for key, meta := range s.index.Trackers {
		if oldestKey == "" || meta.LastUpdated.Before(oldestTime) {
			oldestKey = key
			oldestTime = meta.LastUpdated
		}
	}

	return oldestKey
}

func (s *TrackerStore) evictForMemoryUnlocked() {
	// Sort trackers by LastUpdated (oldest first)
	type trackerAge struct {
		key         string
		lastUpdated time.Time
		memoryUsage int64
	}

	ages := make([]trackerAge, 0, len(s.trackers))
	for key, t := range s.trackers {
		meta := s.index.Trackers[key]
		if meta != nil {
			ages = append(ages, trackerAge{
				key:         key,
				lastUpdated: meta.LastUpdated,
				memoryUsage: int64(t.MemoryUsage()),
			})
		}
	}

	sort.Slice(ages, func(i, j int) bool {
		return ages[i].lastUpdated.Before(ages[j].lastUpdated)
	})

	targetUsage := int64(float64(s.cfg.MaxMemory) * memoryEvictionTarget)
	evictedCount := 0

	for _, age := range ages {
		if s.memoryUsage <= targetUsage {
			break
		}
		s.removeTrackerUnlocked(age.key, "memory")
		evictedCount++
	}

	if evictedCount > 0 {
		logging.Error("bloom memory limit reached, evicting", logging.F(
			"usage_bytes", s.memoryUsage,
			"limit_bytes", s.cfg.MaxMemory,
			"evicted", evictedCount,
		))
	}
}

// CheckMemoryPressure checks memory usage and takes action if needed.
func (s *TrackerStore) CheckMemoryPressure() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cfg.MaxMemory <= 0 {
		return
	}

	usageRatio := float64(s.memoryUsage) / float64(s.cfg.MaxMemory)

	if usageRatio >= memoryEvictionThreshold {
		logging.Error("bloom memory limit reached, evicting", logging.F(
			"usage_bytes", s.memoryUsage,
			"limit_bytes", s.cfg.MaxMemory,
			"usage_pct", int(usageRatio*100),
		))
		s.evictForMemoryUnlocked()
	} else if usageRatio >= memoryWarningThreshold {
		logging.Warn("bloom memory usage high", logging.F(
			"usage_bytes", s.memoryUsage,
			"limit_bytes", s.cfg.MaxMemory,
			"usage_pct", int(usageRatio*100),
		))
		// Trigger early cleanup of stale trackers
		now := time.Now()
		for key, meta := range s.index.Trackers {
			age := now.Sub(meta.LastUpdated)
			if age > s.cfg.StateTTL/2 { // More aggressive cleanup
				s.removeTrackerUnlocked(key, "memory_pressure")
			}
		}
	}
}

// DiskUsage returns current disk usage in bytes.
func (s *TrackerStore) DiskUsage() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.diskUsage
}

// MemoryUsage returns current memory usage in bytes.
func (s *TrackerStore) MemoryUsage() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memoryUsage
}

// TrackerCount returns the number of active trackers.
func (s *TrackerStore) TrackerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.trackers)
}

// Close stops background tasks and performs a final save.
func (s *TrackerStore) Close() error {
	s.cancel()

	// Wait for background tasks with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		logging.Warn("bloom persistence shutdown timeout, continuing")
	}

	// Final save
	s.mu.RLock()
	dirtyCount := len(s.dirty)
	totalCount := len(s.trackers)
	s.mu.RUnlock()

	logging.Info("bloom persistence shutting down", logging.F(
		"dirty_trackers", dirtyCount,
	))

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	startTime := time.Now()
	savedCount := 0

	s.mu.Lock()
	dirtyKeys := make([]string, 0, len(s.dirty))
	for key := range s.dirty {
		dirtyKeys = append(dirtyKeys, key)
	}
	s.mu.Unlock()

	for _, key := range dirtyKeys {
		select {
		case <-ctx.Done():
			logging.Warn("shutdown timeout during save")
			break
		default:
		}
		if err := s.saveTracker(key); err == nil {
			savedCount++
		}
	}

	if err := s.saveIndex(); err != nil {
		logging.Warn("failed to save final index", logging.F("error", err.Error()))
	}

	duration := time.Since(startTime)
	logging.Info("final bloom state saved", logging.F(
		"saved", savedCount,
		"total", totalCount,
		"duration_ms", duration.Milliseconds(),
	))

	logging.Info("bloom persistence stopped")
	return nil
}

func (s *TrackerStore) saveLoop() {
	ticker := time.NewTicker(s.cfg.SaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			dirtyCount := len(s.dirty)
			s.mu.RUnlock()

			if dirtyCount > 0 {
				if err := s.SaveAll(); err != nil {
					logging.Warn("save loop error", logging.F("error", err.Error()))
				}
			}
		}
	}
}

func (s *TrackerStore) cleanupLoop() {
	ticker := time.NewTicker(s.cfg.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.Cleanup()
			s.CheckMemoryPressure()
		}
	}
}

func (s *TrackerStore) hasEnoughDiskSpace(needed int64) bool {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.Path, &stat); err != nil {
		// Can't check, assume OK
		return true
	}
	available := int64(stat.Bavail) * int64(stat.Bsize)
	return available > needed+diskSpaceBuffer
}

// Helper functions

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:16]) // Use first 16 bytes (32 hex chars)
}

func isValidKey(key string) bool {
	if key == "" {
		return false
	}
	if strings.Contains(key, "..") {
		return false
	}
	if strings.ContainsAny(key, "\x00\n\r") {
		return false
	}
	return true
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
