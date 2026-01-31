package queue

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	walFileName    = "queue.wal"
	indexFileName  = "queue.idx"
	walMagic       = 0x51574C00 // "QWL\0"
	walVersion     = 1
	headerSize     = 16 // magic(4) + length(4) + crc(4) + flags(4)
	indexEntrySize = 24 // offset(8) + length(4) + timestamp(8) + flags(4)

	flagActive   = 0x01
	flagConsumed = 0x02
)

var (
	ErrCorrupted   = errors.New("WAL is corrupted")
	ErrDiskFull    = errors.New("disk is full")
	ErrQueueFull   = errors.New("queue is full")
	ErrClosed      = errors.New("WAL is closed")
	crc32Table     = crc32.MakeTable(crc32.Castagnoli)
)

// WALEntry represents an entry in the write-ahead log.
type WALEntry struct {
	Offset    int64
	Length    uint32
	Timestamp time.Time
	Flags     uint32
	Data      []byte
	Retries   int
}

// WAL provides a write-ahead log for durable queue storage.
type WAL struct {
	mu sync.RWMutex

	path      string
	walFile   *os.File
	idxFile   *os.File
	walWriter *bufio.Writer

	entries     []*WALEntry
	activeCount int
	totalBytes  int64

	// Configuration
	maxSize            int
	maxBytes           int64
	targetUtilization  float64 // Target 80-90% utilization
	compactThreshold   float64 // Compact when consumed > this ratio
	adaptiveEnabled    bool

	// Adaptive limits
	effectiveMaxSize  int
	effectiveMaxBytes int64

	closed bool
}

// WALConfig holds WAL configuration.
type WALConfig struct {
	Path              string
	MaxSize           int
	MaxBytes          int64
	TargetUtilization float64 // Default 0.85 (85%)
	CompactThreshold  float64 // Default 0.5 (50% consumed)
	AdaptiveEnabled   bool    // Enable adaptive sizing
}

// NewWAL creates a new write-ahead log.
func NewWAL(cfg WALConfig) (*WAL, error) {
	if cfg.TargetUtilization <= 0 || cfg.TargetUtilization > 1 {
		cfg.TargetUtilization = 0.85
	}
	if cfg.CompactThreshold <= 0 || cfg.CompactThreshold > 1 {
		cfg.CompactThreshold = 0.5
	}

	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	w := &WAL{
		path:              cfg.Path,
		maxSize:           cfg.MaxSize,
		maxBytes:          cfg.MaxBytes,
		targetUtilization: cfg.TargetUtilization,
		compactThreshold:  cfg.CompactThreshold,
		adaptiveEnabled:   cfg.AdaptiveEnabled,
		entries:           make([]*WALEntry, 0),
	}

	// Set capacity metrics
	SetCapacityMetrics(cfg.MaxSize, cfg.MaxBytes)

	// Calculate initial effective limits
	w.updateEffectiveLimits()

	// Open or create WAL file
	walPath := filepath.Join(cfg.Path, walFileName)
	walFile, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file: %w", err)
	}
	w.walFile = walFile
	w.walWriter = bufio.NewWriterSize(walFile, 64*1024) // 64KB buffer

	// Open or create index file
	idxPath := filepath.Join(cfg.Path, indexFileName)
	idxFile, err := os.OpenFile(idxPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		walFile.Close()
		return nil, fmt.Errorf("failed to open index file: %w", err)
	}
	w.idxFile = idxFile

	// Recover existing entries
	if err := w.recover(); err != nil {
		w.Close()
		return nil, fmt.Errorf("failed to recover WAL: %w", err)
	}

	return w, nil
}

// Append adds data to the WAL.
func (w *WAL) Append(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	dataLen := uint32(len(data))

	// Check limits (use effective limits for adaptive sizing)
	if w.activeCount >= w.effectiveMaxSize {
		return ErrQueueFull
	}
	if w.effectiveMaxBytes > 0 && w.totalBytes+int64(dataLen) > w.effectiveMaxBytes {
		return ErrQueueFull
	}

	// Get current write position
	offset, err := w.walFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek WAL: %w", err)
	}

	// Calculate CRC
	crc := crc32.Checksum(data, crc32Table)

	// Write header
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[0:4], walMagic)
	binary.LittleEndian.PutUint32(header[4:8], dataLen)
	binary.LittleEndian.PutUint32(header[8:12], crc)
	binary.LittleEndian.PutUint32(header[12:16], flagActive)

	// Write to buffered writer
	if _, err := w.walWriter.Write(header); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return fmt.Errorf("failed to write header: %w", err)
	}

	if _, err := w.walWriter.Write(data); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return fmt.Errorf("failed to write data: %w", err)
	}

	// Flush to ensure durability
	if err := w.walWriter.Flush(); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return fmt.Errorf("failed to flush WAL: %w", err)
	}

	// Sync to disk
	if err := w.walFile.Sync(); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return fmt.Errorf("failed to sync WAL: %w", err)
	}

	// Create entry
	entry := &WALEntry{
		Offset:    offset,
		Length:    dataLen,
		Timestamp: time.Now(),
		Flags:     flagActive,
		Retries:   0,
	}

	// Write index entry
	if err := w.writeIndexEntry(entry); err != nil {
		return err
	}

	w.entries = append(w.entries, entry)
	w.activeCount++
	w.totalBytes += int64(dataLen) + headerSize

	IncrementWALWrite()
	w.updateMetrics()

	// Check if compaction is needed
	w.maybeCompact()

	return nil
}

// Peek returns the oldest active entry without removing it.
func (w *WAL) Peek() (*WALEntry, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.closed {
		return nil, ErrClosed
	}

	for _, entry := range w.entries {
		if entry.Flags&flagActive != 0 && entry.Flags&flagConsumed == 0 {
			// Read data from WAL
			data, err := w.readEntryData(entry)
			if err != nil {
				return nil, err
			}
			entry.Data = data
			return entry, nil
		}
	}
	return nil, nil
}

// MarkConsumed marks an entry as consumed (ready for removal on compaction).
func (w *WAL) MarkConsumed(offset int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	for i, entry := range w.entries {
		if entry.Offset == offset {
			entry.Flags |= flagConsumed
			w.entries[i] = entry
			w.activeCount--
			w.totalBytes -= int64(entry.Length) + headerSize

			// Update index
			if err := w.updateIndexEntry(i, entry); err != nil {
				return err
			}

			w.updateMetrics()
			w.maybeCompact()
			return nil
		}
	}
	return nil
}

// UpdateRetries updates the retry count for an entry.
func (w *WAL) UpdateRetries(offset int64, retries int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i, entry := range w.entries {
		if entry.Offset == offset {
			w.entries[i].Retries = retries
			return
		}
	}
}

// Len returns the number of active entries.
func (w *WAL) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.activeCount
}

// Size returns the total size of active entries.
func (w *WAL) Size() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.totalBytes
}

// EffectiveMaxSize returns the current effective max size.
func (w *WAL) EffectiveMaxSize() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.effectiveMaxSize
}

// EffectiveMaxBytes returns the current effective max bytes.
func (w *WAL) EffectiveMaxBytes() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.effectiveMaxBytes
}

// Close closes the WAL.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	var errs []error

	if w.walWriter != nil {
		if err := w.walWriter.Flush(); err != nil {
			errs = append(errs, err)
		}
	}

	if w.walFile != nil {
		if err := w.walFile.Sync(); err != nil {
			errs = append(errs, err)
		}
		if err := w.walFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if w.idxFile != nil {
		if err := w.idxFile.Sync(); err != nil {
			errs = append(errs, err)
		}
		if err := w.idxFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing WAL: %v", errs)
	}
	return nil
}

// recover loads existing entries from the WAL and index.
func (w *WAL) recover() error {
	// Read index file
	indexStat, err := w.idxFile.Stat()
	if err != nil {
		return err
	}

	if indexStat.Size() == 0 {
		// Empty WAL, nothing to recover
		w.updateEffectiveLimits()
		return nil
	}

	// Seek to beginning of index
	if _, err := w.idxFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	numEntries := indexStat.Size() / indexEntrySize
	w.entries = make([]*WALEntry, 0, numEntries)

	buf := make([]byte, indexEntrySize)
	for i := int64(0); i < numEntries; i++ {
		if _, err := io.ReadFull(w.idxFile, buf); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		entry := &WALEntry{
			Offset:    int64(binary.LittleEndian.Uint64(buf[0:8])),
			Length:    binary.LittleEndian.Uint32(buf[8:12]),
			Timestamp: time.Unix(0, int64(binary.LittleEndian.Uint64(buf[12:20]))),
			Flags:     binary.LittleEndian.Uint32(buf[20:24]),
		}

		// Validate entry from WAL
		if err := w.validateEntry(entry); err != nil {
			// Skip corrupted entries
			queueDroppedTotal.WithLabelValues("corrupted").Inc()
			continue
		}

		w.entries = append(w.entries, entry)
		if entry.Flags&flagActive != 0 && entry.Flags&flagConsumed == 0 {
			w.activeCount++
			w.totalBytes += int64(entry.Length) + headerSize
		}
	}

	w.updateEffectiveLimits()
	w.updateMetrics()
	return nil
}

// validateEntry validates an entry by reading from WAL and checking CRC.
func (w *WAL) validateEntry(entry *WALEntry) error {
	header := make([]byte, headerSize)
	if _, err := w.walFile.ReadAt(header, entry.Offset); err != nil {
		return err
	}

	magic := binary.LittleEndian.Uint32(header[0:4])
	if magic != walMagic {
		return ErrCorrupted
	}

	length := binary.LittleEndian.Uint32(header[4:8])
	if length != entry.Length {
		return ErrCorrupted
	}

	expectedCRC := binary.LittleEndian.Uint32(header[8:12])

	data := make([]byte, length)
	if _, err := w.walFile.ReadAt(data, entry.Offset+headerSize); err != nil {
		return err
	}

	actualCRC := crc32.Checksum(data, crc32Table)
	if actualCRC != expectedCRC {
		return ErrCorrupted
	}

	return nil
}

// readEntryData reads the data for an entry from the WAL.
func (w *WAL) readEntryData(entry *WALEntry) ([]byte, error) {
	data := make([]byte, entry.Length)
	if _, err := w.walFile.ReadAt(data, entry.Offset+headerSize); err != nil {
		return nil, err
	}
	return data, nil
}

// writeIndexEntry writes an entry to the index file.
func (w *WAL) writeIndexEntry(entry *WALEntry) error {
	buf := make([]byte, indexEntrySize)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(entry.Offset))
	binary.LittleEndian.PutUint32(buf[8:12], entry.Length)
	binary.LittleEndian.PutUint64(buf[12:20], uint64(entry.Timestamp.UnixNano()))
	binary.LittleEndian.PutUint32(buf[20:24], entry.Flags)

	if _, err := w.idxFile.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	if _, err := w.idxFile.Write(buf); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return err
	}

	return w.idxFile.Sync()
}

// updateIndexEntry updates an existing entry in the index file.
func (w *WAL) updateIndexEntry(idx int, entry *WALEntry) error {
	buf := make([]byte, indexEntrySize)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(entry.Offset))
	binary.LittleEndian.PutUint32(buf[8:12], entry.Length)
	binary.LittleEndian.PutUint64(buf[12:20], uint64(entry.Timestamp.UnixNano()))
	binary.LittleEndian.PutUint32(buf[20:24], entry.Flags)

	offset := int64(idx) * indexEntrySize
	if _, err := w.idxFile.WriteAt(buf, offset); err != nil {
		return err
	}

	return w.idxFile.Sync()
}

// maybeCompact checks if compaction is needed and performs it.
func (w *WAL) maybeCompact() {
	if len(w.entries) == 0 {
		return
	}

	consumedCount := 0
	for _, entry := range w.entries {
		if entry.Flags&flagConsumed != 0 {
			consumedCount++
		}
	}

	ratio := float64(consumedCount) / float64(len(w.entries))
	if ratio >= w.compactThreshold {
		go w.compact()
	}
}

// compact removes consumed entries from the WAL.
func (w *WAL) compact() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}

	// Collect active entries
	activeEntries := make([]*WALEntry, 0)
	for _, entry := range w.entries {
		if entry.Flags&flagActive != 0 && entry.Flags&flagConsumed == 0 {
			activeEntries = append(activeEntries, entry)
		}
	}

	if len(activeEntries) == len(w.entries) {
		// Nothing to compact
		return
	}

	// Create new WAL and index files
	newWALPath := filepath.Join(w.path, walFileName+".new")
	newIdxPath := filepath.Join(w.path, indexFileName+".new")

	newWAL, err := os.Create(newWALPath)
	if err != nil {
		return
	}
	defer newWAL.Close()

	newIdx, err := os.Create(newIdxPath)
	if err != nil {
		os.Remove(newWALPath)
		return
	}
	defer newIdx.Close()

	newWriter := bufio.NewWriterSize(newWAL, 64*1024)

	// Copy active entries to new files
	newEntries := make([]*WALEntry, 0, len(activeEntries))
	for _, entry := range activeEntries {
		// Read data from old WAL
		data, err := w.readEntryData(entry)
		if err != nil {
			continue
		}

		// Get new offset
		newOffset, _ := newWAL.Seek(0, io.SeekCurrent)

		// Write header
		crc := crc32.Checksum(data, crc32Table)
		header := make([]byte, headerSize)
		binary.LittleEndian.PutUint32(header[0:4], walMagic)
		binary.LittleEndian.PutUint32(header[4:8], entry.Length)
		binary.LittleEndian.PutUint32(header[8:12], crc)
		binary.LittleEndian.PutUint32(header[12:16], entry.Flags)

		if _, err := newWriter.Write(header); err != nil {
			continue
		}
		if _, err := newWriter.Write(data); err != nil {
			continue
		}

		// Create new entry
		newEntry := &WALEntry{
			Offset:    newOffset,
			Length:    entry.Length,
			Timestamp: entry.Timestamp,
			Flags:     entry.Flags,
			Retries:   entry.Retries,
		}

		// Write index entry
		buf := make([]byte, indexEntrySize)
		binary.LittleEndian.PutUint64(buf[0:8], uint64(newEntry.Offset))
		binary.LittleEndian.PutUint32(buf[8:12], newEntry.Length)
		binary.LittleEndian.PutUint64(buf[12:20], uint64(newEntry.Timestamp.UnixNano()))
		binary.LittleEndian.PutUint32(buf[20:24], newEntry.Flags)

		if _, err := newIdx.Write(buf); err != nil {
			continue
		}

		newEntries = append(newEntries, newEntry)
	}

	if err := newWriter.Flush(); err != nil {
		os.Remove(newWALPath)
		os.Remove(newIdxPath)
		return
	}
	if err := newWAL.Sync(); err != nil {
		os.Remove(newWALPath)
		os.Remove(newIdxPath)
		return
	}
	if err := newIdx.Sync(); err != nil {
		os.Remove(newWALPath)
		os.Remove(newIdxPath)
		return
	}

	// Close old files
	w.walWriter.Flush()
	w.walFile.Close()
	w.idxFile.Close()

	// Rename new files
	oldWALPath := filepath.Join(w.path, walFileName)
	oldIdxPath := filepath.Join(w.path, indexFileName)

	_ = os.Remove(oldWALPath)
	_ = os.Remove(oldIdxPath)
	_ = os.Rename(newWALPath, oldWALPath)
	_ = os.Rename(newIdxPath, oldIdxPath)

	// Reopen files
	w.walFile, _ = os.OpenFile(oldWALPath, os.O_RDWR|os.O_APPEND, 0644)
	w.walWriter = bufio.NewWriterSize(w.walFile, 64*1024)
	w.idxFile, _ = os.OpenFile(oldIdxPath, os.O_RDWR, 0644) // No O_APPEND for random access

	w.entries = newEntries

	IncrementWALCompact()
	w.updateMetrics()
}

// updateEffectiveLimits calculates effective limits based on available disk space.
func (w *WAL) updateEffectiveLimits() {
	w.effectiveMaxSize = w.maxSize
	w.effectiveMaxBytes = w.maxBytes

	if !w.adaptiveEnabled {
		SetEffectiveCapacityMetrics(w.effectiveMaxSize, w.effectiveMaxBytes)
		return
	}

	// Get available disk space
	availableBytes := getAvailableDiskSpace(w.path)
	SetDiskAvailableBytes(availableBytes)

	if availableBytes <= 0 {
		SetEffectiveCapacityMetrics(w.effectiveMaxSize, w.effectiveMaxBytes)
		return
	}

	// Calculate target bytes (80-90% of available, up to maxBytes)
	targetBytes := int64(float64(availableBytes) * w.targetUtilization)
	if w.maxBytes > 0 && targetBytes > w.maxBytes {
		targetBytes = w.maxBytes
	}

	w.effectiveMaxBytes = targetBytes

	// Estimate average entry size to calculate effective max size
	avgEntrySize := int64(1024) // Default 1KB estimate
	if w.activeCount > 0 && w.totalBytes > 0 {
		avgEntrySize = w.totalBytes / int64(w.activeCount)
	}

	effectiveSize := int(targetBytes / avgEntrySize)
	if effectiveSize > w.maxSize {
		effectiveSize = w.maxSize
	}
	w.effectiveMaxSize = effectiveSize

	SetEffectiveCapacityMetrics(w.effectiveMaxSize, w.effectiveMaxBytes)
}

// updateMetrics updates all queue metrics.
func (w *WAL) updateMetrics() {
	UpdateQueueMetrics(w.activeCount, w.totalBytes, w.effectiveMaxBytes)
	w.updateEffectiveLimits()
}

// getAvailableDiskSpace returns available disk space in bytes.
func getAvailableDiskSpace(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}

// isDiskFullError checks if an error is a disk full error.
func isDiskFullError(err error) bool {
	if err == nil {
		return false
	}

	// Check for ENOSPC
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}

	// Check path error
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, syscall.ENOSPC) {
			return true
		}
	}

	return false
}
