package cardinality

import (
	"fmt"
	"testing"
)

func BenchmarkTrackerComparison(b *testing.B) {
	sizes := []int{1000, 10000, 100000}

	for _, size := range sizes {
		keys := make([][]byte, size)
		for i := 0; i < size; i++ {
			keys[i] = []byte(fmt.Sprintf("metric{service=\"svc%d\",env=\"prod\",pod=\"pod-%d\"}", i%100, i))
		}

		b.Run(fmt.Sprintf("Bloom-%d", size), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				tracker := NewBloomTracker(Config{
					ExpectedItems:     uint(size),
					FalsePositiveRate: 0.01,
				})
				for i := 0; i < size; i++ {
					tracker.Add(keys[i])
				}
			}
		})

		b.Run(fmt.Sprintf("HLL-%d", size), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				tracker := NewHLLTracker()
				for i := 0; i < size; i++ {
					tracker.Add(keys[i])
				}
			}
		})

		b.Run(fmt.Sprintf("Hybrid-%d", size), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				tracker := NewHybridTracker(Config{
					ExpectedItems:     uint(size),
					FalsePositiveRate: 0.01,
					HLLThreshold:      10000,
				}, 10000)
				for i := 0; i < size; i++ {
					tracker.Add(keys[i])
				}
			}
		})
	}
}

func BenchmarkMemoryUsage(b *testing.B) {
	sizes := []int{1000, 10000, 100000}

	for _, size := range sizes {
		keys := make([][]byte, size)
		for i := 0; i < size; i++ {
			keys[i] = []byte(fmt.Sprintf("metric{service=\"svc%d\",env=\"prod\",pod=\"pod-%d\"}", i%100, i))
		}

		b.Run(fmt.Sprintf("Bloom-%d", size), func(b *testing.B) {
			tracker := NewBloomTracker(Config{
				ExpectedItems:     uint(size),
				FalsePositiveRate: 0.01,
			})
			for i := 0; i < size; i++ {
				tracker.Add(keys[i])
			}
			b.ReportMetric(float64(tracker.MemoryUsage()), "bytes")
			b.ReportMetric(float64(tracker.Count()), "items")
		})

		b.Run(fmt.Sprintf("HLL-%d", size), func(b *testing.B) {
			tracker := NewHLLTracker()
			for i := 0; i < size; i++ {
				tracker.Add(keys[i])
			}
			b.ReportMetric(float64(tracker.MemoryUsage()), "bytes")
			b.ReportMetric(float64(tracker.Count()), "items")
		})

		b.Run(fmt.Sprintf("Hybrid-%d", size), func(b *testing.B) {
			tracker := NewHybridTracker(Config{
				ExpectedItems:     uint(size),
				FalsePositiveRate: 0.01,
				HLLThreshold:      10000,
			}, 10000)
			for i := 0; i < size; i++ {
				tracker.Add(keys[i])
			}
			b.ReportMetric(float64(tracker.MemoryUsage()), "bytes")
			b.ReportMetric(float64(tracker.Count()), "items")
			b.ReportMetric(float64(tracker.Mode()), "mode") // 0=bloom, 1=hll
		})
	}
}

func BenchmarkHybridTracker_ShouldSample(b *testing.B) {
	tracker := NewHybridTracker(Config{
		ExpectedItems:     100000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      10000,
	}, 10000)

	const numKeys = 10000
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("series-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.ShouldSample(keys[i%numKeys], 0.5)
	}
}

func BenchmarkConcurrentAdd(b *testing.B) {
	sizes := []int{1000, 10000, 100000}

	for _, size := range sizes {
		keys := make([][]byte, size)
		for i := 0; i < size; i++ {
			keys[i] = []byte(fmt.Sprintf("metric{service=\"svc%d\",pod=\"pod-%d\"}", i%100, i))
		}

		b.Run(fmt.Sprintf("Bloom-%d", size), func(b *testing.B) {
			tracker := NewBloomTracker(Config{
				ExpectedItems:     uint(size),
				FalsePositiveRate: 0.01,
			})
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					tracker.Add(keys[i%size])
					i++
				}
			})
		})

		b.Run(fmt.Sprintf("HLL-%d", size), func(b *testing.B) {
			tracker := NewHLLTracker()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					tracker.Add(keys[i%size])
					i++
				}
			})
		})

		b.Run(fmt.Sprintf("Hybrid-%d", size), func(b *testing.B) {
			tracker := NewHybridTracker(Config{
				ExpectedItems:     uint(size),
				FalsePositiveRate: 0.01,
				HLLThreshold:      10000,
			}, 10000)
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					tracker.Add(keys[i%size])
					i++
				}
			})
		})
	}
}
