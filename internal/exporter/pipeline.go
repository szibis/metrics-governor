package exporter

// PreparedEntry holds a queue entry that has been compressed and is ready for HTTP send.
// This is the data passed from preparers to senders via a bounded channel.
type PreparedEntry struct {
	// CompressedData is the compressed payload ready for HTTP body.
	CompressedData []byte
	// RawData is the original uncompressed proto bytes for re-queue on failure.
	RawData []byte
	// ContentEncoding is the compression encoding ("gzip", "snappy", etc.).
	ContentEncoding string
	// DatapointCount tracks datapoints for metrics (may be 0 for raw-bytes fast path).
	DatapointCount int
	// UncompressedSize is the size of the original uncompressed data.
	UncompressedSize int
}

// PipelineSplitConfig holds configuration for the pipeline split optimization.
type PipelineSplitConfig struct {
	// Enabled activates pipeline split (preparers + senders instead of unified workers).
	Enabled bool
	// PreparerCount is the number of preparer goroutines (CPU-bound, default: NumCPU).
	PreparerCount int
	// SenderCount is the number of sender goroutines (I/O-bound, default: NumCPU*2).
	SenderCount int
	// ChannelSize is the bounded channel buffer size between preparers and senders (default: 256).
	ChannelSize int
}
