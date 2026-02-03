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

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
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
	// TypeLZ4 uses lz4 compression.
	TypeLZ4 Type = "lz4"
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
	lz4WriterPool   sync.Pool
	zstdEncoderPool sync.Pool
	zstdDecoderPool sync.Pool
	lz4ReaderPool   sync.Pool
)

// Pool metrics - exported via PoolStats().
var (
	compressionPoolGets     atomic.Int64
	compressionPoolPuts     atomic.Int64
	compressionPoolDiscards atomic.Int64
	compressionPoolNews     atomic.Int64
	bufferPoolGets          atomic.Int64
	bufferPoolPuts          atomic.Int64
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

// ResetPoolStats resets all pool metric counters to zero (useful in tests).
func ResetPoolStats() {
	compressionPoolGets.Store(0)
	compressionPoolPuts.Store(0)
	compressionPoolDiscards.Store(0)
	compressionPoolNews.Store(0)
	bufferPoolGets.Store(0)
	bufferPoolPuts.Store(0)
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
	case "lz4":
		return TypeLZ4, nil
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
	case TypeLZ4:
		return "lz4"
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
	case "lz4":
		return TypeLZ4
	default:
		return TypeNone
	}
}

// Compress compresses data using the specified compression type and level.
func Compress(data []byte, cfg Config) ([]byte, error) {
	if cfg.Type == TypeNone || cfg.Type == "" {
		return data, nil
	}

	// Snappy is allocation-free and doesn't need buffer pooling.
	if cfg.Type == TypeSnappy {
		return compressSnappy(data), nil
	}

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	bufferPoolGets.Add(1)

	var err error
	switch cfg.Type {
	case TypeGzip:
		err = compressGzip(buf, data, cfg.Level)
	case TypeZstd:
		err = compressZstd(buf, data, cfg.Level)
	case TypeZlib:
		err = compressZlib(buf, data, cfg.Level)
	case TypeDeflate:
		err = compressDeflate(buf, data, cfg.Level)
	case TypeLZ4:
		err = compressLZ4(buf, data, cfg.Level)
	default:
		bufferPoolPuts.Add(1)
		bufPool.Put(buf)
		return nil, fmt.Errorf("unsupported compression type: %s", cfg.Type)
	}

	if err != nil {
		bufferPoolPuts.Add(1)
		bufPool.Put(buf)
		return nil, err
	}

	// Copy result so the buffer can be returned to the pool safely.
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	bufferPoolPuts.Add(1)
	bufPool.Put(buf)
	return result, nil
}

// Decompress decompresses data using the specified compression type.
func Decompress(data []byte, compressionType Type) ([]byte, error) {
	if compressionType == TypeNone || compressionType == "" {
		return data, nil
	}

	switch compressionType {
	case TypeGzip:
		return decompressGzip(data)
	case TypeZstd:
		return decompressZstd(data)
	case TypeSnappy:
		return decompressSnappy(data)
	case TypeZlib:
		return decompressZlib(data)
	case TypeDeflate:
		return decompressDeflate(data)
	case TypeLZ4:
		return decompressLZ4(data)
	default:
		return nil, fmt.Errorf("unsupported compression type: %s", compressionType)
	}
}

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
	return io.ReadAll(gr)
}

// -----------------------------------------------------------------------
// zstd compression (pooled encoder + decoder)
// -----------------------------------------------------------------------

func compressZstd(w io.Writer, data []byte, level Level) error {
	zstdLevel := zstd.SpeedDefault
	switch level {
	case ZstdSpeedFastest:
		zstdLevel = zstd.SpeedFastest
	case ZstdSpeedBetterCompression:
		zstdLevel = zstd.SpeedBetterCompression
	case ZstdSpeedBestCompression:
		zstdLevel = zstd.SpeedBestCompression
	}

	var encoder *zstd.Encoder
	if v := zstdEncoderPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		encoder = v.(*zstd.Encoder)
		encoder.Reset(w)
	} else {
		compressionPoolNews.Add(1)
		var err error
		encoder, err = zstd.NewWriter(w, zstd.WithEncoderLevel(zstdLevel))
		if err != nil {
			return fmt.Errorf("failed to create zstd encoder: %w", err)
		}
	}

	if _, err := encoder.Write(data); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to write zstd data: %w", err)
	}
	if err := encoder.Close(); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to close zstd encoder: %w", err)
	}
	encoder.Reset(nil) // detach from writer before pooling
	compressionPoolPuts.Add(1)
	zstdEncoderPool.Put(encoder)
	return nil
}

func decompressZstd(data []byte) ([]byte, error) {
	var decoder *zstd.Decoder
	if v := zstdDecoderPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		decoder = v.(*zstd.Decoder)
		if err := decoder.Reset(bytes.NewReader(data)); err != nil {
			// Reset failed - discard this decoder, create a fresh one.
			decoder.Close()
			compressionPoolDiscards.Add(1)
			var err2 error
			decoder, err2 = zstd.NewReader(bytes.NewReader(data))
			if err2 != nil {
				return nil, fmt.Errorf("failed to create zstd decoder: %w", err2)
			}
			compressionPoolNews.Add(1)
		}
	} else {
		compressionPoolNews.Add(1)
		var err error
		decoder, err = zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
	}

	result, err := io.ReadAll(decoder)
	if err != nil {
		decoder.Close()
		compressionPoolDiscards.Add(1)
		return nil, err
	}
	_ = decoder.Reset(nil) // detach before pooling
	compressionPoolPuts.Add(1)
	zstdDecoderPool.Put(decoder)
	return result, nil
}

// -----------------------------------------------------------------------
// snappy compression (allocation-free, no pooling needed)
// -----------------------------------------------------------------------

func compressSnappy(data []byte) []byte {
	return snappy.Encode(nil, data)
}

func decompressSnappy(data []byte) ([]byte, error) {
	return snappy.Decode(nil, data)
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
	return io.ReadAll(zr)
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
	return io.ReadAll(fr)
}

// -----------------------------------------------------------------------
// lz4 compression (pooled writer + reader)
// -----------------------------------------------------------------------

func compressLZ4(w io.Writer, data []byte, level Level) error {
	var lw *lz4.Writer
	if v := lz4WriterPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		lw = v.(*lz4.Writer)
		lw.Reset(w)
	} else {
		compressionPoolNews.Add(1)
		lw = lz4.NewWriter(w)
	}

	if level != LevelDefault {
		if err := lw.Apply(lz4.CompressionLevelOption(lz4.CompressionLevel(level))); err != nil {
			compressionPoolDiscards.Add(1)
			return fmt.Errorf("failed to apply lz4 compression level: %w", err)
		}
	}

	if _, err := lw.Write(data); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to write lz4 data: %w", err)
	}
	if err := lw.Close(); err != nil {
		compressionPoolDiscards.Add(1)
		return fmt.Errorf("failed to close lz4 writer: %w", err)
	}
	compressionPoolPuts.Add(1)
	lz4WriterPool.Put(lw)
	return nil
}

func decompressLZ4(data []byte) ([]byte, error) {
	var lr *lz4.Reader
	if v := lz4ReaderPool.Get(); v != nil {
		compressionPoolGets.Add(1)
		lr = v.(*lz4.Reader)
		lr.Reset(bytes.NewReader(data))
	} else {
		compressionPoolNews.Add(1)
		lr = lz4.NewReader(bytes.NewReader(data))
	}

	result, err := io.ReadAll(lr)
	if err != nil {
		compressionPoolDiscards.Add(1)
		return nil, err
	}
	compressionPoolPuts.Add(1)
	lz4ReaderPool.Put(lr)
	return result, nil
}
