package pipeline

import (
	"sync"
	"testing"
	"time"
)

func BenchmarkRecord(b *testing.B) {
	d := 100 * time.Microsecond
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Record("stats", d)
	}
}

func BenchmarkRecordBytes(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RecordBytes("serialize", 4096)
	}
}

func BenchmarkRecordParallel(b *testing.B) {
	d := 100 * time.Microsecond
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Record("stats", d)
		}
	})
}

// BenchmarkRecordContention simulates realistic pipeline contention:
// multiple goroutines recording to different components simultaneously,
// which is the pattern that caused WithLabelValues() mutex contention.
func BenchmarkRecordContention(b *testing.B) {
	components := []string{"stats", "sampling", "tenant", "limits", "serialize", "compress", "export_http"}
	d := 100 * time.Microsecond
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			Record(components[i%len(components)], d)
			i++
		}
	})
}

// BenchmarkFullIngestPath simulates the timing overhead of a single ingest call
// through the buffer (stats + sampling + tenant + limits = 4 Record calls + 4 time.Now).
func BenchmarkFullIngestPath(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		Record("stats", Since(start))

		start = time.Now()
		Record("sampling", Since(start))

		start = time.Now()
		Record("tenant", Since(start))

		start = time.Now()
		Record("limits", Since(start))
	}
}

func BenchmarkFullIngestPathParallel(b *testing.B) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			start := time.Now()
			Record("stats", Since(start))

			start = time.Now()
			Record("sampling", Since(start))

			start = time.Now()
			Record("tenant", Since(start))

			start = time.Now()
			Record("limits", Since(start))
		}
	})
}

// BenchmarkWithLabelValuesBaseline measures the OLD approach (WithLabelValues on every call)
// vs the new pre-resolved approach, to quantify the improvement.
func BenchmarkWithLabelValuesBaseline(b *testing.B) {
	d := 100 * time.Microsecond
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			componentSeconds.WithLabelValues("stats").Add(d.Seconds())
		}
	})
}

func BenchmarkPreResolvedCounter(b *testing.B) {
	d := 100 * time.Microsecond
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Record("stats", d)
		}
	})
}

// BenchmarkMixedContention simulates full pipeline: multiple goroutines doing
// both Record and RecordBytes across different components.
func BenchmarkMixedContention(b *testing.B) {
	components := []string{"stats", "sampling", "tenant", "limits", "serialize", "compress", "export_http"}
	d := 100 * time.Microsecond
	var wg sync.WaitGroup
	b.ResetTimer()

	// Use RunParallel for proper benchmark scaling
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c := components[i%len(components)]
			Record(c, d)
			RecordBytes(c, 4096)
			i++
		}
	})
	wg.Wait()
}
