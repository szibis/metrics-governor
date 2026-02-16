package config

import (
	"math"
	"testing"
)

// deriveExpected mirrors the production DeriveMemorySizing calculation.
// Using a helper avoids Go's constant-folding restriction on int64(float64_const).
func deriveExpected(limit int64, pct float64) int64 {
	return int64(float64(limit) * pct)
}

// ---------------------------------------------------------------------------
// DeriveMemorySizing — the function that prevents the math.MaxInt64 bug
// where GOMEMLIMIT not being set caused 15% of MaxInt64 ≈ 1.38 exabytes,
// effectively making the buffer unbounded and causing heap explosion.
// ---------------------------------------------------------------------------

func TestDeriveMemorySizing_MaxInt64_TreatedAsNoLimit(t *testing.T) {
	// This is THE critical test: debug.SetMemoryLimit(-1) returns math.MaxInt64
	// when GOMEMLIMIT was never set. Without the guard, 15% of MaxInt64 is
	// 1.38 exabytes — making the buffer effectively unbounded.
	s := DeriveMemorySizing(math.MaxInt64, 0.15, 0.15)

	if s.MemoryLimit != 0 {
		t.Errorf("expected MemoryLimit=0 for MaxInt64 input, got %d", s.MemoryLimit)
	}
	if s.BufferMaxBytes != 0 {
		t.Errorf("expected BufferMaxBytes=0 for MaxInt64 input, got %d (would be %.0f exabytes!)",
			s.BufferMaxBytes, float64(s.BufferMaxBytes)/1e18)
	}
	if s.QueueMaxBytes != 0 {
		t.Errorf("expected QueueMaxBytes=0 for MaxInt64 input, got %d", s.QueueMaxBytes)
	}
}

func TestDeriveMemorySizing_NegativeLimit_TreatedAsNoLimit(t *testing.T) {
	s := DeriveMemorySizing(-1, 0.15, 0.15)

	if s.MemoryLimit != 0 {
		t.Errorf("expected MemoryLimit=0 for negative input, got %d", s.MemoryLimit)
	}
	if s.BufferMaxBytes != 0 {
		t.Errorf("expected BufferMaxBytes=0 for negative input, got %d", s.BufferMaxBytes)
	}
}

func TestDeriveMemorySizing_ZeroLimit_TreatedAsNoLimit(t *testing.T) {
	s := DeriveMemorySizing(0, 0.15, 0.15)

	if s.MemoryLimit != 0 {
		t.Errorf("expected MemoryLimit=0 for zero input, got %d", s.MemoryLimit)
	}
	if s.BufferMaxBytes != 0 {
		t.Errorf("expected BufferMaxBytes=0 for zero input, got %d", s.BufferMaxBytes)
	}
}

func TestDeriveMemorySizing_1GB_DefaultPercents(t *testing.T) {
	const oneGB int64 = 1 << 30 // 1,073,741,824 bytes
	s := DeriveMemorySizing(oneGB, 0.15, 0.15)

	if s.MemoryLimit != oneGB {
		t.Errorf("expected MemoryLimit=%d, got %d", oneGB, s.MemoryLimit)
	}

	expectedBuffer := deriveExpected(oneGB, 0.15) // ~161 MB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("expected BufferMaxBytes=%d (~161MB), got %d", expectedBuffer, s.BufferMaxBytes)
	}

	expectedQueue := deriveExpected(oneGB, 0.15) // ~161 MB
	if s.QueueMaxBytes != expectedQueue {
		t.Errorf("expected QueueMaxBytes=%d (~161MB), got %d", expectedQueue, s.QueueMaxBytes)
	}
}

func TestDeriveMemorySizing_512MB_CustomPercents(t *testing.T) {
	const halfGB int64 = 512 * 1024 * 1024 // 536,870,912 bytes
	s := DeriveMemorySizing(halfGB, 0.20, 0.10)

	expectedBuffer := deriveExpected(halfGB, 0.20) // ~107 MB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("expected BufferMaxBytes=%d, got %d", expectedBuffer, s.BufferMaxBytes)
	}

	expectedQueue := deriveExpected(halfGB, 0.10) // ~53 MB
	if s.QueueMaxBytes != expectedQueue {
		t.Errorf("expected QueueMaxBytes=%d, got %d", expectedQueue, s.QueueMaxBytes)
	}
}

func TestDeriveMemorySizing_ZeroPercent_BufferOnly(t *testing.T) {
	const oneGB int64 = 1 << 30
	s := DeriveMemorySizing(oneGB, 0.0, 0.15)

	if s.BufferMaxBytes != 0 {
		t.Errorf("expected BufferMaxBytes=0 with 0%% buffer, got %d", s.BufferMaxBytes)
	}
	if s.QueueMaxBytes == 0 {
		t.Errorf("expected non-zero QueueMaxBytes with 15%% queue, got 0")
	}
}

func TestDeriveMemorySizing_ZeroPercent_QueueOnly(t *testing.T) {
	const oneGB int64 = 1 << 30
	s := DeriveMemorySizing(oneGB, 0.15, 0.0)

	if s.BufferMaxBytes == 0 {
		t.Errorf("expected non-zero BufferMaxBytes with 15%% buffer, got 0")
	}
	if s.QueueMaxBytes != 0 {
		t.Errorf("expected QueueMaxBytes=0 with 0%% queue, got %d", s.QueueMaxBytes)
	}
}

func TestDeriveMemorySizing_BothZeroPercent(t *testing.T) {
	const oneGB int64 = 1 << 30
	s := DeriveMemorySizing(oneGB, 0.0, 0.0)

	if s.MemoryLimit != oneGB {
		t.Errorf("expected MemoryLimit=%d even with 0%% percents, got %d", oneGB, s.MemoryLimit)
	}
	if s.BufferMaxBytes != 0 {
		t.Errorf("expected BufferMaxBytes=0, got %d", s.BufferMaxBytes)
	}
	if s.QueueMaxBytes != 0 {
		t.Errorf("expected QueueMaxBytes=0, got %d", s.QueueMaxBytes)
	}
}

func TestDeriveMemorySizing_SmallContainer_128MB(t *testing.T) {
	// Typical sidecar container: 128MB limit
	const limit int64 = 128 * 1024 * 1024
	s := DeriveMemorySizing(limit, 0.15, 0.15)

	expectedBuffer := deriveExpected(limit, 0.15) // ~19.2 MB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("expected BufferMaxBytes=%d (~19MB), got %d", expectedBuffer, s.BufferMaxBytes)
	}

	// Sanity: buffer should be reasonable for a 128MB container
	if s.BufferMaxBytes > 30*1024*1024 {
		t.Errorf("buffer too large for 128MB container: %d bytes", s.BufferMaxBytes)
	}
}

func TestDeriveMemorySizing_ResultNeverExceedsLimit(t *testing.T) {
	// Property test: buffer + queue should never exceed the limit
	for _, limit := range []int64{
		64 * 1024 * 1024,  // 64MB
		256 * 1024 * 1024, // 256MB
		1 << 30,           // 1GB
		4 * (1 << 30),     // 4GB
		16 * (1 << 30),    // 16GB
	} {
		s := DeriveMemorySizing(limit, 0.15, 0.15)
		total := s.BufferMaxBytes + s.QueueMaxBytes
		if total > limit {
			t.Errorf("limit=%d: buffer(%d) + queue(%d) = %d exceeds limit",
				limit, s.BufferMaxBytes, s.QueueMaxBytes, total)
		}
	}
}

// TestDeriveMemorySizing_LargeValues_NoOverflow ensures no integer overflow
// with large but valid GOMEMLIMIT values (e.g., 64GB container).
func TestDeriveMemorySizing_LargeValues_NoOverflow(t *testing.T) {
	const sixtyFourGB int64 = 64 * (1 << 30) // 68,719,476,736
	s := DeriveMemorySizing(sixtyFourGB, 0.15, 0.15)

	if s.BufferMaxBytes <= 0 {
		t.Errorf("expected positive BufferMaxBytes for 64GB limit, got %d", s.BufferMaxBytes)
	}
	if s.QueueMaxBytes <= 0 {
		t.Errorf("expected positive QueueMaxBytes for 64GB limit, got %d", s.QueueMaxBytes)
	}

	expectedBuffer := deriveExpected(sixtyFourGB, 0.15) // ~9.6 GB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("expected BufferMaxBytes=%d (~9.6GB), got %d", expectedBuffer, s.BufferMaxBytes)
	}
}

// TestDeriveMemorySizing_QueueBytesCorrect verifies QueueMaxBytes is computed
// as limit × queuePercent — the value that must be wired into cfg.QueueMaxBytes.
func TestDeriveMemorySizing_QueueBytesCorrect(t *testing.T) {
	const limit int64 = 2700 * 1024 * 1024 // 2.7GB — production container size
	const queuePct = 0.15

	s := DeriveMemorySizing(limit, 0.10, queuePct)

	expected := deriveExpected(limit, queuePct) // ~405 MB
	if s.QueueMaxBytes != expected {
		t.Errorf("QueueMaxBytes: got %d, want %d (%.0fMB)", s.QueueMaxBytes, expected, float64(expected)/1e6)
	}
	if s.QueueMaxBytes <= 0 {
		t.Error("QueueMaxBytes must be positive for a valid limit + percent")
	}
}

// --- Memory optimization: tests for reduced balanced profile percents ---

func TestDeriveMemorySizing_1GB_BalancedProfile(t *testing.T) {
	// Balanced profile uses 0.07 buffer + 0.05 queue (reduced from 0.10 + 0.10)
	// GOMEMLIMIT = 1GB × 0.80 = ~850MB
	const gomemlimit int64 = 850 * 1024 * 1024 // 891,289,600 bytes
	s := DeriveMemorySizing(gomemlimit, 0.07, 0.05)

	expectedBuffer := deriveExpected(gomemlimit, 0.07) // ~60 MB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("BufferMaxBytes = %d, want %d (~60MB)", s.BufferMaxBytes, expectedBuffer)
	}

	expectedQueue := deriveExpected(gomemlimit, 0.05) // ~42 MB
	if s.QueueMaxBytes != expectedQueue {
		t.Errorf("QueueMaxBytes = %d, want %d (~42MB)", s.QueueMaxBytes, expectedQueue)
	}

	// Buffer + queue should use only 12% of GOMEMLIMIT
	total := s.BufferMaxBytes + s.QueueMaxBytes
	pct := float64(total) / float64(gomemlimit)
	if pct > 0.13 {
		t.Errorf("buffer+queue = %.1f%% of GOMEMLIMIT, want < 13%%", pct*100)
	}
}

func TestDeriveMemorySizing_512MB_BalancedProfile(t *testing.T) {
	// Small container: 512MB × 0.80 = ~410MB GOMEMLIMIT
	const gomemlimit int64 = 410 * 1024 * 1024
	s := DeriveMemorySizing(gomemlimit, 0.07, 0.05)

	expectedBuffer := deriveExpected(gomemlimit, 0.07) // ~29 MB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("BufferMaxBytes = %d, want %d (~29MB)", s.BufferMaxBytes, expectedBuffer)
	}

	expectedQueue := deriveExpected(gomemlimit, 0.05) // ~20 MB
	if s.QueueMaxBytes != expectedQueue {
		t.Errorf("QueueMaxBytes = %d, want %d (~20MB)", s.QueueMaxBytes, expectedQueue)
	}
}

func TestDeriveMemorySizing_2GB_BalancedProfile(t *testing.T) {
	// Large container: 2GB × 0.80 = ~1700MB GOMEMLIMIT
	const gomemlimit int64 = 1700 * 1024 * 1024
	s := DeriveMemorySizing(gomemlimit, 0.07, 0.05)

	expectedBuffer := deriveExpected(gomemlimit, 0.07) // ~119 MB
	if s.BufferMaxBytes != expectedBuffer {
		t.Errorf("BufferMaxBytes = %d, want %d (~119MB)", s.BufferMaxBytes, expectedBuffer)
	}

	expectedQueue := deriveExpected(gomemlimit, 0.05) // ~85 MB
	if s.QueueMaxBytes != expectedQueue {
		t.Errorf("QueueMaxBytes = %d, want %d (~85MB)", s.QueueMaxBytes, expectedQueue)
	}
}

func TestDeriveMemorySizing_BufferPlusQueue_NeverExceeds15Percent(t *testing.T) {
	// Property test: with balanced profile's 0.07 + 0.05 = 0.12, always < 0.15
	for _, limit := range []int64{
		256 * 1024 * 1024,  // 256MB
		512 * 1024 * 1024,  // 512MB
		850 * 1024 * 1024,  // ~1GB container
		1700 * 1024 * 1024, // ~2GB container
		4 * (1 << 30),      // 4GB
	} {
		s := DeriveMemorySizing(limit, 0.07, 0.05)
		total := s.BufferMaxBytes + s.QueueMaxBytes
		pct := float64(total) / float64(limit)
		if pct > 0.15 {
			t.Errorf("limit=%d: buffer(%d) + queue(%d) = %.1f%%, want < 15%%",
				limit, s.BufferMaxBytes, s.QueueMaxBytes, pct*100)
		}
	}
}

// TestDeriveMemorySizing_QueueAndBufferIndependent verifies that buffer and
// queue percentages produce independent values — changing one doesn't affect the other.
func TestDeriveMemorySizing_QueueAndBufferIndependent(t *testing.T) {
	const limit int64 = 4 * (1 << 30) // 4GB

	s1 := DeriveMemorySizing(limit, 0.10, 0.20)
	s2 := DeriveMemorySizing(limit, 0.25, 0.05)

	// s1: buffer=10%, queue=20%
	// s2: buffer=25%, queue=5%
	// Queue values must differ
	if s1.QueueMaxBytes == s2.QueueMaxBytes {
		t.Errorf("queue bytes should differ: s1=%d, s2=%d", s1.QueueMaxBytes, s2.QueueMaxBytes)
	}
	// Buffer values must differ
	if s1.BufferMaxBytes == s2.BufferMaxBytes {
		t.Errorf("buffer bytes should differ: s1=%d, s2=%d", s1.BufferMaxBytes, s2.BufferMaxBytes)
	}
	// Cross-check: changing buffer% shouldn't affect queue
	s3 := DeriveMemorySizing(limit, 0.30, 0.20)
	if s3.QueueMaxBytes != s1.QueueMaxBytes {
		t.Errorf("changing buffer%% should not affect queue: s1.queue=%d, s3.queue=%d",
			s1.QueueMaxBytes, s3.QueueMaxBytes)
	}
}
