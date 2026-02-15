//! C-ABI compression library wrapping flate2 (gzip/zlib/deflate) and zstd.
//!
//! All functions take raw byte pointers and return:
//!   >= 0: number of bytes written to dst
//!   -1:   dst buffer too small (retry with larger buffer)
//!   -2:   compression/decompression error
//!
//! Zstd supports two modes:
//!   1. Single-shot: `native_zstd_compress` / `native_zstd_decompress`
//!      Creates a temporary context per call. Simple but slower.
//!   2. Persistent context: `native_zstd_cctx_new` + `native_zstd_compress_ctx`
//!      Reuses allocated context across calls. Pooled on Go side via sync.Pool.
//!      This matches klauspost/compress's pooled encoder pattern.

use std::io::{Read, Write};

// ──────────────────────────── Gzip ────────────────────────────

#[unsafe(no_mangle)]
pub extern "C" fn native_gzip_compress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
    level: i32,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let compression = flate2::Compression::new(level as u32);
    let mut encoder = flate2::write::GzEncoder::new(Vec::with_capacity(src_len), compression);
    if encoder.write_all(src).is_err() {
        return -2;
    }
    let result = match encoder.finish() {
        Ok(v) => v,
        Err(_) => return -2,
    };
    if result.len() > dst_cap {
        return -1;
    }
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    dst[..result.len()].copy_from_slice(&result);
    result.len() as i64
}

#[unsafe(no_mangle)]
pub extern "C" fn native_gzip_decompress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let mut decoder = flate2::read::GzDecoder::new(src);
    let mut result = Vec::with_capacity(src_len * 4);
    if decoder.read_to_end(&mut result).is_err() {
        return -2;
    }
    if result.len() > dst_cap {
        return -1;
    }
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    dst[..result.len()].copy_from_slice(&result);
    result.len() as i64
}

// ──────────────────────────── Zlib ────────────────────────────

#[unsafe(no_mangle)]
pub extern "C" fn native_zlib_compress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
    level: i32,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let compression = flate2::Compression::new(level as u32);
    let mut encoder = flate2::write::ZlibEncoder::new(Vec::with_capacity(src_len), compression);
    if encoder.write_all(src).is_err() {
        return -2;
    }
    let result = match encoder.finish() {
        Ok(v) => v,
        Err(_) => return -2,
    };
    if result.len() > dst_cap {
        return -1;
    }
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    dst[..result.len()].copy_from_slice(&result);
    result.len() as i64
}

#[unsafe(no_mangle)]
pub extern "C" fn native_zlib_decompress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let mut decoder = flate2::read::ZlibDecoder::new(src);
    let mut result = Vec::with_capacity(src_len * 4);
    if decoder.read_to_end(&mut result).is_err() {
        return -2;
    }
    if result.len() > dst_cap {
        return -1;
    }
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    dst[..result.len()].copy_from_slice(&result);
    result.len() as i64
}

// ──────────────────────────── Deflate (raw) ────────────────────────────

#[unsafe(no_mangle)]
pub extern "C" fn native_deflate_compress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
    level: i32,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let compression = flate2::Compression::new(level as u32);
    let mut encoder = flate2::write::DeflateEncoder::new(Vec::with_capacity(src_len), compression);
    if encoder.write_all(src).is_err() {
        return -2;
    }
    let result = match encoder.finish() {
        Ok(v) => v,
        Err(_) => return -2,
    };
    if result.len() > dst_cap {
        return -1;
    }
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    dst[..result.len()].copy_from_slice(&result);
    result.len() as i64
}

#[unsafe(no_mangle)]
pub extern "C" fn native_deflate_decompress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let mut decoder = flate2::read::DeflateDecoder::new(src);
    let mut result = Vec::with_capacity(src_len * 4);
    if decoder.read_to_end(&mut result).is_err() {
        return -2;
    }
    if result.len() > dst_cap {
        return -1;
    }
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    dst[..result.len()].copy_from_slice(&result);
    result.len() as i64
}

// ──────────────────────────── Zstd (single-shot) ────────────────────────────

/// Single-shot zstd compression. Uses zstd_safe::compress() which writes
/// directly to the output buffer (no intermediate Vec allocation).
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_compress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
    level: i32,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    match zstd_safe::compress(dst, src, level) {
        Ok(n) => n as i64,
        Err(code) => {
            // ZSTD_isError returns non-zero for errors. The error code for
            // "destination buffer too small" is ZSTD_error_dstSize_tooSmall.
            // We check if the compressed bound exceeds dst_cap.
            if zstd_safe::compress_bound(src_len) > dst_cap {
                -1 // buffer too small
            } else {
                let _ = code; // suppress unused warning
                -2 // other compression error
            }
        }
    }
}

/// Single-shot zstd decompression. Uses zstd_safe::decompress() which writes
/// directly to the output buffer (no intermediate Vec allocation).
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_decompress(
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
) -> i64 {
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };
    match zstd_safe::decompress(dst, src) {
        Ok(n) => n as i64,
        Err(_) => {
            // Check if it's likely a buffer-too-small error by looking at
            // the frame content size hint.
            let content_size = zstd_safe::get_frame_content_size(src);
            match content_size {
                Ok(Some(size)) if size as usize > dst_cap => -1,
                _ => {
                    // Could still be buffer too small if content size unknown.
                    // Return -1 to let Go retry with a larger buffer.
                    if dst_cap < src_len * 10 {
                        -1
                    } else {
                        -2
                    }
                }
            }
        }
    }
}

// ──────────────────────────── Zstd (persistent context) ────────────────────────────

/// Create a new persistent zstd compression context.
/// The returned pointer must be freed with `native_zstd_cctx_free`.
/// Pooled on Go side via sync.Pool for amortized context creation.
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_cctx_new() -> *mut std::ffi::c_void {
    let cctx = zstd_safe::CCtx::create();
    Box::into_raw(Box::new(cctx)) as *mut std::ffi::c_void
}

/// Free a persistent zstd compression context.
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_cctx_free(ctx: *mut std::ffi::c_void) {
    if !ctx.is_null() {
        unsafe {
            drop(Box::from_raw(ctx as *mut zstd_safe::CCtx<'static>));
        }
    }
}

/// Compress using a persistent zstd context. The context's internal buffers
/// are reused across calls, eliminating per-call allocation overhead.
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_compress_ctx(
    ctx: *mut std::ffi::c_void,
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
    level: i32,
) -> i64 {
    if ctx.is_null() {
        return -2;
    }
    let cctx = unsafe { &mut *(ctx as *mut zstd_safe::CCtx<'static>) };
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };

    // Set compression level (resets context state for new frame)
    if cctx
        .set_parameter(zstd_safe::CParameter::CompressionLevel(level))
        .is_err()
    {
        return -2;
    }

    match cctx.compress2(dst, src) {
        Ok(n) => n as i64,
        Err(_) => {
            if zstd_safe::compress_bound(src_len) > dst_cap {
                -1
            } else {
                -2
            }
        }
    }
}

/// Create a new persistent zstd decompression context.
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_dctx_new() -> *mut std::ffi::c_void {
    let dctx = zstd_safe::DCtx::create();
    Box::into_raw(Box::new(dctx)) as *mut std::ffi::c_void
}

/// Free a persistent zstd decompression context.
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_dctx_free(ctx: *mut std::ffi::c_void) {
    if !ctx.is_null() {
        unsafe {
            drop(Box::from_raw(ctx as *mut zstd_safe::DCtx<'static>));
        }
    }
}

/// Decompress using a persistent zstd context.
#[unsafe(no_mangle)]
pub extern "C" fn native_zstd_decompress_ctx(
    ctx: *mut std::ffi::c_void,
    src: *const u8,
    src_len: usize,
    dst: *mut u8,
    dst_cap: usize,
) -> i64 {
    if ctx.is_null() {
        return -2;
    }
    let dctx = unsafe { &mut *(ctx as *mut zstd_safe::DCtx<'static>) };
    let src = unsafe { std::slice::from_raw_parts(src, src_len) };
    let dst = unsafe { std::slice::from_raw_parts_mut(dst, dst_cap) };

    match dctx.decompress(dst, src) {
        Ok(n) => n as i64,
        Err(_) => {
            let content_size = zstd_safe::get_frame_content_size(src);
            match content_size {
                Ok(Some(size)) if size as usize > dst_cap => -1,
                _ => {
                    if dst_cap < src_len * 10 {
                        -1
                    } else {
                        -2
                    }
                }
            }
        }
    }
}

// ──────────────────────────── Tests ────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn roundtrip_test(
        compress_fn: unsafe extern "C" fn(*const u8, usize, *mut u8, usize, i32) -> i64,
        decompress_fn: unsafe extern "C" fn(*const u8, usize, *mut u8, usize) -> i64,
        level: i32,
    ) {
        let input = b"Hello, metrics-governor! This is a test of native compression via Rust FFI.";
        let mut compressed = vec![0u8; input.len() * 2 + 128];
        let mut decompressed = vec![0u8; input.len() * 4];

        let comp_len =
            unsafe { compress_fn(input.as_ptr(), input.len(), compressed.as_mut_ptr(), compressed.len(), level) };
        assert!(comp_len > 0, "compression failed: {comp_len}");

        let decomp_len = unsafe {
            decompress_fn(
                compressed.as_ptr(),
                comp_len as usize,
                decompressed.as_mut_ptr(),
                decompressed.len(),
            )
        };
        assert!(decomp_len > 0, "decompression failed: {decomp_len}");
        assert_eq!(&decompressed[..decomp_len as usize], input);
    }

    #[test]
    fn test_gzip_roundtrip() {
        roundtrip_test(native_gzip_compress, native_gzip_decompress, 6);
    }

    #[test]
    fn test_gzip_all_levels() {
        for level in 1..=9 {
            roundtrip_test(native_gzip_compress, native_gzip_decompress, level);
        }
    }

    #[test]
    fn test_zlib_roundtrip() {
        roundtrip_test(native_zlib_compress, native_zlib_decompress, 6);
    }

    #[test]
    fn test_deflate_roundtrip() {
        roundtrip_test(native_deflate_compress, native_deflate_decompress, 6);
    }

    #[test]
    fn test_zstd_roundtrip() {
        roundtrip_test(native_zstd_compress, native_zstd_decompress, 3);
    }

    #[test]
    fn test_zstd_all_levels() {
        for level in [1, 3, 5, 9, 19] {
            roundtrip_test(native_zstd_compress, native_zstd_decompress, level);
        }
    }

    #[test]
    fn test_empty_input() {
        let input: &[u8] = &[];
        let mut compressed = vec![0u8; 256];
        let mut decompressed = vec![0u8; 256];

        let comp_len =
            native_gzip_compress(input.as_ptr(), 0, compressed.as_mut_ptr(), compressed.len(), 6);
        assert!(comp_len > 0, "empty gzip compress failed: {comp_len}");

        let decomp_len = native_gzip_decompress(
            compressed.as_ptr(),
            comp_len as usize,
            decompressed.as_mut_ptr(),
            decompressed.len(),
        );
        assert_eq!(decomp_len, 0, "empty input should decompress to 0 bytes");
    }

    #[test]
    fn test_large_payload() {
        let input: Vec<u8> = (0..1_000_000).map(|i| (i % 256) as u8).collect();
        let mut compressed = vec![0u8; input.len() * 2];
        let mut decompressed = vec![0u8; input.len() * 2];

        let comp_len = native_gzip_compress(
            input.as_ptr(),
            input.len(),
            compressed.as_mut_ptr(),
            compressed.len(),
            6,
        );
        assert!(comp_len > 0, "large payload compress failed");

        let decomp_len = native_gzip_decompress(
            compressed.as_ptr(),
            comp_len as usize,
            decompressed.as_mut_ptr(),
            decompressed.len(),
        );
        assert_eq!(decomp_len as usize, input.len());
        assert_eq!(&decompressed[..decomp_len as usize], &input[..]);
    }

    #[test]
    fn test_buffer_too_small() {
        let input = b"Hello, this is a test payload for buffer size checking.";
        let mut tiny_buf = vec![0u8; 1]; // Too small

        let result =
            native_gzip_compress(input.as_ptr(), input.len(), tiny_buf.as_mut_ptr(), tiny_buf.len(), 6);
        assert_eq!(result, -1, "should return -1 for buffer too small");
    }

    // ──── Persistent context tests ────

    #[test]
    fn test_zstd_ctx_roundtrip() {
        let input = b"Hello, metrics-governor! Testing persistent zstd context compression.";
        let mut compressed = vec![0u8; input.len() * 2 + 128];
        let mut decompressed = vec![0u8; input.len() * 4];

        let cctx = native_zstd_cctx_new();
        assert!(!cctx.is_null());
        let dctx = native_zstd_dctx_new();
        assert!(!dctx.is_null());

        let comp_len =
            native_zstd_compress_ctx(cctx, input.as_ptr(), input.len(), compressed.as_mut_ptr(), compressed.len(), 3);
        assert!(comp_len > 0, "ctx compression failed: {comp_len}");

        let decomp_len =
            native_zstd_decompress_ctx(
                dctx,
                compressed.as_ptr(),
                comp_len as usize,
                decompressed.as_mut_ptr(),
                decompressed.len(),
            );
        assert!(decomp_len > 0, "ctx decompression failed: {decomp_len}");
        assert_eq!(&decompressed[..decomp_len as usize], input);

        native_zstd_cctx_free(cctx);
        native_zstd_dctx_free(dctx);
    }

    #[test]
    fn test_zstd_ctx_reuse() {
        let cctx = native_zstd_cctx_new();
        let dctx = native_zstd_dctx_new();

        // Reuse same context for 100 compress/decompress cycles
        for i in 0..100 {
            let input = format!("Context reuse iteration {i:04} with some padding data to compress.");
            let input = input.as_bytes();
            let mut compressed = vec![0u8; input.len() * 2 + 128];
            let mut decompressed = vec![0u8; input.len() * 4];

            let comp_len =
                native_zstd_compress_ctx(cctx, input.as_ptr(), input.len(), compressed.as_mut_ptr(), compressed.len(), 3);
            assert!(comp_len > 0, "iter {i}: compression failed: {comp_len}");

            let decomp_len =
                native_zstd_decompress_ctx(
                    dctx,
                    compressed.as_ptr(),
                    comp_len as usize,
                    decompressed.as_mut_ptr(),
                    decompressed.len(),
                );
            assert!(decomp_len > 0, "iter {i}: decompression failed: {decomp_len}");
            assert_eq!(&decompressed[..decomp_len as usize], input, "iter {i}: mismatch");
        }

        native_zstd_cctx_free(cctx);
        native_zstd_dctx_free(dctx);
    }

    #[test]
    fn test_zstd_ctx_all_levels() {
        let cctx = native_zstd_cctx_new();
        let dctx = native_zstd_dctx_new();

        let input = b"Testing persistent context with various compression levels for zstd.";

        for level in [1, 3, 5, 9, 19] {
            let mut compressed = vec![0u8; input.len() * 2 + 128];
            let mut decompressed = vec![0u8; input.len() * 4];

            let comp_len =
                native_zstd_compress_ctx(cctx, input.as_ptr(), input.len(), compressed.as_mut_ptr(), compressed.len(), level);
            assert!(comp_len > 0, "level {level}: compression failed: {comp_len}");

            let decomp_len =
                native_zstd_decompress_ctx(
                    dctx,
                    compressed.as_ptr(),
                    comp_len as usize,
                    decompressed.as_mut_ptr(),
                    decompressed.len(),
                );
            assert!(decomp_len > 0, "level {level}: decompression failed: {decomp_len}");
            assert_eq!(&decompressed[..decomp_len as usize], input, "level {level}: mismatch");
        }

        native_zstd_cctx_free(cctx);
        native_zstd_dctx_free(dctx);
    }

    #[test]
    fn test_zstd_ctx_large_payload() {
        let input: Vec<u8> = (0..1_000_000).map(|i| (i % 256) as u8).collect();
        let bound = zstd_safe::compress_bound(input.len());
        let mut compressed = vec![0u8; bound];
        let mut decompressed = vec![0u8; input.len() + 1024];

        let cctx = native_zstd_cctx_new();
        let dctx = native_zstd_dctx_new();

        let comp_len =
            native_zstd_compress_ctx(cctx, input.as_ptr(), input.len(), compressed.as_mut_ptr(), compressed.len(), 3);
        assert!(comp_len > 0, "large ctx compression failed: {comp_len}");

        let decomp_len =
            native_zstd_decompress_ctx(
                dctx,
                compressed.as_ptr(),
                comp_len as usize,
                decompressed.as_mut_ptr(),
                decompressed.len(),
            );
        assert_eq!(decomp_len as usize, input.len());
        assert_eq!(&decompressed[..decomp_len as usize], &input[..]);

        native_zstd_cctx_free(cctx);
        native_zstd_dctx_free(dctx);
    }

    #[test]
    fn test_zstd_null_ctx() {
        let input = b"test";
        let mut out = vec![0u8; 256];

        let result =
            native_zstd_compress_ctx(std::ptr::null_mut(), input.as_ptr(), input.len(), out.as_mut_ptr(), out.len(), 3);
        assert_eq!(result, -2, "null cctx should return -2");

        let result =
            native_zstd_decompress_ctx(std::ptr::null_mut(), input.as_ptr(), input.len(), out.as_mut_ptr(), out.len());
        assert_eq!(result, -2, "null dctx should return -2");
    }
}
