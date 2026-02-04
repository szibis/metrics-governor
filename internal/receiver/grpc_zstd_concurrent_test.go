package receiver

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
)

// TestConcurrentZstdCompressor verifies the gRPC zstd compressor is safe
// for concurrent use. This exercises the zstdWriterPoolWrapper and
// zstdReaderPoolWrapper with many goroutines, catching races after the
// klauspost/compress v1.18.0 → v1.18.3 bump.
func TestConcurrentZstdCompressor(t *testing.T) {
	c := &zstdCompressor{}

	const goroutines = 30
	const iterations = 20
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			testData := bytes.Repeat([]byte(fmt.Sprintf("grpc-zstd-concurrent-%03d-", id)), 50)

			for i := 0; i < iterations; i++ {
				// Compress
				var compressedBuf bytes.Buffer
				writer, err := c.Compress(&compressedBuf)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Compress: %w", id, i, err)
					return
				}

				if _, err := writer.Write(testData); err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Write: %w", id, i, err)
					return
				}

				if err := writer.Close(); err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Close writer: %w", id, i, err)
					return
				}

				// Decompress
				reader, err := c.Decompress(bytes.NewReader(compressedBuf.Bytes()))
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Decompress: %w", id, i, err)
					return
				}

				decompressed, err := io.ReadAll(reader)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: ReadAll: %w", id, i, err)
					return
				}

				if !bytes.Equal(testData, decompressed) {
					errCh <- fmt.Errorf("goroutine %d iter %d: data mismatch (got %d bytes, want %d)",
						id, i, len(decompressed), len(testData))
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

// TestZstdPooledWriterReuse verifies that the zstd writer pool wrapper
// correctly handles encoder reuse across multiple sequential uses.
func TestZstdPooledWriterReuse(t *testing.T) {
	wrapper := &zstdWriterPoolWrapper{}

	for i := 0; i < 50; i++ {
		testData := bytes.Repeat([]byte(fmt.Sprintf("reuse-iter-%03d-", i)), 30)

		var buf bytes.Buffer
		writer, err := wrapper.Get(&buf)
		if err != nil {
			t.Fatalf("iteration %d: Get failed: %v", i, err)
		}

		if _, err := writer.Write(testData); err != nil {
			t.Fatalf("iteration %d: Write failed: %v", i, err)
		}

		if err := writer.Close(); err != nil {
			t.Fatalf("iteration %d: Close failed: %v", i, err)
		}

		// Verify by decompressing
		readerWrapper := &zstdReaderPoolWrapper{}
		reader, err := readerWrapper.Get(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("iteration %d: Get reader failed: %v", i, err)
		}

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("iteration %d: ReadAll failed: %v", i, err)
		}

		if !bytes.Equal(testData, decompressed) {
			t.Fatalf("iteration %d: data mismatch", i)
		}
	}
}

// TestZstdPooledReaderReuse verifies that the zstd reader pool wrapper
// correctly handles decoder reuse across multiple sequential uses.
func TestZstdPooledReaderReuse(t *testing.T) {
	// Pre-compress several different payloads
	type testCase struct {
		data       []byte
		compressed []byte
	}

	wrapper := &zstdWriterPoolWrapper{}
	readerWrapper := &zstdReaderPoolWrapper{}

	cases := make([]testCase, 20)
	for i := range cases {
		cases[i].data = bytes.Repeat([]byte(fmt.Sprintf("reader-reuse-%03d-", i)), 40)

		var buf bytes.Buffer
		w, err := wrapper.Get(&buf)
		if err != nil {
			t.Fatalf("setup %d: Get writer failed: %v", i, err)
		}
		if _, err := w.Write(cases[i].data); err != nil {
			t.Fatalf("setup %d: Write failed: %v", i, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("setup %d: Close failed: %v", i, err)
		}
		cases[i].compressed = buf.Bytes()
	}

	// Now decompress them all sequentially (exercises reader pool reuse)
	for i, tc := range cases {
		reader, err := readerWrapper.Get(bytes.NewReader(tc.compressed))
		if err != nil {
			t.Fatalf("case %d: Get reader failed: %v", i, err)
		}

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("case %d: ReadAll failed: %v", i, err)
		}

		if !bytes.Equal(decompressed, tc.data) {
			t.Fatalf("case %d: data mismatch (got %d bytes, want %d)", i, len(decompressed), len(tc.data))
		}
	}
}

// TestZstdCompressorLargePayload tests the gRPC zstd compressor with a
// large payload to verify buffering behavior after the library bump.
func TestZstdCompressorLargePayload(t *testing.T) {
	c := &zstdCompressor{}

	// ~1MB payload
	testData := bytes.Repeat([]byte("large-grpc-zstd-payload-test-data-with-patterns-"), 25000)

	var compressedBuf bytes.Buffer
	writer, err := c.Compress(&compressedBuf)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if _, err := writer.Write(testData); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify compression happened
	if compressedBuf.Len() >= len(testData) {
		t.Errorf("expected compression to reduce size: compressed=%d, original=%d",
			compressedBuf.Len(), len(testData))
	}

	reader, err := c.Decompress(bytes.NewReader(compressedBuf.Bytes()))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if !bytes.Equal(testData, decompressed) {
		t.Errorf("data mismatch: got %d bytes, want %d bytes", len(decompressed), len(testData))
	}

	t.Logf("compressed %d -> %d bytes (%.1f%% reduction)",
		len(testData), compressedBuf.Len(),
		100*(1-float64(compressedBuf.Len())/float64(len(testData))))
}
