//go:build cgo && native_compress

package compression

/*
#cgo linux LDFLAGS: -L${SRCDIR}/../../rust/compress-ffi/target/release -lcompress_ffi -lm -ldl -lpthread
#cgo darwin LDFLAGS: -L${SRCDIR}/../../rust/compress-ffi/target/release -lcompress_ffi -lm -ldl -lpthread -framework CoreFoundation -framework Security
#include <stdint.h>
#include <stddef.h>

// Gzip
extern int64_t native_gzip_compress(const uint8_t* src, size_t src_len,
                                     uint8_t* dst, size_t dst_cap,
                                     int32_t level);
extern int64_t native_gzip_decompress(const uint8_t* src, size_t src_len,
                                       uint8_t* dst, size_t dst_cap);

// Zlib
extern int64_t native_zlib_compress(const uint8_t* src, size_t src_len,
                                     uint8_t* dst, size_t dst_cap,
                                     int32_t level);
extern int64_t native_zlib_decompress(const uint8_t* src, size_t src_len,
                                       uint8_t* dst, size_t dst_cap);

// Deflate (raw)
extern int64_t native_deflate_compress(const uint8_t* src, size_t src_len,
                                        uint8_t* dst, size_t dst_cap,
                                        int32_t level);
extern int64_t native_deflate_decompress(const uint8_t* src, size_t src_len,
                                          uint8_t* dst, size_t dst_cap);

// Zstd (single-shot)
extern int64_t native_zstd_compress(const uint8_t* src, size_t src_len,
                                     uint8_t* dst, size_t dst_cap,
                                     int32_t level);
extern int64_t native_zstd_decompress(const uint8_t* src, size_t src_len,
                                       uint8_t* dst, size_t dst_cap);

// Zstd (persistent context — pooled on Go side for amortized allocation)
extern void* native_zstd_cctx_new(void);
extern void  native_zstd_cctx_free(void* ctx);
extern int64_t native_zstd_compress_ctx(void* ctx,
                                         const uint8_t* src, size_t src_len,
                                         uint8_t* dst, size_t dst_cap,
                                         int32_t level);
extern void* native_zstd_dctx_new(void);
extern void  native_zstd_dctx_free(void* ctx);
extern int64_t native_zstd_decompress_ctx(void* ctx,
                                           const uint8_t* src, size_t src_len,
                                           uint8_t* dst, size_t dst_cap);
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

const nativeCompressionAvailable = true

// zstdCCtxPool pools persistent Rust zstd compression contexts.
// Each context retains ~256KB of internal hash tables that are reused
// across calls, eliminating per-call allocation overhead.
var zstdCCtxPool = sync.Pool{
	New: func() any {
		return C.native_zstd_cctx_new()
	},
}

// zstdDCtxPool pools persistent Rust zstd decompression contexts.
var zstdDCtxPool = sync.Pool{
	New: func() any {
		return C.native_zstd_dctx_new()
	},
}

// nativeCompress calls the Rust FFI compression function for the given algorithm.
func nativeCompress(data []byte, cfg Config) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// Pre-allocate output buffer: compressed output is typically smaller than input,
	// but add headroom for incompressible data + header overhead.
	dstCap := len(data) + len(data)/4 + 128
	dst := make([]byte, dstCap)

	level := int32(cfg.Level)
	if level == int32(LevelDefault) {
		level = 6 // default compression level for gzip/zlib/deflate
	}

	var n C.int64_t
	srcPtr := (*C.uint8_t)(unsafe.Pointer(&data[0]))
	dstPtr := (*C.uint8_t)(unsafe.Pointer(&dst[0]))
	srcLen := C.size_t(len(data))
	dstCapC := C.size_t(dstCap)

	switch cfg.Type {
	case TypeGzip:
		n = C.native_gzip_compress(srcPtr, srcLen, dstPtr, dstCapC, C.int32_t(level))
	case TypeZstd:
		n = nativeZstdCompressPooled(data, dst, level)
	case TypeZlib:
		n = C.native_zlib_compress(srcPtr, srcLen, dstPtr, dstCapC, C.int32_t(level))
	case TypeDeflate:
		n = C.native_deflate_compress(srcPtr, srcLen, dstPtr, dstCapC, C.int32_t(level))
	default:
		return nil, fmt.Errorf("native compression: unsupported type %s", cfg.Type)
	}

	if n == -1 {
		// Buffer too small — retry with larger buffer
		dstCap = len(data) * 2
		dst = make([]byte, dstCap)
		dstPtr = (*C.uint8_t)(unsafe.Pointer(&dst[0]))
		dstCapC = C.size_t(dstCap)

		switch cfg.Type {
		case TypeGzip:
			n = C.native_gzip_compress(srcPtr, srcLen, dstPtr, dstCapC, C.int32_t(level))
		case TypeZstd:
			n = nativeZstdCompressPooled(data, dst, level)
		case TypeZlib:
			n = C.native_zlib_compress(srcPtr, srcLen, dstPtr, dstCapC, C.int32_t(level))
		case TypeDeflate:
			n = C.native_deflate_compress(srcPtr, srcLen, dstPtr, dstCapC, C.int32_t(level))
		}
	}

	if n < 0 {
		return nil, fmt.Errorf("native compression failed for %s (error code: %d)", cfg.Type, n)
	}

	return dst[:n], nil
}

// nativeZstdCompressPooled compresses using a pooled persistent Rust zstd context.
func nativeZstdCompressPooled(src, dst []byte, level int32) C.int64_t {
	// Map our level constants to zstd levels
	zstdLevel := level
	switch Level(level) {
	case ZstdSpeedFastest:
		zstdLevel = 1
	case ZstdSpeedDefault:
		zstdLevel = 3
	case ZstdSpeedBetterCompression:
		zstdLevel = 6
	case ZstdSpeedBestCompression:
		zstdLevel = 19
	}

	ctx := zstdCCtxPool.Get().(unsafe.Pointer)
	n := C.native_zstd_compress_ctx(
		ctx,
		(*C.uint8_t)(unsafe.Pointer(&src[0])), C.size_t(len(src)),
		(*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)),
		C.int32_t(zstdLevel),
	)
	zstdCCtxPool.Put(ctx)
	return n
}

// nativeDecompress calls the Rust FFI decompression function for the given algorithm.
func nativeDecompress(data []byte, compressionType Type) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// Start with 10x input size for decompression (compressed data can expand
	// dramatically, e.g. 1MB compresses to <1KB with zstd on patterned data).
	// Minimum 64KB to handle small compressed payloads that expand to larger outputs.
	dstCap := len(data) * 10
	if dstCap < 64*1024 {
		dstCap = 64 * 1024
	}
	dst := make([]byte, dstCap)

	srcPtr := (*C.uint8_t)(unsafe.Pointer(&data[0]))
	dstPtr := (*C.uint8_t)(unsafe.Pointer(&dst[0]))
	srcLen := C.size_t(len(data))

	callDecompress := func(newCap int) C.int64_t {
		if newCap != dstCap {
			dstCap = newCap
			dst = make([]byte, dstCap)
			dstPtr = (*C.uint8_t)(unsafe.Pointer(&dst[0]))
		}
		dstCapC := C.size_t(dstCap)
		switch compressionType {
		case TypeGzip:
			return C.native_gzip_decompress(srcPtr, srcLen, dstPtr, dstCapC)
		case TypeZstd:
			return nativeZstdDecompressPooled(data, dst)
		case TypeZlib:
			return C.native_zlib_decompress(srcPtr, srcLen, dstPtr, dstCapC)
		case TypeDeflate:
			return C.native_deflate_decompress(srcPtr, srcLen, dstPtr, dstCapC)
		default:
			return -2
		}
	}

	n := callDecompress(dstCap)

	// Retry with 8x larger buffers if too small (up to 5 retries = 8^5 = 32768x growth).
	for retries := 0; n == -1 && retries < 5; retries++ {
		n = callDecompress(dstCap * 8)
	}

	if n < 0 {
		return nil, fmt.Errorf("native decompression failed for %s (error code: %d)", compressionType, n)
	}

	return dst[:n], nil
}

// nativeZstdDecompressPooled decompresses using a pooled persistent Rust zstd context.
func nativeZstdDecompressPooled(src, dst []byte) C.int64_t {
	ctx := zstdDCtxPool.Get().(unsafe.Pointer)
	n := C.native_zstd_decompress_ctx(
		ctx,
		(*C.uint8_t)(unsafe.Pointer(&src[0])), C.size_t(len(src)),
		(*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)),
	)
	zstdDCtxPool.Put(ctx)
	return n
}
