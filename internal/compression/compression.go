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
	ZstdSpeedFastest          Level = 1
	ZstdSpeedDefault          Level = 3
	ZstdSpeedBetterCompression Level = 6
	ZstdSpeedBestCompression  Level = 11
)

// Config holds compression configuration.
type Config struct {
	// Type is the compression algorithm to use.
	Type Type
	// Level is the compression level (algorithm-specific).
	Level Level
}

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

	var buf bytes.Buffer
	var err error

	switch cfg.Type {
	case TypeGzip:
		err = compressGzip(&buf, data, cfg.Level)
	case TypeZstd:
		err = compressZstd(&buf, data, cfg.Level)
	case TypeSnappy:
		return compressSnappy(data), nil
	case TypeZlib:
		err = compressZlib(&buf, data, cfg.Level)
	case TypeDeflate:
		err = compressDeflate(&buf, data, cfg.Level)
	case TypeLZ4:
		err = compressLZ4(&buf, data, cfg.Level)
	default:
		return nil, fmt.Errorf("unsupported compression type: %s", cfg.Type)
	}

	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

// gzip compression
func compressGzip(w io.Writer, data []byte, level Level) error {
	gzLevel := gzip.DefaultCompression
	if level != LevelDefault {
		gzLevel = int(level)
	}
	gw, err := gzip.NewWriterLevel(w, gzLevel)
	if err != nil {
		return fmt.Errorf("failed to create gzip writer: %w", err)
	}
	if _, err := gw.Write(data); err != nil {
		return fmt.Errorf("failed to write gzip data: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}
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

// zstd compression
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
	encoder, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstdLevel))
	if err != nil {
		return fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	if _, err := encoder.Write(data); err != nil {
		return fmt.Errorf("failed to write zstd data: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to close zstd encoder: %w", err)
	}
	return nil
}

func decompressZstd(data []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
	}
	defer decoder.Close()
	return io.ReadAll(decoder)
}

// snappy compression
func compressSnappy(data []byte) []byte {
	return snappy.Encode(nil, data)
}

func decompressSnappy(data []byte) ([]byte, error) {
	return snappy.Decode(nil, data)
}

// zlib compression
func compressZlib(w io.Writer, data []byte, level Level) error {
	zlibLevel := zlib.DefaultCompression
	if level != LevelDefault {
		zlibLevel = int(level)
	}
	zw, err := zlib.NewWriterLevel(w, zlibLevel)
	if err != nil {
		return fmt.Errorf("failed to create zlib writer: %w", err)
	}
	if _, err := zw.Write(data); err != nil {
		return fmt.Errorf("failed to write zlib data: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to close zlib writer: %w", err)
	}
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

// deflate compression
func compressDeflate(w io.Writer, data []byte, level Level) error {
	deflateLevel := flate.DefaultCompression
	if level != LevelDefault {
		deflateLevel = int(level)
	}
	fw, err := flate.NewWriter(w, deflateLevel)
	if err != nil {
		return fmt.Errorf("failed to create deflate writer: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return fmt.Errorf("failed to write deflate data: %w", err)
	}
	if err := fw.Close(); err != nil {
		return fmt.Errorf("failed to close deflate writer: %w", err)
	}
	return nil
}

func decompressDeflate(data []byte) ([]byte, error) {
	fr := flate.NewReader(bytes.NewReader(data))
	defer fr.Close()
	return io.ReadAll(fr)
}

// lz4 compression
func compressLZ4(w io.Writer, data []byte, level Level) error {
	lw := lz4.NewWriter(w)
	if level != LevelDefault {
		lw.Apply(lz4.CompressionLevelOption(lz4.CompressionLevel(level)))
	}
	if _, err := lw.Write(data); err != nil {
		return fmt.Errorf("failed to write lz4 data: %w", err)
	}
	if err := lw.Close(); err != nil {
		return fmt.Errorf("failed to close lz4 writer: %w", err)
	}
	return nil
}

func decompressLZ4(data []byte) ([]byte, error) {
	lr := lz4.NewReader(bytes.NewReader(data))
	return io.ReadAll(lr)
}
