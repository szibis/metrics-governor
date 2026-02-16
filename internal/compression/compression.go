// Package compression provides compression and decompression utilities for OTLP HTTP.
package compression

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
)

// Type represents a compression algorithm.
type Type string

const (
	// TypeNone means no compression.
	TypeNone Type = "none"
	// TypeGzip uses gzip compression.
	TypeGzip Type = "gzip"
	// TypeZstd uses zstd compression.
	TypeZstd Type = "zstd"
	// TypeSnappy uses snappy compression.
	TypeSnappy Type = "snappy"
	// TypeZlib uses zlib compression.
	TypeZlib Type = "zlib"
	// TypeDeflate uses deflate compression.
	TypeDeflate Type = "deflate"
)

// Level represents compression level settings.
type Level int

// Common compression levels (algorithm-specific mappings).
const (
	// LevelDefault uses the default compression level for the algorithm.
	LevelDefault Level = 0
	// LevelFastest uses the fastest compression (lowest ratio).
	LevelFastest Level = 1
	// LevelBest uses the best compression (highest ratio).
	LevelBest Level = 9
)

// gzip/zlib/deflate levels
const (
	GzipBestSpeed          Level = 1
	GzipBestCompression    Level = 9
	GzipDefaultCompression Level = -1
)

// zstd levels
const (
	ZstdSpeedFastest           Level = 1
	ZstdSpeedDefault           Level = 3
	ZstdSpeedBetterCompression Level = 6
	ZstdSpeedBestCompression   Level = 11
)

// Config holds compression configuration.
type Config struct {
	// Type is the compression algorithm to use.
	Type Type
	// Level is the compression level (algorithm-specific).
	Level Level
}

// -----------------------------------------------------------------------
// Encoder/decoder pools
// -----------------------------------------------------------------------

// bufPool pools bytes.Buffer instances used as compression output targets.
var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Writer pools - writers are created at whatever level is first requested and
// reused. In practice the compression config is set once per process, so pooled
// writers will almost always match the requested level.
var (
	gzipWriterPool  sync.Pool
	zlibWriterPool  sync.Pool
	flateWriterPool sync.Pool
	zstdEncoderPool sync.Pool
	zstdDecoderPool sync.Pool

	// zstdCompressDstPool pools destination buffers for zstd EncodeAll output.
	// Compressed output is typically 10-30% of input; 128KB handles most payloads.
	zstdCompressDstPool = sync.Pool{New: func() any {
		b := make([]byte, 0, 128*1024)
		return &b
	}}

	// zstdDecompressDstPool pools destination buffers for zstd DecodeAll output.
	// Decompressed output is typically 3-10x larger than input; 256KB initial capacity.
	zstdDecompressDstPool = sync.Pool{New: func() any {
		b := make([]byte, 0, 256*1024)
		return &b
	}}
)

// Pool metrics - exported via PoolStats().
var (
	compressionPoolGets     atomic.Int64
	compressionPoolPuts     atomic.Int64
	compressionPoolDiscards atomic.Int64
	compressionPoolNews     atomic.Int64
	bufferPoolGets          atomic.Int64
	bufferPoolPuts          atomic.Int64

	// Active buffer tracking — number of buffers currently checked out from pool.
	bufferActive atomic.Int64
)

// PoolStatsSnapshot holds a point-in-time snapshot of pool counters.
type PoolStatsSnapshot struct {
	CompressionPoolGets     int64
	CompressionPoolPuts     int64
	CompressionPoolDiscards int64
	CompressionPoolNews     int64
	BufferPoolGets          int64
	BufferPoolPuts          int64
}

// PoolStats returns a snapshot of the pool metrics.
func PoolStats() PoolStatsSnapshot {
	return PoolStatsSnapshot{
		CompressionPoolGets:     compressionPoolGets.Load(),
		CompressionPoolPuts:     compressionPoolPuts.Load(),
		CompressionPoolDiscards: compressionPoolDiscards.Load(),
		CompressionPoolNews:     compressionPoolNews.Load(),
		BufferPoolGets:          bufferPoolGets.Load(),
		BufferPoolPuts:          bufferPoolPuts.Load(),
	}
}

// BufferActiveCount returns the number of compression buffers currently checked out.
func BufferActiveCount() int64 {
	return bufferActive.Load()
}

// maxPoolBufSize is the maximum buffer capacity retained in the pool.
// Buffers larger than this are discarded to prevent memory bloat from outlier requests.
const maxPoolBufSize = 4 * 1024 * 1024 // 4 MB

// GetBuffer returns a *bytes.Buffer from the pool, ready for use.
// The caller MUST call ReleaseBuffer when done with the buffer.
func GetBuffer() *bytes.Buffer {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	bufferPoolGets.Add(1)
	bufferActive.Add(1)
	return buf
}

// ReleaseBuffer returns a buffer to the pool. Oversized buffers are discarded.
func ReleaseBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	bufferActive.Add(-1)
	if buf.Cap() > maxPoolBufSize {
		compressionPoolDiscards.Add(1)
		return
	}
	bufferPoolPuts.Add(1)
	bufPool.Put(buf)
}

// ResetPoolStats resets all pool metric counters to zero (useful in tests).
func ResetPoolStats() {
	compressionPoolGets.Store(0)
	compressionPoolPuts.Store(0)
	compressionPoolDiscards.Store(0)
	compressionPoolNews.Store(0)
	bufferPoolGets.Store(0)
	bufferPoolPuts.Store(0)
	bufferActive.Store(0)
}

// -----------------------------------------------------------------------
// Public API
// -----------------------------------------------------------------------

// ParseType parses a compression type string.
func ParseType(s string) (Type, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return TypeNone, nil
	case "gzip":
		return TypeGzip, nil
	case "zstd":
		return TypeZstd, nil
	case "snappy":
		return TypeSnappy, nil
	case "zlib":
		return TypeZlib, nil
	case "deflate":
		return TypeDeflate, nil
	default:
		return TypeNone, fmt.Errorf("unsupported compression type: %s", s)
	}
}

// ContentEncoding returns the HTTP Content-Encoding header value for the compression type.
func (t Type) ContentEncoding() string {
	switch t {
	case TypeGzip:
		return "gzip"
	case TypeZstd:
		return "zstd"
	case TypeSnappy:
		return "snappy"
	case TypeZlib:
		return "zlib"
	case TypeDeflate:
		return "deflate"
	default:
		return ""
	}
}

// ParseContentEncoding parses an HTTP Content-Encoding header value to a compression type.
func ParseContentEncoding(encoding string) Type {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip", "x-gzip":
		return TypeGzip
	case "zstd":
		return TypeZstd
	case "snappy", "x-snappy-framed":
		return TypeSnappy
	case "zlib":
		return TypeZlib
	case "deflate":
		return TypeDeflate
	default:
		return TypeNone
	}
}

// Compress compresses data using the specified compression type and level.
// When built with CGO_ENABLED=1 -tags native_compress, uses Rust FFI for
// gzip, zlib, and deflate (~1.6x faster via flate2/miniz_oxide).
// Zstd uses pure Go (klauspost with assembly-accelerated hash matching
// outperforms C libzstd via CGO by ~1.8x at level 3).
// Snappy uses pure Go (S2 outperforms all native Snappy implementations).
func Compress(data []byte, cfg Config) ([]byte, error) {
	if cfg.Type == TypeNone || cfg.Type == "" {
		return data, nil
	}

	// Snappy is allocation-free and doesn't need buffer pooling.
	// S2 already outperforms all native Snappy implementations.
	if cfg.Type == TypeSnappy {
		return compressSnappy(data), nil
	}

	// Use native (Rust FFI) compression for deflate-family algorithms.
	// Zstd excluded: klauspost's pooled asm-accelerated encoder beats C libzstd via CGO.
	if nativeCompressionAvailable && cfg.Type != TypeZstd {
		return nativeCompress(data, cfg)
	}

	// Zstd uses EncodeAll (1.5x faster than streaming, zero-alloc with pooled dst).
	if cfg.Type == TypeZstd {
		return compressZstdEncodeAll(data, cfg.Level)
	}

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	bufferPoolGets.Add(1)
	bufferActive.Add(1)

	var err error
	switch cfg.Type {
	case TypeGzip:
		err = compressGzip(buf, data, cfg.Level)
	case TypeZlib:
		err = compressZlib(buf, data, cfg.Level)
	case TypeDeflate:
		err = compressDeflate(buf, data, cfg.Level)
	default:
		bufferPoolPuts.Add(1)
		bufferActive.Add(-1)
		bufPool.Put(buf)
		return nil, fmt.Errorf("unsupported compression type: %s", cfg.Type)
	}

	if err != nil {
		bufferPoolPuts.Add(1)
		bufferActive.Add(-1)
		bufPool.Put(buf)
		return nil, err
	}

	// Copy result so the buffer can be returned to the pool safely.
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	bufferPoolPuts.Add(1)
	bufferActive.Add(-1)
	bufPool.Put(buf)
	return result, nil
}

// Decompress decompresses data using the specified compression type.
// When built with CGO_ENABLED=1 -tags native_compress, uses Rust FFI for
// gzip, zlib, and deflate. Zstd and Snappy use pure Go.
func Decompress(data []byte, compressionType Type) ([]byte, error) {
	if compressionType == TypeNone || compressionType == "" {
		return data, nil
	}

	// Use native (Rust FFI) decompression for deflate-family algorithms.
	if nativeCompressionAvailable && compressionType != TypeSnappy && compressionType != TypeZstd {
		return nativeDecompress(data, compressionType)
	}

	// Zstd uses DecodeAll (1.7x faster than streaming, 19 fewer allocs).
	if compressionType == TypeZstd {
		return decompressZstdDecodeAll(data)
	}

	switch compressionType {
	case TypeGzip:
		return decompressGzip(data)
	case TypeSnappy:
		return decompressSnappy(data)
	case TypeZlib:
		return decompressZlib(data)
	case TypeDeflate:
		return decompressDeflate(data)
	default:
		return nil, fmt.Errorf("unsupported compression type: %s", compressionType)
	}
}

// CompressToBuf compresses data into dst using the specified compression config.
// The caller owns dst and must release it via ReleaseBuffer when done.
// For snappy, dst is unused and the result is written to a new allocation
// (snappy is already allocation-free via s2.EncodeSnappy).
func CompressToBuf(dst *bytes.Buffer, data []byte, cfg Config) error {
	if cfg.Type == TypeNone || cfg.Type == "" {
		dst.Write(data)
		return nil
	}

	if cfg.Type == TypeSnappy {
		// Snappy uses s2.EncodeSnappy which manages its own output buffer.
		// Write the result into dst.
		dst.Write(compressSnappy(data))
		return nil
	}

	// Use native (Rust FFI) compression for deflate-family algorithms.
	if nativeCompressionAvailable && cfg.Type != TypeZstd {
		result, err := nativeCompress(data, cfg)
		if err != nil {
			return err
		}
		dst.Write(result)
		return nil
	}

	// Zstd uses EncodeAll (1.5x faster than streaming).
	if cfg.Type == TypeZstd {
		result, err := compressZstdEncodeAll(data, cfg.Level)
		if err != nil {
			return err
		}
		dst.Write(result)
		return nil
	}

	switch cfg.Type {
	case TypeGzip:
		return compressGzip(dst, data, cfg.Level)
	case TypeZlib:
		return compressZlib(dst, data, cfg.Level)
	case TypeDeflate:
		return compressDeflate(dst, data, cfg.Level)
	default:
		return fmt.Errorf("unsupported compression type: %s", cfg.Type)
	}
}

// DecompressToBuf decompresses data into dst using the specified compression type.
// The caller owns dst and must release it via ReleaseBuffer when done.
func DecompressToBuf(dst *bytes.Buffer, data []byte, compressionType Type) error {
	if compressionType == TypeNone || compressionType == "" {
		dst.Write(data)
		return nil
	}

	// Use native (Rust FFI) decompression for deflate-family algorithms.
	if nativeCompressionAvailable && compressionType != TypeSnappy && compressionType != TypeZstd {
		result, err := nativeDecompress(data, compressionType)
		if err != nil {
			return err
		}
		dst.Write(result)
		return nil
	}

	// Zstd uses DecodeAll (1.7x faster than streaming).
	if compressionType == TypeZstd {
		result, err := decompressZstdDecodeAll(data)
		if err != nil {
			return err
		}
		dst.Write(result)
		return nil
	}

	switch compressionType {
	case TypeGzip:
		return decompressIntoBuf(dst, data, func(r io.Reader) (io.ReadCloser, error) {
			return gzip.NewReader(r)
		})
	case TypeSnappy:
		result, err := decompressSnappy(data)
		if err != nil {
			return err
		}
		dst.Write(result)
		return nil
	case TypeZlib:
		return decompressIntoBuf(dst, data, func(r io.Reader) (io.ReadCloser, error) {
			return zlib.NewReader(r)
		})
	case TypeDeflate:
		return decompressIntoBuf(dst, data, func(r io.Reader) (io.ReadCloser, error) {
			return flate.NewReader(r), nil
		})
	default:
		return fmt.Errorf("unsupported compression type: %s", compressionType)
	}
}

// decompressIntoBuf is a generic helper that decompresses into a caller-provided buffer
// using a reader factory (gzip, zlib, deflate all follow the same pattern).
func decompressIntoBuf(dst *bytes.Buffer, data []byte, newReader func(io.Reader) (io.ReadCloser, error)) error {
	r, err := newReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = dst.ReadFrom(r)
	return err
}

// maxPooledBufCap is the maximum capacity for buffers retained in pools.
// Prevents retaining outlier-sized buffers that would waste memory.
const maxPooledBufCap = 4 * 1024 * 1024 // 4MB

// -----------------------------------------------------------------------
// gzip compression (pooled)
// -----------------------------------------------------------------------

func compressGzip(w io.Writer, data []byte, level Level) error {
	gzLevel := gzip.DefaultCompression
	if level != LevelDefault {
		gzLevel = int(level)
	}

	var gw *gzip.Writer
	if v := gzipWriterPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		gw = v.(*gzip.Writer)
		gw.Reset(w)
	} else {
		compressionPoolNews.Add(1)
		var err error
		gw, err = gzip.NewWriterLevel(w, gzLevel)
		if err != nil {
			return fmt.Errorf("failed to create gzip writer: %w", err)
		}
	}

	if _, err := gw.Write(data); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to write gzip data: %w", err)
	}
	if err := gw.Close(); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}
	compressionPoolPuts.Add(1)
	gzipWriterPool.Put(gw)
	return nil
}

func decompressGzip(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()
	return readAllPooled(gr)
}

// -----------------------------------------------------------------------
// zstd compression (EncodeAll/DecodeAll — 1.5x faster than streaming)
// -----------------------------------------------------------------------

// mapZstdLevel converts our Level constants to klauspost zstd.EncoderLevel.
func mapZstdLevel(level Level) zstd.EncoderLevel {
	switch level {
	case ZstdSpeedFastest:
		return zstd.SpeedFastest
	case ZstdSpeedBetterCompression:
		return zstd.SpeedBetterCompression
	case ZstdSpeedBestCompression:
		return zstd.SpeedBestCompression
	default:
		return zstd.SpeedDefault
	}
}

// compressZstdEncodeAll compresses using EncodeAll with pooled encoder and
// destination buffer. EncodeAll bypasses the streaming goroutine coordination,
// yielding ~1.5x throughput at zero extra allocations.
func compressZstdEncodeAll(data []byte, level Level) ([]byte, error) {
	var encoder *zstd.Encoder
	if v := zstdEncoderPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		encoder = v.(*zstd.Encoder)
	} else {
		compressionPoolNews.Add(1)
		var err error
		encoder, err = zstd.NewWriter(nil,
			zstd.WithEncoderLevel(mapZstdLevel(level)),
			zstd.WithEncoderConcurrency(1),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
		}
	}

	// Get pooled destination buffer — avoids per-call allocation.
	dstBuf := zstdCompressDstPool.Get().(*[]byte)
	result := encoder.EncodeAll(data, (*dstBuf)[:0])

	// Copy to caller-owned []byte (compressed output is typically small).
	owned := make([]byte, len(result))
	copy(owned, result)

	// Return buffers to pools (cap-gate prevents retaining outliers).
	*dstBuf = result[:0]
	if cap(*dstBuf) <= maxPooledBufCap {
		zstdCompressDstPool.Put(dstBuf)
	}
	compressionPoolPuts.Add(1)
	zstdEncoderPool.Put(encoder)

	return owned, nil
}

// decompressZstdDecodeAll decompresses using DecodeAll with pooled decoder and
// destination buffer. DecodeAll avoids streaming overhead and the 20 allocations
// per call that the streaming path requires.
func decompressZstdDecodeAll(data []byte) ([]byte, error) {
	var decoder *zstd.Decoder
	if v := zstdDecoderPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		decoder = v.(*zstd.Decoder)
	} else {
		compressionPoolNews.Add(1)
		var err error
		decoder, err = zstd.NewReader(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
	}

	// Get pooled destination buffer — avoids per-call allocation.
	dstBuf := zstdDecompressDstPool.Get().(*[]byte)
	result, err := decoder.DecodeAll(data, (*dstBuf)[:0])
	if err != nil {
		compressionPoolDiscards.Add(1)
		decoder.Close()
		return nil, fmt.Errorf("zstd decompression failed: %w", err)
	}

	// Copy to caller-owned []byte.
	owned := make([]byte, len(result))
	copy(owned, result)

	// Return buffers to pools.
	*dstBuf = result[:0]
	if cap(*dstBuf) <= maxPooledBufCap {
		zstdDecompressDstPool.Put(dstBuf)
	}
	compressionPoolPuts.Add(1)
	zstdDecoderPool.Put(decoder)

	return owned, nil
}

// compressZstd wraps compressZstdEncodeAll for callers that need io.Writer output.
// Used by CompressToBuf streaming fallback path and tests.
func compressZstd(w io.Writer, data []byte, level Level) error {
	result, err := compressZstdEncodeAll(data, level)
	if err != nil {
		return err
	}
	_, err = w.Write(result)
	return err
}

// decompressZstd wraps decompressZstdDecodeAll. Retained for test compatibility.
func decompressZstd(data []byte) ([]byte, error) {
	return decompressZstdDecodeAll(data)
}

// -----------------------------------------------------------------------
// snappy compression (allocation-free, no pooling needed)
// -----------------------------------------------------------------------

func compressSnappy(data []byte) []byte {
	return s2.EncodeSnappy(nil, data)
}

func decompressSnappy(data []byte) ([]byte, error) {
	return s2.Decode(nil, data)
}

// -----------------------------------------------------------------------
// zlib compression (pooled)
// -----------------------------------------------------------------------

func compressZlib(w io.Writer, data []byte, level Level) error {
	zlibLevel := zlib.DefaultCompression
	if level != LevelDefault {
		zlibLevel = int(level)
	}

	var zw *zlib.Writer
	if v := zlibWriterPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		zw = v.(*zlib.Writer)
		zw.Reset(w)
	} else {
		compressionPoolNews.Add(1)
		var err error
		zw, err = zlib.NewWriterLevel(w, zlibLevel)
		if err != nil {
			return fmt.Errorf("failed to create zlib writer: %w", err)
		}
	}

	if _, err := zw.Write(data); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to write zlib data: %w", err)
	}
	if err := zw.Close(); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to close zlib writer: %w", err)
	}
	compressionPoolPuts.Add(1)
	zlibWriterPool.Put(zw)
	return nil
}

func decompressZlib(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create zlib reader: %w", err)
	}
	defer zr.Close()
	return readAllPooled(zr)
}

// -----------------------------------------------------------------------
// deflate compression (pooled)
// -----------------------------------------------------------------------

func compressDeflate(w io.Writer, data []byte, level Level) error {
	deflateLevel := flate.DefaultCompression
	if level != LevelDefault {
		deflateLevel = int(level)
	}

	var fw *flate.Writer
	if v := flateWriterPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		fw = v.(*flate.Writer)
		fw.Reset(w)
	} else {
		compressionPoolNews.Add(1)
		var err error
		fw, err = flate.NewWriter(w, deflateLevel)
		if err != nil {
			return fmt.Errorf("failed to create deflate writer: %w", err)
		}
	}

	if _, err := fw.Write(data); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to write deflate data: %w", err)
	}
	if err := fw.Close(); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to close deflate writer: %w", err)
	}
	compressionPoolPuts.Add(1)
	flateWriterPool.Put(fw)
	return nil
}

func decompressDeflate(data []byte) ([]byte, error) {
	fr := flate.NewReader(bytes.NewReader(data))
	defer fr.Close()
	return readAllPooled(fr)
}

// -----------------------------------------------------------------------
// Pooled read helpers
// -----------------------------------------------------------------------

// readAllPooled reads all data from r using a pooled intermediate buffer,
// then returns an exact-sized copy. This avoids the repeated growth allocations
// that io.ReadAll performs (512 → 1K → 2K → ...) by reusing a buffer that
// already has capacity from previous calls.
func readAllPooled(r io.Reader) ([]byte, error) {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	_, err := buf.ReadFrom(r)
	if err != nil {
		bufPool.Put(buf)
		return nil, err
	}
	// Copy to exact-sized slice so buffer can return to pool safely.
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	bufPool.Put(buf)
	return result, nil
}
