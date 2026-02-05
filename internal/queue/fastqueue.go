package queue

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/klauspost/compress/s2"
	"github.com/szibis/metrics-governor/internal/compression"
)

const (
	// Default configuration values
	defaultInmemoryBlocks     = 2048
	defaultChunkFileSize      = 512 * 1024 * 1024 // 512MB
	defaultMetaSyncInterval   = time.Second
	defaultStaleFlushInterval = 30 * time.Second
	defaultWriteBufferSize    = 262144 // 256KB

	// Compression flag bits in the block header length field
	compressionFlagBit = uint64(1) << 63 // Bit 63: snappy compressed
	lengthMask         = ^(uint64(3) << 62) // Mask out top 2 bits for actual length

	// File names
	metaFileName = "fastqueue.meta"

	// Block header size: 8-byte length prefix
	blockHeaderSize = 8

	// Recovery timeout for legacy metadata (prevents blocking startup)
	recoveryTimeout = 10 * time.Second
)

var (
	// ErrQueueClosed is returned when operations are attempted on a closed queue.
	ErrQueueClosed = errors.New("queue is closed")
	// ErrQueueFull is returned when the queue is full.
	ErrQueueFull = errors.New("queue is full")
	// ErrDiskFull is returned when the disk is full.
	ErrDiskFull = errors.New("disk is full")
)

// block represents an in-memory data block.
type block struct {
	data      []byte
	timestamp time.Time
}

// FastQueueConfig holds the FastQueue configuration.
type FastQueueConfig struct {
	Path               string
	MaxInmemoryBlocks  int
	ChunkFileSize      int64
	MetaSyncInterval   time.Duration
	StaleFlushInterval time.Duration
	MaxSize            int              // Maximum number of entries (for compatibility)
	MaxBytes           int64            // Maximum total bytes (for compatibility)
	WriteBufferSize    int              // Buffered writer size in bytes (default: 256KB)
	Compression        compression.Type // Block compression type (default: snappy)
}

// fastqueueMeta holds the metadata persisted to disk.
type fastqueueMeta struct {
	Name         string `json:"name"`
	ReaderOffset int64  `json:"reader_offset"`
	WriterOffset int64  `json:"writer_offset"`
	Version      int    `json:"version"`
	// V2 fields - stored to avoid expensive recovery scan
	EntryCount int64 `json:"entry_count,omitempty"`
	TotalBytes int64 `json:"total_bytes,omitempty"`
}

// FastQueue implements a high-performance persistent queue inspired by VictoriaMetrics.
type FastQueue struct {
	cfg FastQueueConfig

	// In-memory layer
	ch            chan *block
	inmemoryBytes atomic.Int64

	// Disk layer
	writerChunk  *os.File
	writerBuf    *bufio.Writer
	writerOffset int64
	readerChunk  *os.File
	readerOffset int64
	pendingBytes int64

	// Stats
	activeCount atomic.Int64
	totalBytes  atomic.Int64
	diskBytes   atomic.Int64

	// Metadata sync
	lastMetaSync   time.Time
	lastBlockWrite time.Time
	stopCh         chan struct{}
	doneCh         chan struct{}

	mu     sync.Mutex
	closed bool
}

// NewFastQueue creates a new FastQueue instance.
func NewFastQueue(cfg FastQueueConfig) (*FastQueue, error) {
	// Apply defaults
	if cfg.MaxInmemoryBlocks <= 0 {
		cfg.MaxInmemoryBlocks = defaultInmemoryBlocks
	}
	if cfg.ChunkFileSize <= 0 {
		cfg.ChunkFileSize = defaultChunkFileSize
	}
	if cfg.MetaSyncInterval <= 0 {
		cfg.MetaSyncInterval = defaultMetaSyncInterval
	}
	if cfg.StaleFlushInterval <= 0 {
		cfg.StaleFlushInterval = defaultStaleFlushInterval
	}
	if cfg.WriteBufferSize <= 0 {
		cfg.WriteBufferSize = defaultWriteBufferSize
	}
	if cfg.Compression == "" {
		cfg.Compression = compression.TypeSnappy
	}

	// Create directory
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create queue directory: %w", err)
	}

	fq := &FastQueue{
		cfg:          cfg,
		ch:           make(chan *block, cfg.MaxInmemoryBlocks),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		lastMetaSync: time.Now(),
	}

	// Set capacity metrics
	SetCapacityMetrics(cfg.MaxSize, cfg.MaxBytes)
	SetEffectiveCapacityMetrics(cfg.MaxSize, cfg.MaxBytes)

	// Recover from disk
	if err := fq.recover(); err != nil {
		return nil, fmt.Errorf("failed to recover queue: %w", err)
	}

	// Start background goroutines
	go fq.metaSyncLoop()
	go fq.staleFlushLoop()

	return fq, nil
}

// Push adds data to the queue.
func (fq *FastQueue) Push(data []byte) error {
	fq.mu.Lock()
	if fq.closed {
		fq.mu.Unlock()
		return ErrQueueClosed
	}
	fq.mu.Unlock()

	// Check limits
	if fq.cfg.MaxSize > 0 && int(fq.activeCount.Load()) >= fq.cfg.MaxSize {
		return ErrQueueFull
	}
	if fq.cfg.MaxBytes > 0 && fq.totalBytes.Load()+int64(len(data)) > fq.cfg.MaxBytes {
		return ErrQueueFull
	}

	b := &block{
		data:      data,
		timestamp: time.Now(),
	}

	// Try non-blocking send to channel
	select {
	case fq.ch <- b:
		fq.inmemoryBytes.Add(int64(len(data)))
		fq.activeCount.Add(1)
		fq.totalBytes.Add(int64(len(data)))
		queuePushTotal.Inc()
		fq.updateMetrics()
		return nil
	default:
		// Channel is full, flush to disk
	}

	// Flush channel to disk and then write the new block
	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.closed {
		return ErrQueueClosed
	}

	// Flush all channel blocks to disk
	if err := fq.flushInmemoryBlocksLocked(); err != nil {
		return fmt.Errorf("failed to flush in-memory blocks: %w", err)
	}

	// Write the new block directly to disk
	if err := fq.writeBlockToDiskLocked(data); err != nil {
		return fmt.Errorf("failed to write block to disk: %w", err)
	}

	fq.activeCount.Add(1)
	fq.totalBytes.Add(int64(len(data)))
	queuePushTotal.Inc()
	fq.updateMetrics()
	return nil
}

// Pop removes and returns the oldest block from the queue.
func (fq *FastQueue) Pop() ([]byte, error) {
	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.closed {
		return nil, ErrQueueClosed
	}

	// Check if there's data on disk first (disk has older data)
	if fq.readerOffset < fq.writerOffset {
		offsetBefore := fq.readerOffset
		data, err := fq.readBlockFromDiskLocked()
		if err != nil && err != io.EOF {
			return nil, err
		}
		if data != nil {
			diskBytesConsumed := fq.readerOffset - offsetBefore
			fq.activeCount.Add(-1)
			fq.totalBytes.Add(-int64(len(data)))
			fq.diskBytes.Add(-diskBytesConsumed)
			fq.updateMetrics()

			// Clean up consumed chunks
			fq.cleanupConsumedChunksLocked()
			return data, nil
		}
	}

	// Try non-blocking receive from channel
	select {
	case b := <-fq.ch:
		fq.inmemoryBytes.Add(-int64(len(b.data)))
		fq.activeCount.Add(-1)
		fq.totalBytes.Add(-int64(len(b.data)))
		fq.updateMetrics()
		return b.data, nil
	default:
		// Channel is empty
		return nil, nil // Empty queue
	}
}

// Peek returns the oldest block without removing it.
func (fq *FastQueue) Peek() ([]byte, error) {
	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.closed {
		return nil, ErrQueueClosed
	}

	// Check if there's data on disk first (disk has older data)
	if fq.readerOffset < fq.writerOffset {
		data, err := fq.peekBlockFromDiskLocked()
		if err != nil && err != io.EOF {
			return nil, err
		}
		if data != nil {
			return data, nil
		}
	}

	// Peek from channel by doing a select with a temporary variable
	select {
	case b := <-fq.ch:
		// Put it back (this changes order but maintains data)
		select {
		case fq.ch <- b:
		default:
			// Channel full after we took from it - shouldn't happen
			// Write to disk instead
			if err := fq.writeBlockToDiskLocked(b.data); err != nil {
				return nil, err
			}
			fq.inmemoryBytes.Add(-int64(len(b.data)))
		}
		return b.data, nil
	default:
		return nil, nil // Empty queue
	}
}

// Len returns the number of entries in the queue.
func (fq *FastQueue) Len() int {
	return int(fq.activeCount.Load())
}

// Size returns the total size of entries in bytes.
func (fq *FastQueue) Size() int64 {
	return fq.totalBytes.Load()
}

// Close closes the queue, flushing all data to disk.
func (fq *FastQueue) Close() error {
	fq.mu.Lock()
	if fq.closed {
		fq.mu.Unlock()
		return nil
	}
	fq.closed = true
	fq.mu.Unlock()

	// Signal background goroutines to stop
	close(fq.stopCh)

	// Wait for background goroutines
	<-fq.doneCh

	fq.mu.Lock()
	defer fq.mu.Unlock()

	var errs []error

	// Flush in-memory blocks
	if err := fq.flushInmemoryBlocksLocked(); err != nil {
		errs = append(errs, fmt.Errorf("failed to flush in-memory blocks: %w", err))
	}

	// Final metadata sync
	if err := fq.syncMetadataLocked(); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync metadata: %w", err))
	}

	// Flush buffered writer and close chunk files
	if fq.writerBuf != nil {
		if err := fq.writerBuf.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("failed to flush write buffer: %w", err))
		}
	}
	if fq.writerChunk != nil {
		if err := fq.writerChunk.Sync(); err != nil {
			errs = append(errs, err)
		}
		if err := fq.writerChunk.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
		if err := fq.readerChunk.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing queue: %v", errs)
	}
	return nil
}

// recover loads the queue state from disk.
func (fq *FastQueue) recover() error {
	metaPath := filepath.Join(fq.cfg.Path, metaFileName)

	// Try to read metadata
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Fresh queue, no recovery needed
			return nil
		}
		return fmt.Errorf("failed to read metadata: %w", err)
	}

	var meta fastqueueMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	fq.readerOffset = meta.ReaderOffset
	fq.writerOffset = meta.WriterOffset

	// Calculate pending bytes and active count from disk
	if fq.writerOffset > fq.readerOffset {
		fq.pendingBytes = fq.writerOffset - fq.readerOffset
		fq.diskBytes.Store(fq.pendingBytes)

		// Use stored count if available (V2 metadata), otherwise scan
		if meta.EntryCount > 0 && meta.TotalBytes > 0 {
			// Fast path: use persisted counts (O(1) recovery)
			fq.activeCount.Store(meta.EntryCount)
			fq.totalBytes.Store(meta.TotalBytes)
			log.Printf("[fastqueue] recovered %d entries (%d bytes) from metadata", meta.EntryCount, meta.TotalBytes)
		} else {
			// Slow path: count entries by scanning (legacy metadata)
			// Use timeout to prevent blocking startup indefinitely
			log.Printf("[fastqueue] legacy metadata detected, scanning queue (this may take a while)...")
			start := time.Now()

			done := make(chan struct{})
			var count int
			var totalDataBytes int64
			var scanErr error

			go func() {
				count, totalDataBytes, scanErr = fq.countEntriesOnDisk()
				close(done)
			}()

			select {
			case <-done:
				if scanErr != nil {
					return fmt.Errorf("failed to count entries: %w", scanErr)
				}
				fq.activeCount.Store(int64(count))
				fq.totalBytes.Store(totalDataBytes)
				log.Printf("[fastqueue] recovered %d entries (%d bytes) in %v", count, totalDataBytes, time.Since(start))
			case <-time.After(recoveryTimeout):
				// Timeout - discard queue data to allow startup
				log.Printf("[fastqueue] recovery timeout after %v, discarding queue data", recoveryTimeout)
				fq.readerOffset = 0
				fq.writerOffset = 0
				fq.pendingBytes = 0
				fq.diskBytes.Store(0)
				fq.activeCount.Store(0)
				fq.totalBytes.Store(0)
				// Clean up all queue files
				fq.cleanupAllChunks()
			}
		}
	}

	// Open chunk files if we have data
	if fq.writerOffset > 0 {
		writerChunkPath := fq.chunkPath(fq.writerOffset)
		writerChunk, err := os.OpenFile(writerChunkPath, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return fmt.Errorf("failed to open writer chunk: %w", err)
		}
		fq.writerChunk = writerChunk

		// Seek to the correct position within the chunk
		chunkOffset := fq.writerOffset % fq.cfg.ChunkFileSize
		if _, err := writerChunk.Seek(chunkOffset, io.SeekStart); err != nil {
			writerChunk.Close()
			return fmt.Errorf("failed to seek writer chunk: %w", err)
		}
	}

	if fq.readerOffset < fq.writerOffset {
		readerChunkPath := fq.chunkPath(fq.readerOffset)
		if readerChunkPath == fq.chunkPath(fq.writerOffset) {
			fq.readerChunk = fq.writerChunk
		} else {
			readerChunk, err := os.OpenFile(readerChunkPath, os.O_RDONLY, 0644)
			if err != nil {
				return fmt.Errorf("failed to open reader chunk: %w", err)
			}
			fq.readerChunk = readerChunk
		}

		// Seek to the correct position
		chunkOffset := fq.readerOffset % fq.cfg.ChunkFileSize
		if _, err := fq.readerChunk.Seek(chunkOffset, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek reader chunk: %w", err)
		}
	}

	// Clean up orphaned chunks
	fq.cleanupOrphanedChunks()

	fq.updateMetrics()
	return nil
}

// countEntriesOnDisk counts entries between reader and writer offsets.
func (fq *FastQueue) countEntriesOnDisk() (int, int64, error) {
	if fq.readerOffset >= fq.writerOffset {
		return 0, 0, nil
	}

	count := 0
	var totalDataBytes int64
	offset := fq.readerOffset

	for offset < fq.writerOffset {
		chunkPath := fq.chunkPath(offset)
		f, err := os.Open(chunkPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Chunk file missing, skip to next chunk boundary
				nextChunk := ((offset / fq.cfg.ChunkFileSize) + 1) * fq.cfg.ChunkFileSize
				offset = nextChunk
				continue
			}
			return 0, 0, err
		}

		// Seek to position within chunk
		chunkOffset := offset % fq.cfg.ChunkFileSize
		if _, err := f.Seek(chunkOffset, io.SeekStart); err != nil {
			f.Close()
			return 0, 0, err
		}

		// Read entries until end of chunk or writer offset
		chunkEnd := ((offset / fq.cfg.ChunkFileSize) + 1) * fq.cfg.ChunkFileSize
		if chunkEnd > fq.writerOffset {
			chunkEnd = fq.writerOffset
		}

		for offset < chunkEnd {
			// Read length header
			var lengthField uint64
			if err := binary.Read(f, binary.LittleEndian, &lengthField); err != nil {
				if err == io.EOF {
					break
				}
				f.Close()
				return 0, 0, err
			}

			// Mask out compression flag bits to get actual data length
			dataLen := int64(lengthField & lengthMask)

			// Skip data
			if _, err := f.Seek(dataLen, io.SeekCurrent); err != nil {
				f.Close()
				return 0, 0, err
			}

			count++
			totalDataBytes += dataLen
			offset += blockHeaderSize + dataLen
		}

		f.Close()
	}

	return count, totalDataBytes, nil
}

// chunkPath returns the file path for the chunk containing the given offset.
func (fq *FastQueue) chunkPath(offset int64) string {
	chunkStart := (offset / fq.cfg.ChunkFileSize) * fq.cfg.ChunkFileSize
	return filepath.Join(fq.cfg.Path, fmt.Sprintf("%016x", chunkStart))
}

// flushInmemoryBlocksLocked drains the channel to disk with write coalescing (must hold lock).
func (fq *FastQueue) flushInmemoryBlocksLocked() error {
	// Drain all pending blocks from channel first (coalescing)
	var blocks []*block
	for {
		select {
		case b := <-fq.ch:
			blocks = append(blocks, b)
			fq.inmemoryBytes.Add(-int64(len(b.data)))
		default:
			goto done
		}
	}
done:
	if len(blocks) == 0 {
		return nil
	}

	// Write all blocks through the buffered writer
	for _, b := range blocks {
		if err := fq.writeBlockToDiskLocked(b.data); err != nil {
			return err
		}
	}

	// Flush the bufio.Writer after the entire batch
	if fq.writerBuf != nil {
		if err := fq.writerBuf.Flush(); err != nil {
			return err
		}
	}

	IncrementInmemoryFlush()
	return nil
}

// writeBlockToDiskLocked writes a block to the current chunk (must hold lock).
// Applies compression if configured and writes through a bufio.Writer.
func (fq *FastQueue) writeBlockToDiskLocked(data []byte) error {
	// Compress data if configured
	writeData := data
	var lengthField uint64

	if fq.cfg.Compression == compression.TypeSnappy {
		writeData = s2.EncodeSnappy(nil, data)
		lengthField = uint64(len(writeData)) | compressionFlagBit
	} else {
		lengthField = uint64(len(writeData))
	}

	// Check if we need to rotate to a new chunk
	if fq.writerChunk != nil {
		chunkOffset := fq.writerOffset % fq.cfg.ChunkFileSize
		if chunkOffset+int64(blockHeaderSize+len(writeData)) > fq.cfg.ChunkFileSize {
			// Flush buffered writer before rotation
			if fq.writerBuf != nil {
				if err := fq.writerBuf.Flush(); err != nil {
					return err
				}
			}
			// Sync and close current chunk
			if err := fq.writerChunk.Sync(); err != nil {
				return err
			}
			// Don't close if reader is using the same file
			if fq.readerChunk != fq.writerChunk {
				fq.writerChunk.Close()
			}
			fq.writerChunk = nil
			fq.writerBuf = nil

			// Move writer offset to next chunk boundary
			fq.writerOffset = ((fq.writerOffset / fq.cfg.ChunkFileSize) + 1) * fq.cfg.ChunkFileSize
			IncrementChunkRotation()
		}
	}

	// Open chunk file if needed
	if fq.writerChunk == nil {
		chunkPath := fq.chunkPath(fq.writerOffset)
		f, err := os.OpenFile(chunkPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			if isDiskFullError(err) {
				IncrementDiskFull()
				return ErrDiskFull
			}
			return err
		}
		fq.writerChunk = f
		fq.writerBuf = bufio.NewWriterSize(f, fq.cfg.WriteBufferSize)
	}

	// Write through buffered writer
	w := fq.writerBuf

	// Write length header (with compression flag)
	header := make([]byte, blockHeaderSize)
	binary.LittleEndian.PutUint64(header, lengthField)

	if _, err := w.Write(header); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return err
	}

	// Write (possibly compressed) data
	if _, err := w.Write(writeData); err != nil {
		if isDiskFullError(err) {
			IncrementDiskFull()
			return ErrDiskFull
		}
		return err
	}

	fq.writerOffset += int64(blockHeaderSize + len(writeData))
	fq.pendingBytes += int64(blockHeaderSize + len(writeData))
	fq.diskBytes.Add(int64(blockHeaderSize + len(writeData)))
	fq.lastBlockWrite = time.Now()

	return nil
}

// readBlockFromDiskLocked reads a block from disk (must hold lock).
// Returns the decompressed data and the on-disk bytes consumed (header + compressed data).
func (fq *FastQueue) readBlockFromDiskLocked() ([]byte, error) {
	if fq.readerOffset >= fq.writerOffset {
		return nil, io.EOF
	}

	// Flush buffered writer before reading to ensure data is visible on disk
	if fq.writerBuf != nil {
		if err := fq.writerBuf.Flush(); err != nil {
			return nil, err
		}
	}

	// Check if we need to move to next chunk
	chunkEnd := ((fq.readerOffset / fq.cfg.ChunkFileSize) + 1) * fq.cfg.ChunkFileSize
	if fq.readerOffset >= chunkEnd {
		// Move to next chunk
		if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
			fq.readerChunk.Close()
		}
		fq.readerChunk = nil
		fq.readerOffset = chunkEnd
	}

	// Open chunk file if needed
	if fq.readerChunk == nil {
		chunkPath := fq.chunkPath(fq.readerOffset)

		// Check if it's the same as writer chunk
		if fq.writerChunk != nil && chunkPath == fq.chunkPath(fq.writerOffset) {
			fq.readerChunk = fq.writerChunk
		} else {
			f, err := os.OpenFile(chunkPath, os.O_RDONLY, 0644)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, io.EOF
				}
				return nil, err
			}
			fq.readerChunk = f
		}

		// Seek to position within chunk
		chunkOffset := fq.readerOffset % fq.cfg.ChunkFileSize
		if _, err := fq.readerChunk.Seek(chunkOffset, io.SeekStart); err != nil {
			return nil, err
		}
	}

	// Read length header
	header := make([]byte, blockHeaderSize)
	if _, err := io.ReadFull(fq.readerChunk, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			// Might have hit end of chunk due to padding, try next chunk
			chunkEnd := ((fq.readerOffset / fq.cfg.ChunkFileSize) + 1) * fq.cfg.ChunkFileSize
			if fq.readerOffset < fq.writerOffset && fq.readerOffset < chunkEnd {
				// There's a gap, skip to next chunk boundary
				if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
					fq.readerChunk.Close()
				}
				fq.readerChunk = nil
				fq.readerOffset = chunkEnd
				// Retry read from new chunk
				return fq.readBlockFromDiskLocked()
			}
			return nil, io.EOF
		}
		return nil, err
	}

	lengthField := binary.LittleEndian.Uint64(header)
	compressed := (lengthField & compressionFlagBit) != 0
	dataLen := lengthField & lengthMask

	// Read data
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(fq.readerChunk, data); err != nil {
		return nil, err
	}

	fq.readerOffset += int64(blockHeaderSize) + int64(dataLen)
	fq.pendingBytes -= int64(blockHeaderSize) + int64(dataLen)

	// Decompress if needed
	if compressed {
		decoded, err := s2.Decode(nil, data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress block: %w", err)
		}
		return decoded, nil
	}

	return data, nil
}

// peekBlockFromDiskLocked peeks at a block without advancing the reader (must hold lock).
func (fq *FastQueue) peekBlockFromDiskLocked() ([]byte, error) {
	if fq.readerOffset >= fq.writerOffset {
		return nil, io.EOF
	}

	// Remember current position
	originalOffset := fq.readerOffset

	// Open chunk file if needed (temporarily)
	chunkPath := fq.chunkPath(fq.readerOffset)
	var f *os.File
	var err error

	if fq.readerChunk != nil && chunkPath == fq.chunkPath(fq.readerOffset-fq.readerOffset%fq.cfg.ChunkFileSize) {
		f = fq.readerChunk
	} else {
		f, err = os.Open(chunkPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, io.EOF
			}
			return nil, err
		}
		defer f.Close()
	}

	// Seek to position within chunk
	chunkOffset := originalOffset % fq.cfg.ChunkFileSize
	if _, err := f.Seek(chunkOffset, io.SeekStart); err != nil {
		return nil, err
	}

	// Read length header
	header := make([]byte, blockHeaderSize)
	if _, err := io.ReadFull(f, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, err
	}

	lengthField := binary.LittleEndian.Uint64(header)
	compressed := (lengthField & compressionFlagBit) != 0
	dataLen := lengthField & lengthMask

	// Read data
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}

	// Decompress if needed
	if compressed {
		decoded, err := s2.Decode(nil, data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress block: %w", err)
		}
		return decoded, nil
	}

	return data, nil
}

// cleanupConsumedChunksLocked removes chunk files that have been fully consumed.
func (fq *FastQueue) cleanupConsumedChunksLocked() {
	// Get all chunk files
	entries, err := os.ReadDir(fq.cfg.Path)
	if err != nil {
		return
	}

	readerChunkStart := (fq.readerOffset / fq.cfg.ChunkFileSize) * fq.cfg.ChunkFileSize

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == metaFileName {
			continue
		}

		// Parse chunk start offset from filename
		chunkStart, err := strconv.ParseInt(entry.Name(), 16, 64)
		if err != nil {
			continue
		}

		// Remove chunks that are fully consumed
		if chunkStart+fq.cfg.ChunkFileSize <= readerChunkStart {
			chunkPath := filepath.Join(fq.cfg.Path, entry.Name())
			os.Remove(chunkPath)
		}
	}
}

// cleanupAllChunks removes all chunk files (used when recovery times out).
func (fq *FastQueue) cleanupAllChunks() {
	entries, err := os.ReadDir(fq.cfg.Path)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == metaFileName || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		if len(entry.Name()) == 16 {
			chunkPath := filepath.Join(fq.cfg.Path, entry.Name())
			os.Remove(chunkPath)
		}
	}

	// Remove stale metadata
	metaPath := filepath.Join(fq.cfg.Path, metaFileName)
	os.Remove(metaPath)
}

// cleanupOrphanedChunks removes chunk files outside the valid range.
func (fq *FastQueue) cleanupOrphanedChunks() {
	entries, err := os.ReadDir(fq.cfg.Path)
	if err != nil {
		return
	}

	readerChunkStart := (fq.readerOffset / fq.cfg.ChunkFileSize) * fq.cfg.ChunkFileSize
	writerChunkStart := (fq.writerOffset / fq.cfg.ChunkFileSize) * fq.cfg.ChunkFileSize

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == metaFileName {
			continue
		}

		// Skip non-hex filenames
		if len(entry.Name()) != 16 {
			continue
		}

		chunkStart, err := strconv.ParseInt(entry.Name(), 16, 64)
		if err != nil {
			continue
		}

		// Remove chunks outside valid range
		if chunkStart+fq.cfg.ChunkFileSize <= readerChunkStart || chunkStart > writerChunkStart {
			chunkPath := filepath.Join(fq.cfg.Path, entry.Name())
			os.Remove(chunkPath)
		}
	}
}

// syncMetadataLocked writes metadata to disk atomically (must hold lock).
func (fq *FastQueue) syncMetadataLocked() error {
	meta := fastqueueMeta{
		Name:         "fastqueue",
		ReaderOffset: fq.readerOffset,
		WriterOffset: fq.writerOffset,
		Version:      2,
		// V2: persist counts to avoid expensive recovery scan
		EntryCount: fq.activeCount.Load(),
		TotalBytes: fq.totalBytes.Load(),
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	// Write to temp file first
	tmpPath := filepath.Join(fq.cfg.Path, metaFileName+".tmp")
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	// Sync the temp file
	tmpFile, err := os.Open(tmpPath)
	if err == nil {
		_ = tmpFile.Sync()
		tmpFile.Close()
	}

	// Atomic rename
	metaPath := filepath.Join(fq.cfg.Path, metaFileName)
	if err := os.Rename(tmpPath, metaPath); err != nil {
		return err
	}

	// Sync directory
	dir, err := os.Open(fq.cfg.Path)
	if err == nil {
		_ = dir.Sync()
		dir.Close()
	}

	fq.lastMetaSync = time.Now()
	IncrementMetaSync()

	return nil
}

// metaSyncLoop periodically syncs metadata to disk.
func (fq *FastQueue) metaSyncLoop() {
	ticker := time.NewTicker(fq.cfg.MetaSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-fq.stopCh:
			return
		case <-ticker.C:
			fq.mu.Lock()
			if !fq.closed {
				_ = fq.syncMetadataLocked()
			}
			fq.mu.Unlock()
		}
	}
}

// staleFlushLoop periodically flushes stale in-memory blocks to disk.
func (fq *FastQueue) staleFlushLoop() {
	ticker := time.NewTicker(fq.cfg.StaleFlushInterval)
	defer ticker.Stop()
	defer close(fq.doneCh)

	for {
		select {
		case <-fq.stopCh:
			return
		case <-ticker.C:
			fq.mu.Lock()
			if !fq.closed && len(fq.ch) > 0 {
				// Check if oldest block is stale
				if time.Since(fq.lastBlockWrite) >= fq.cfg.StaleFlushInterval {
					_ = fq.flushInmemoryBlocksLocked()
					// flushInmemoryBlocksLocked already flushes the bufio.Writer
				}
			}
			fq.mu.Unlock()
		}
	}
}

// updateMetrics updates all queue metrics.
func (fq *FastQueue) updateMetrics() {
	UpdateQueueMetrics(int(fq.activeCount.Load()), fq.totalBytes.Load(), fq.cfg.MaxBytes)
	SetInmemoryBlocks(len(fq.ch))
	SetDiskBytes(fq.diskBytes.Load())
}

// GetChunkFiles returns the list of chunk files (for testing).
func (fq *FastQueue) GetChunkFiles() ([]string, error) {
	entries, err := os.ReadDir(fq.cfg.Path)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == metaFileName || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		if len(entry.Name()) == 16 {
			chunks = append(chunks, entry.Name())
		}
	}

	sort.Strings(chunks)
	return chunks, nil
}

// isDiskFullError checks if an error indicates disk is full.
func isDiskFullError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.ENOSPC) {
		return true
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, syscall.ENOSPC) {
			return true
		}
	}

	return false
}
