package compression

import (
	"bytes"
	"testing"
)

func TestParseType(t *testing.T) {
	tests := []struct {
		input    string
		expected Type
		wantErr  bool
	}{
		{"", TypeNone, false},
		{"none", TypeNone, false},
		{"gzip", TypeGzip, false},
		{"GZIP", TypeGzip, false},
		{"zstd", TypeZstd, false},
		{"snappy", TypeSnappy, false},
		{"zlib", TypeZlib, false},
		{"deflate", TypeDeflate, false},
		{"lz4", TypeLZ4, false},
		{"unknown", TypeNone, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("ParseType(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestContentEncoding(t *testing.T) {
	tests := []struct {
		t        Type
		expected string
	}{
		{TypeNone, ""},
		{TypeGzip, "gzip"},
		{TypeZstd, "zstd"},
		{TypeSnappy, "snappy"},
		{TypeZlib, "zlib"},
		{TypeDeflate, "deflate"},
		{TypeLZ4, "lz4"},
	}

	for _, tt := range tests {
		t.Run(string(tt.t), func(t *testing.T) {
			got := tt.t.ContentEncoding()
			if got != tt.expected {
				t.Errorf("ContentEncoding() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseContentEncoding(t *testing.T) {
	tests := []struct {
		input    string
		expected Type
	}{
		{"", TypeNone},
		{"gzip", TypeGzip},
		{"x-gzip", TypeGzip},
		{"zstd", TypeZstd},
		{"snappy", TypeSnappy},
		{"x-snappy-framed", TypeSnappy},
		{"zlib", TypeZlib},
		{"deflate", TypeDeflate},
		{"lz4", TypeLZ4},
		{"unknown", TypeNone},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseContentEncoding(tt.input)
			if got != tt.expected {
				t.Errorf("ParseContentEncoding(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCompressDecompress(t *testing.T) {
	testData := []byte("Hello, World! This is some test data for compression testing. Let's make it a bit longer to see actual compression.")

	tests := []struct {
		name  string
		cfg   Config
	}{
		{"none", Config{Type: TypeNone}},
		{"gzip-default", Config{Type: TypeGzip, Level: LevelDefault}},
		{"gzip-fast", Config{Type: TypeGzip, Level: GzipBestSpeed}},
		{"gzip-best", Config{Type: TypeGzip, Level: GzipBestCompression}},
		{"zstd-default", Config{Type: TypeZstd, Level: LevelDefault}},
		{"zstd-fastest", Config{Type: TypeZstd, Level: ZstdSpeedFastest}},
		{"zstd-best", Config{Type: TypeZstd, Level: ZstdSpeedBestCompression}},
		{"snappy", Config{Type: TypeSnappy}},
		{"zlib-default", Config{Type: TypeZlib, Level: LevelDefault}},
		{"zlib-best", Config{Type: TypeZlib, Level: LevelBest}},
		{"deflate-default", Config{Type: TypeDeflate, Level: LevelDefault}},
		{"deflate-best", Config{Type: TypeDeflate, Level: LevelBest}},
		{"lz4-default", Config{Type: TypeLZ4, Level: LevelDefault}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := Compress(testData, tt.cfg)
			if err != nil {
				t.Fatalf("Compress() error = %v", err)
			}

			decompressed, err := Decompress(compressed, tt.cfg.Type)
			if err != nil {
				t.Fatalf("Decompress() error = %v", err)
			}

			if !bytes.Equal(decompressed, testData) {
				t.Errorf("Decompressed data doesn't match original. Got %d bytes, want %d bytes", len(decompressed), len(testData))
			}
		})
	}
}

func TestCompressNone(t *testing.T) {
	data := []byte("test data")
	cfg := Config{Type: TypeNone}

	compressed, err := Compress(data, cfg)
	if err != nil {
		t.Fatalf("Compress() error = %v", err)
	}

	if !bytes.Equal(compressed, data) {
		t.Error("Compress with TypeNone should return original data")
	}
}

func TestDecompressNone(t *testing.T) {
	data := []byte("test data")

	decompressed, err := Decompress(data, TypeNone)
	if err != nil {
		t.Fatalf("Decompress() error = %v", err)
	}

	if !bytes.Equal(decompressed, data) {
		t.Error("Decompress with TypeNone should return original data")
	}
}

func TestCompressUnsupportedType(t *testing.T) {
	_, err := Compress([]byte("test"), Config{Type: Type("invalid")})
	if err == nil {
		t.Error("expected error for unsupported compression type")
	}
}

func TestDecompressUnsupportedType(t *testing.T) {
	_, err := Decompress([]byte("test"), Type("invalid"))
	if err == nil {
		t.Error("expected error for unsupported compression type")
	}
}

func TestDecompressInvalidData(t *testing.T) {
	invalidData := []byte("not compressed data")

	tests := []Type{
		TypeGzip,
		TypeZstd,
		TypeSnappy,
		TypeZlib,
		TypeDeflate,
		TypeLZ4,
	}

	for _, tt := range tests {
		t.Run(string(tt), func(t *testing.T) {
			_, err := Decompress(invalidData, tt)
			if err == nil {
				t.Errorf("expected error for invalid %s data", tt)
			}
		})
	}
}

func TestEmptyData(t *testing.T) {
	emptyData := []byte{}

	tests := []Config{
		{Type: TypeNone},
		{Type: TypeGzip},
		{Type: TypeZstd},
		{Type: TypeSnappy},
		{Type: TypeZlib},
		{Type: TypeDeflate},
		{Type: TypeLZ4},
	}

	for _, tt := range tests {
		t.Run(string(tt.Type), func(t *testing.T) {
			compressed, err := Compress(emptyData, tt)
			if err != nil {
				t.Fatalf("Compress() error = %v", err)
			}

			decompressed, err := Decompress(compressed, tt.Type)
			if err != nil {
				t.Fatalf("Decompress() error = %v", err)
			}

			if !bytes.Equal(decompressed, emptyData) {
				t.Error("Decompressed data should be empty")
			}
		})
	}
}
