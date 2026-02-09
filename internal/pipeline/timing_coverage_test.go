package pipeline

import (
	"testing"
	"time"
)

// --- RecordBytes tests ---

func TestRecordBytes_KnownComponent(t *testing.T) {
	// RecordBytes with a known component and positive value should not panic.
	RecordBytes("serialize", 4096)
}

func TestRecordBytes_Zero(t *testing.T) {
	// n=0 should be a no-op (guarded by n > 0 check), must not panic.
	RecordBytes("serialize", 0)
}

func TestRecordBytes_Negative(t *testing.T) {
	// Negative n should be a no-op (guarded by n > 0 check), must not panic.
	RecordBytes("serialize", -1)
}

func TestRecordBytes_UnknownComponent(t *testing.T) {
	// Unknown component should be silently ignored, must not panic.
	RecordBytes("nonexistent_component", 100)
}

func TestRecordBytes_MultipleComponents(t *testing.T) {
	// Record bytes for several known components to exercise the lookup path.
	for _, c := range []string{"stats", "processing", "compress", "export_http"} {
		RecordBytes(c, 1024)
	}
}

// --- Track tests ---

func TestTrack_KnownComponent(t *testing.T) {
	// Track returns a function; calling it records elapsed time. Must not panic.
	stop := Track("stats")
	time.Sleep(time.Millisecond)
	stop()
}

func TestTrack_DeferPattern(t *testing.T) {
	// Verify the idiomatic defer pattern: defer Track("component")()
	func() {
		defer Track("processing")()
		time.Sleep(time.Millisecond)
	}()
}

func TestTrack_UnknownComponent(t *testing.T) {
	// Unknown component returns a stop func with nil counter; must not panic.
	stop := Track("nonexistent_component")
	time.Sleep(time.Millisecond)
	stop()
}

func TestTrack_ImmediateStop(t *testing.T) {
	// Calling stop immediately (near-zero duration) must not panic.
	stop := Track("limits")
	stop()
}

func TestTrack_MultipleComponents(t *testing.T) {
	// Track multiple known components concurrently.
	stops := make([]func(), 0, 4)
	for _, c := range []string{"relabel", "batch_split", "queue_push", "queue_pop"} {
		stops = append(stops, Track(c))
	}
	time.Sleep(time.Millisecond)
	for _, stop := range stops {
		stop()
	}
}
