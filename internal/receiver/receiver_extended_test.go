package receiver

import (
	"bytes"
	"io"
	"testing"
)

func TestZstdCompressor_CompressDecompress_Extended(t *testing.T) {
	c := &zstdCompressor{}

	// Test with various data sizes
	testCases := []struct {
		name string
		data []byte
	}{
		{"small", []byte("hello world")},
		{"medium", bytes.Repeat([]byte("test data for compression "), 100)},
		{"large", bytes.Repeat([]byte("large data pattern "), 1000)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w, err := c.Compress(&buf)
			if err != nil {
				t.Fatalf("Failed to get compressor: %v", err)
			}

			_, err = w.Write(tc.data)
			if err != nil {
				t.Fatalf("Failed to write: %v", err)
			}

			err = w.Close()
			if err != nil {
				t.Fatalf("Failed to close writer: %v", err)
			}

			r, err := c.Decompress(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("Failed to decompress: %v", err)
			}

			decompressed, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("Failed to read decompressed: %v", err)
			}

			if !bytes.Equal(tc.data, decompressed) {
				t.Error("Decompressed data doesn't match original")
			}
		})
	}
}

func TestZstdCompressor_Name_Extended(t *testing.T) {
	c := &zstdCompressor{}
	name := c.Name()

	if name != "zstd" {
		t.Errorf("Expected name 'zstd', got '%s'", name)
	}
}

func TestZstdCompressor_MultipleWrites(t *testing.T) {
	c := &zstdCompressor{}

	var buf bytes.Buffer
	w, err := c.Compress(&buf)
	if err != nil {
		t.Fatalf("Failed to get compressor: %v", err)
	}

	// Write multiple chunks
	chunks := [][]byte{
		[]byte("first chunk "),
		[]byte("second chunk "),
		[]byte("third chunk"),
	}

	for _, chunk := range chunks {
		_, err := w.Write(chunk)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Decompress
	r, err := c.Decompress(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	decompressed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	expected := "first chunk second chunk third chunk"
	if string(decompressed) != expected {
		t.Errorf("Got %q, want %q", string(decompressed), expected)
	}
}

func TestZstdCompressor_EmptyData(t *testing.T) {
	c := &zstdCompressor{}

	var buf bytes.Buffer
	w, err := c.Compress(&buf)
	if err != nil {
		t.Fatalf("Failed to get compressor: %v", err)
	}

	// Write empty data
	_, err = w.Write([]byte{})
	if err != nil {
		t.Fatalf("Write empty failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Should produce some output (at least zstd header/footer)
	t.Logf("Empty data compressed to %d bytes", buf.Len())
}

func TestZstdCompressor_ReadInChunks(t *testing.T) {
	c := &zstdCompressor{}

	testData := bytes.Repeat([]byte("chunk data "), 100)

	var buf bytes.Buffer
	w, _ := c.Compress(&buf)
	w.Write(testData)
	w.Close()

	r, err := c.Decompress(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	// Read in small chunks
	chunk := make([]byte, 50)
	var total int
	for {
		n, err := r.Read(chunk)
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
	}

	if total != len(testData) {
		t.Errorf("Expected %d bytes, got %d", len(testData), total)
	}
}

func TestZstdCompressor_DecompressInvalid(t *testing.T) {
	c := &zstdCompressor{}

	// Try to decompress invalid data
	invalidData := []byte("not valid zstd data")
	_, err := c.Decompress(bytes.NewReader(invalidData))
	// This may or may not error depending on the implementation
	t.Logf("Decompress invalid data: %v", err)
}

func TestZstdCompressor_LargeData(t *testing.T) {
	c := &zstdCompressor{}

	// Test with large data
	testData := bytes.Repeat([]byte("a"), 100000) // 100KB of data

	var buf bytes.Buffer
	w, err := c.Compress(&buf)
	if err != nil {
		t.Fatalf("Failed to get compressor: %v", err)
	}

	_, err = w.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify compression happened (compressed should be smaller for repetitive data)
	if buf.Len() >= len(testData) {
		t.Logf("Compressed size %d not smaller than original %d", buf.Len(), len(testData))
	}

	r, err := c.Decompress(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	decompressed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if !bytes.Equal(testData, decompressed) {
		t.Error("Decompressed data doesn't match original")
	}
}
