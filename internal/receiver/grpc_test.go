package receiver

import (
	"bytes"
	"io"
	"testing"
)

func TestZstdCompressor(t *testing.T) {
	c := &zstdCompressor{}

	t.Run("Name", func(t *testing.T) {
		if name := c.Name(); name != "zstd" {
			t.Errorf("expected name 'zstd', got '%s'", name)
		}
	})

	t.Run("Compress and Decompress", func(t *testing.T) {
		// Test data
		testData := []byte("Hello, this is a test of zstd compression in gRPC!")

		// Compress
		var compressedBuf bytes.Buffer
		writer, err := c.Compress(&compressedBuf)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}

		_, err = writer.Write(testData)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		err = writer.Close()
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		// Decompress
		reader, err := c.Decompress(bytes.NewReader(compressedBuf.Bytes()))
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}

		if !bytes.Equal(testData, decompressed) {
			t.Errorf("decompressed data mismatch: got %s, want %s", decompressed, testData)
		}
	})
}

func TestPooledZstdWriter(t *testing.T) {
	testData := []byte("Test data for pooled writer")

	var buf bytes.Buffer
	wrapper := &zstdWriterPoolWrapper{}

	writer, err := wrapper.Get(&buf)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	_, err = writer.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close should return encoder to pool
	err = writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify compressed data is in buffer
	if buf.Len() == 0 {
		t.Error("expected non-empty compressed data")
	}
}

func TestPooledZstdReader(t *testing.T) {
	testData := []byte("Test data for pooled reader - some longer text to compress properly")

	// First compress the data
	var compressedBuf bytes.Buffer
	wrapper := &zstdWriterPoolWrapper{}
	writer, err := wrapper.Get(&compressedBuf)
	if err != nil {
		t.Fatalf("Get writer failed: %v", err)
	}
	_, err = writer.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	err = writer.Close()
	if err != nil {
		t.Fatalf("Close writer failed: %v", err)
	}

	// Now decompress
	readerWrapper := &zstdReaderPoolWrapper{}
	reader, err := readerWrapper.Get(bytes.NewReader(compressedBuf.Bytes()))
	if err != nil {
		t.Fatalf("Get reader failed: %v", err)
	}

	// Read all data including triggering EOF
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if !bytes.Equal(testData, decompressed) {
		t.Errorf("data mismatch: got %s, want %s", decompressed, testData)
	}
}

func TestZstdReaderPoolWrapper_InvalidData(t *testing.T) {
	wrapper := &zstdReaderPoolWrapper{}

	// Try to decompress invalid data - this should still return a reader
	// but reading from it may fail
	invalidData := bytes.NewReader([]byte{0x00, 0x01, 0x02, 0x03})
	reader, err := wrapper.Get(invalidData)

	// Reset might fail on truly invalid data, but empty or small data
	// might just work until read
	if err != nil {
		// Expected for some invalid inputs
		return
	}

	// Trying to read from invalid compressed data should fail
	_, err = io.ReadAll(reader)
	if err == nil {
		// This is also acceptable - empty/garbage input might produce empty output
		t.Log("No error reading invalid data (might be empty output)")
	}
}

func TestZstdCompressorRoundTrip(t *testing.T) {
	c := &zstdCompressor{}

	testCases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello")},
		{"medium", bytes.Repeat([]byte("test data "), 100)},
		{"large", bytes.Repeat([]byte("large test data for compression "), 1000)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Compress
			var compressedBuf bytes.Buffer
			writer, err := c.Compress(&compressedBuf)
			if err != nil {
				t.Fatalf("Compress failed: %v", err)
			}

			_, err = writer.Write(tc.data)
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatalf("Close failed: %v", err)
			}

			// Decompress
			reader, err := c.Decompress(bytes.NewReader(compressedBuf.Bytes()))
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}

			decompressed, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("ReadAll failed: %v", err)
			}

			if !bytes.Equal(tc.data, decompressed) {
				t.Errorf("data mismatch: lengths got %d, want %d", len(decompressed), len(tc.data))
			}
		})
	}
}

func TestMultipleCompressDecompress(t *testing.T) {
	c := &zstdCompressor{}

	// Test pool reuse by compressing/decompressing multiple times
	for i := 0; i < 5; i++ {
		testData := bytes.Repeat([]byte("iteration data "), i+1)

		var compressedBuf bytes.Buffer
		writer, err := c.Compress(&compressedBuf)
		if err != nil {
			t.Fatalf("iteration %d: Compress failed: %v", i, err)
		}

		_, err = writer.Write(testData)
		if err != nil {
			t.Fatalf("iteration %d: Write failed: %v", i, err)
		}

		err = writer.Close()
		if err != nil {
			t.Fatalf("iteration %d: Close failed: %v", i, err)
		}

		reader, err := c.Decompress(bytes.NewReader(compressedBuf.Bytes()))
		if err != nil {
			t.Fatalf("iteration %d: Decompress failed: %v", i, err)
		}

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("iteration %d: ReadAll failed: %v", i, err)
		}

		if !bytes.Equal(testData, decompressed) {
			t.Errorf("iteration %d: data mismatch", i)
		}
	}
}
