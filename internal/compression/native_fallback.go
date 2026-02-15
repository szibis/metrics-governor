//go:build !cgo || !native_compress

package compression

const nativeCompressionAvailable = false

func nativeCompress(_ []byte, _ Config) ([]byte, error) {
	panic("native compression not available: build with CGO_ENABLED=1 -tags native_compress")
}

func nativeDecompress(_ []byte, _ Type) ([]byte, error) {
	panic("native decompression not available: build with CGO_ENABLED=1 -tags native_compress")
}
