package prw

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/sharding"
)

// ---------------------------------------------------------------------------
// limits.go: Stop (0% coverage)
// ---------------------------------------------------------------------------

func TestLimitsEnforcer_Stop_Coverage(t *testing.T) {
	// Test Stop on a freshly created enforcer with rules
	enforcer := NewLimitsEnforcer(LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "test_rule",
				MetricPattern:          "^test_.*",
				MaxDatapointsPerSecond: 100,
				MaxCardinality:         50,
			},
		},
	}, false)

	// Process some data first so internal state is populated
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "id", Value: "a"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	enforcer.Process(req)

	// Stop should not panic even with populated state
	enforcer.Stop()

	// Calling Stop multiple times should be safe
	enforcer.Stop()
	enforcer.Stop()
}

// ---------------------------------------------------------------------------
// shardkey.go: ShardKeyInternStats (0% coverage)
// ---------------------------------------------------------------------------

func TestShardKeyInternStats_Coverage(t *testing.T) {
	// Build some keys to populate intern pools
	builder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})

	ts := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "shardkey_test_metric"},
			{Name: "service", Value: "api"},
			{Name: "env", Value: "prod"},
		},
	}

	// Build key multiple times to generate hits
	_ = builder.BuildKey(ts)
	_ = builder.BuildKey(ts)
	_ = builder.BuildKey(ts)

	metricHits, metricMisses, labelHits, labelMisses := ShardKeyInternStats()

	// We expect some stats to be populated (exact values depend on test order)
	t.Logf("ShardKeyInternStats: metricHits=%d, metricMisses=%d, labelHits=%d, labelMisses=%d",
		metricHits, metricMisses, labelHits, labelMisses)

	// After building the same key 3 times, we should have at least 2 metric hits
	// (first is a miss, subsequent are hits)
	if metricHits+metricMisses == 0 {
		t.Error("expected some metric intern stats")
	}
}

// ---------------------------------------------------------------------------
// proto.go: skipField (14.3% coverage) - Test with all wire types
// ---------------------------------------------------------------------------

func TestSkipField_AllWireTypes(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wireType int
		wantN    int
		wantErr  bool
	}{
		{
			name:     "varint - single byte",
			data:     []byte{0x05},
			wireType: wireVarint,
			wantN:    1,
		},
		{
			name:     "varint - multi byte",
			data:     []byte{0x80, 0x01},
			wireType: wireVarint,
			wantN:    2,
		},
		{
			name:     "varint - empty data",
			data:     []byte{},
			wireType: wireVarint,
			wantN:    -1, // consumeVarint returns -1
		},
		{
			name:     "fixed64 - valid",
			data:     []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			wireType: wireFixed64,
			wantN:    8,
		},
		{
			name:     "fixed64 - insufficient data",
			data:     []byte{0x01, 0x02, 0x03},
			wireType: wireFixed64,
			wantErr:  true,
		},
		{
			name:     "length delimited - valid",
			data:     []byte{0x03, 0x41, 0x42, 0x43}, // length=3, data="ABC"
			wireType: wireLengthDelim,
			wantN:    4,
		},
		{
			name:     "length delimited - empty",
			data:     []byte{0x00}, // length=0
			wireType: wireLengthDelim,
			wantN:    1,
		},
		{
			name:     "length delimited - invalid length varint",
			data:     []byte{},
			wireType: wireLengthDelim,
			wantErr:  true,
		},
		{
			name:     "fixed32 - valid",
			data:     []byte{0x01, 0x02, 0x03, 0x04},
			wireType: wireFixed32,
			wantN:    4,
		},
		{
			name:     "fixed32 - insufficient data",
			data:     []byte{0x01, 0x02},
			wireType: wireFixed32,
			wantErr:  true,
		},
		{
			name:     "unknown wire type",
			data:     []byte{0x01},
			wireType: 3, // wire type 3 is deprecated/unknown
			wantErr:  true,
		},
		{
			name:     "unknown wire type 4",
			data:     []byte{0x01},
			wireType: 4,
			wantErr:  true,
		},
		{
			name:     "unknown wire type 6",
			data:     []byte{0x01},
			wireType: 6,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := skipField(tt.data, tt.wireType)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				if tt.wantN == -1 {
					// consumeVarint returning -1 means skipField returns (n, nil) where n comes from consumeVarint
					// Actually skipField returns the result of consumeVarint directly for wireVarint
					return
				}
				t.Errorf("unexpected error: %v", err)
				return
			}
			if n != tt.wantN {
				t.Errorf("skipField() n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// proto.go: consumeTag (75% coverage) - Test with truncated/invalid data
// ---------------------------------------------------------------------------

func TestConsumeTag_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantFN   int
		wantWT   int
		wantN    int
		wantFail bool
	}{
		{
			name:     "empty data",
			data:     []byte{},
			wantFail: true,
		},
		{
			name:   "field 1, varint",
			data:   []byte{0x08}, // field=1, wire=0
			wantFN: 1,
			wantWT: 0,
			wantN:  1,
		},
		{
			name:   "field 1, length delimited",
			data:   []byte{0x0a}, // field=1, wire=2
			wantFN: 1,
			wantWT: 2,
			wantN:  1,
		},
		{
			name:   "field 1, fixed64",
			data:   []byte{0x09}, // field=1, wire=1
			wantFN: 1,
			wantWT: 1,
			wantN:  1,
		},
		{
			name:   "large field number",
			data:   []byte{0xf8, 0x01}, // field=31, wire=0
			wantFN: 31,
			wantWT: 0,
			wantN:  2,
		},
		{
			name:     "all continuation bits set (invalid)",
			data:     []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
			wantFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fieldNum, wireType, n := consumeTag(tt.data)
			if tt.wantFail {
				if n >= 0 {
					t.Errorf("expected failure (n < 0), got n=%d", n)
				}
				return
			}
			if n < 0 {
				t.Fatalf("consumeTag failed, n=%d", n)
			}
			if fieldNum != tt.wantFN {
				t.Errorf("fieldNum = %d, want %d", fieldNum, tt.wantFN)
			}
			if wireType != tt.wantWT {
				t.Errorf("wireType = %d, want %d", wireType, tt.wantWT)
			}
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// proto.go: Label.unmarshal edge cases (64.5% coverage)
// ---------------------------------------------------------------------------

func TestLabel_Unmarshal_EdgeCases(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		var l Label
		err := l.unmarshal([]byte{})
		if err != nil {
			t.Errorf("expected nil error for empty data, got %v", err)
		}
		if l.Name != "" || l.Value != "" {
			t.Errorf("expected empty label, got %+v", l)
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		// All continuation bits set
		data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
		var l Label
		err := l.unmarshal(data)
		if err == nil {
			t.Error("expected error for invalid tag")
		}
	})

	t.Run("wrong wire type for name", func(t *testing.T) {
		// Field 1 (name) with wire type 0 (varint) instead of 2 (length delimited)
		data := []byte{0x08, 0x05} // field=1, wire=0, value=5
		var l Label
		err := l.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type")
		}
	})

	t.Run("wrong wire type for value", func(t *testing.T) {
		// Field 2 (value) with wire type 0 (varint) instead of 2 (length delimited)
		data := []byte{0x10, 0x05} // field=2, wire=0, value=5
		var l Label
		err := l.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type")
		}
	})

	t.Run("truncated name length", func(t *testing.T) {
		// Field 1, wire type 2, but truncated length
		data := []byte{0x0a, 0x80} // field=1, wire=2, length varint incomplete
		var l Label
		err := l.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated name")
		}
	})

	t.Run("truncated value length", func(t *testing.T) {
		// Field 2, wire type 2, but truncated length
		data := []byte{0x12, 0x80} // field=2, wire=2, length varint incomplete
		var l Label
		err := l.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated value")
		}
	})

	t.Run("unknown field skipped", func(t *testing.T) {
		// Build a label with known fields + an unknown field 99 (varint)
		l := Label{Name: "foo", Value: "bar"}
		data := l.marshal()
		// Append unknown field 99, wire type 0 (varint), value 42
		unknownTag := byte(15<<3 | 0) // field=15, wire=0
		data = append(data, unknownTag, 42)

		var result Label
		err := result.unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error with unknown field, got %v", err)
		}
		if result.Name != "foo" || result.Value != "bar" {
			t.Errorf("expected foo/bar, got %+v", result)
		}
	})

	t.Run("interning disabled", func(t *testing.T) {
		oldEnabled := InternEnabled
		InternEnabled = false
		defer func() { InternEnabled = oldEnabled }()

		l := Label{Name: "__name__", Value: "test_metric"}
		data := l.marshal()

		var result Label
		err := result.unmarshal(data)
		if err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if result.Name != "__name__" || result.Value != "test_metric" {
			t.Errorf("expected __name__/test_metric, got %+v", result)
		}
	})

	t.Run("long value not interned", func(t *testing.T) {
		// Value longer than InternMaxValueLength should not be interned
		longValue := ""
		for i := 0; i < InternMaxValueLength+10; i++ {
			longValue += "x"
		}

		l := Label{Name: "key", Value: longValue}
		data := l.marshal()

		var result Label
		err := result.unmarshal(data)
		if err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if result.Value != longValue {
			t.Errorf("expected long value, got len=%d", len(result.Value))
		}
	})
}

// ---------------------------------------------------------------------------
// proto.go: Sample.unmarshal edge cases (65.4% coverage)
// ---------------------------------------------------------------------------

func TestSample_Unmarshal_EdgeCases(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		var s Sample
		err := s.unmarshal([]byte{})
		if err != nil {
			t.Errorf("expected nil error for empty data, got %v", err)
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
		var s Sample
		err := s.unmarshal(data)
		if err == nil {
			t.Error("expected error for invalid tag")
		}
	})

	t.Run("wrong wire type for value", func(t *testing.T) {
		// Field 1 (value) with wire type 0 (varint) instead of 1 (fixed64)
		data := []byte{0x08, 0x05} // field=1, wire=0
		var s Sample
		err := s.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for value")
		}
	})

	t.Run("wrong wire type for timestamp", func(t *testing.T) {
		// Field 2 (timestamp) with wire type 1 (fixed64) instead of 0 (varint)
		data := []byte{0x11, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08} // field=2, wire=1
		var s Sample
		err := s.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for timestamp")
		}
	})

	t.Run("insufficient data for fixed64 value", func(t *testing.T) {
		// Field 1 (value), wire type 1 (fixed64), but only 3 bytes
		data := []byte{0x09, 0x01, 0x02, 0x03} // field=1, wire=1, only 3 data bytes
		var s Sample
		err := s.unmarshal(data)
		if err == nil {
			t.Error("expected error for insufficient data")
		}
	})

	t.Run("truncated timestamp varint", func(t *testing.T) {
		// Valid value field, then truncated timestamp varint
		s := Sample{Value: 1.0, Timestamp: 0}
		data := s.marshal()
		// Replace timestamp portion with truncated varint
		// Find where timestamp field starts and truncate
		data = data[:len(data)-1] // remove last byte to truncate varint
		data[len(data)-1] |= 0x80 // set continuation bit

		var result Sample
		err := result.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated timestamp")
		}
	})

	t.Run("unknown field skipped", func(t *testing.T) {
		s := Sample{Value: 42.0, Timestamp: 1000}
		data := s.marshal()
		// Append unknown field 99 with wire type 0 (varint)
		data = append(data, byte(15<<3|0), 0x01)

		var result Sample
		err := result.unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error with unknown field, got %v", err)
		}
		if result.Value != 42.0 {
			t.Errorf("expected value 42.0, got %f", result.Value)
		}
	})

	t.Run("NaN value roundtrip", func(t *testing.T) {
		s := Sample{Value: math.NaN(), Timestamp: 999}
		data := s.marshal()

		var result Sample
		err := result.unmarshal(data)
		if err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if !math.IsNaN(result.Value) {
			t.Errorf("expected NaN, got %f", result.Value)
		}
	})
}

// ---------------------------------------------------------------------------
// proto.go: BucketSpan.unmarshal edge cases (66.7% coverage)
// ---------------------------------------------------------------------------

func TestBucketSpan_Unmarshal_EdgeCases(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		var span BucketSpan
		err := span.unmarshal([]byte{})
		if err != nil {
			t.Errorf("expected nil error for empty data, got %v", err)
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
		var span BucketSpan
		err := span.unmarshal(data)
		if err == nil {
			t.Error("expected error for invalid tag")
		}
	})

	t.Run("wrong wire type for offset", func(t *testing.T) {
		// Field 1 (offset) with wire type 2 (length delimited) instead of 0 (varint)
		data := []byte{0x0a, 0x01, 0x05} // field=1, wire=2
		var span BucketSpan
		err := span.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for offset")
		}
	})

	t.Run("wrong wire type for length", func(t *testing.T) {
		// Field 2 (length) with wire type 2 (length delimited) instead of 0 (varint)
		data := []byte{0x12, 0x01, 0x05} // field=2, wire=2
		var span BucketSpan
		err := span.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for length")
		}
	})

	t.Run("truncated offset varint", func(t *testing.T) {
		// Field 1, wire type 0, but truncated varint
		data := []byte{0x08, 0x80} // field=1, wire=0, truncated varint
		var span BucketSpan
		err := span.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated varint")
		}
	})

	t.Run("truncated length varint", func(t *testing.T) {
		// Field 2, wire type 0, but truncated varint
		data := []byte{0x10, 0x80} // field=2, wire=0, truncated varint
		var span BucketSpan
		err := span.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated varint")
		}
	})

	t.Run("unknown field skipped", func(t *testing.T) {
		span := BucketSpan{Offset: -3, Length: 5}
		data := span.marshal()
		// Append unknown field 99, wire type 0
		data = append(data, byte(15<<3|0), 0x01)

		var result BucketSpan
		err := result.unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error with unknown field, got %v", err)
		}
		if result.Offset != -3 || result.Length != 5 {
			t.Errorf("expected Offset=-3, Length=5, got %+v", result)
		}
	})

	t.Run("negative offset roundtrip", func(t *testing.T) {
		span := BucketSpan{Offset: -100, Length: 42}
		data := span.marshal()

		var result BucketSpan
		err := result.unmarshal(data)
		if err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if result.Offset != -100 || result.Length != 42 {
			t.Errorf("expected Offset=-100, Length=42, got %+v", result)
		}
	})
}

// ---------------------------------------------------------------------------
// proto.go: Exemplar.unmarshal edge cases (67.6% coverage)
// ---------------------------------------------------------------------------

func TestExemplar_Unmarshal_EdgeCases(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		var e Exemplar
		err := e.unmarshal([]byte{})
		if err != nil {
			t.Errorf("expected nil error for empty data, got %v", err)
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for invalid tag")
		}
	})

	t.Run("wrong wire type for labels", func(t *testing.T) {
		// Field 1 (labels) with wire type 0 (varint) instead of 2 (length delimited)
		data := []byte{0x08, 0x05} // field=1, wire=0
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for labels")
		}
	})

	t.Run("wrong wire type for value", func(t *testing.T) {
		// Field 2 (value) with wire type 0 (varint) instead of 1 (fixed64)
		data := []byte{0x10, 0x05} // field=2, wire=0
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for value")
		}
	})

	t.Run("wrong wire type for timestamp", func(t *testing.T) {
		// Field 3 (timestamp) with wire type 1 (fixed64) instead of 0 (varint)
		data := []byte{0x19, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08} // field=3, wire=1
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for timestamp")
		}
	})

	t.Run("truncated label data", func(t *testing.T) {
		// Field 1, wire type 2, truncated length
		data := []byte{0x0a, 0x80} // field=1, wire=2, truncated length varint
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated label data")
		}
	})

	t.Run("insufficient data for value fixed64", func(t *testing.T) {
		data := []byte{0x11, 0x01, 0x02, 0x03} // field=2, wire=1, only 3 bytes
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for insufficient fixed64 data")
		}
	})

	t.Run("truncated timestamp varint", func(t *testing.T) {
		data := []byte{0x18, 0x80} // field=3, wire=0, truncated varint
		var e Exemplar
		err := e.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated timestamp")
		}
	})

	t.Run("unknown field skipped", func(t *testing.T) {
		e := Exemplar{
			Labels:    []Label{{Name: "trace_id", Value: "abc"}},
			Value:     1.5,
			Timestamp: 2000,
		}
		data := e.marshal()
		// Append unknown field 99, wire type 0
		data = append(data, byte(15<<3|0), 0x01)

		var result Exemplar
		err := result.unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if result.Value != 1.5 {
			t.Errorf("expected value 1.5, got %f", result.Value)
		}
	})

	t.Run("full roundtrip with multiple labels", func(t *testing.T) {
		original := Exemplar{
			Labels: []Label{
				{Name: "trace_id", Value: "abc123"},
				{Name: "span_id", Value: "def456"},
			},
			Value:     3.14,
			Timestamp: 9999,
		}
		data := original.marshal()

		var result Exemplar
		err := result.unmarshal(data)
		if err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if len(result.Labels) != 2 {
			t.Fatalf("expected 2 labels, got %d", len(result.Labels))
		}
		if result.Value != 3.14 {
			t.Errorf("expected value 3.14, got %f", result.Value)
		}
		if result.Timestamp != 9999 {
			t.Errorf("expected timestamp 9999, got %d", result.Timestamp)
		}
	})
}

// ---------------------------------------------------------------------------
// proto.go: MetricMetadata.unmarshal edge cases (68.4% coverage)
// ---------------------------------------------------------------------------

func TestMetricMetadata_Unmarshal_EdgeCases(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		var md MetricMetadata
		err := md.unmarshal([]byte{})
		if err != nil {
			t.Errorf("expected nil error for empty data, got %v", err)
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for invalid tag")
		}
	})

	t.Run("truncated type varint", func(t *testing.T) {
		data := []byte{0x08, 0x80} // field=1, wire=0, truncated varint
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated type varint")
		}
	})

	t.Run("wrong wire type for metric_family_name", func(t *testing.T) {
		// Field 2 with wire type 0 instead of 2
		data := []byte{0x10, 0x05} // field=2, wire=0
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type")
		}
	})

	t.Run("truncated metric_family_name", func(t *testing.T) {
		data := []byte{0x12, 0x80} // field=2, wire=2, truncated length
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated name")
		}
	})

	t.Run("wrong wire type for help", func(t *testing.T) {
		// Field 3 with wire type 0 instead of 2
		data := []byte{0x18, 0x05} // field=3, wire=0
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for help")
		}
	})

	t.Run("truncated help", func(t *testing.T) {
		data := []byte{0x1a, 0x80} // field=3, wire=2, truncated length
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated help")
		}
	})

	t.Run("wrong wire type for unit", func(t *testing.T) {
		// Field 4 with wire type 0 instead of 2
		data := []byte{0x20, 0x05} // field=4, wire=0
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for unit")
		}
	})

	t.Run("truncated unit", func(t *testing.T) {
		data := []byte{0x22, 0x80} // field=4, wire=2, truncated length
		var md MetricMetadata
		err := md.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated unit")
		}
	})

	t.Run("unknown field skipped", func(t *testing.T) {
		md := MetricMetadata{
			Type:             MetricTypeCounter,
			MetricFamilyName: "test_total",
			Help:             "A test counter",
			Unit:             "bytes",
		}
		data := md.marshal()
		// Append unknown field 99, wire type 0
		data = append(data, byte(15<<3|0), 0x01)

		var result MetricMetadata
		err := result.unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if result.MetricFamilyName != "test_total" {
			t.Errorf("expected test_total, got %s", result.MetricFamilyName)
		}
		if result.Help != "A test counter" {
			t.Errorf("expected 'A test counter', got %s", result.Help)
		}
		if result.Unit != "bytes" {
			t.Errorf("expected 'bytes', got %s", result.Unit)
		}
	})

	t.Run("all metric types roundtrip", func(t *testing.T) {
		types := []MetricType{
			MetricTypeCounter,
			MetricTypeGauge,
			MetricTypeHistogram,
			MetricTypeSummary,
			MetricTypeGaugeHistogram,
			MetricTypeInfo,
			MetricTypeStateset,
		}
		for _, mt := range types {
			md := MetricMetadata{Type: mt, MetricFamilyName: "test"}
			data := md.marshal()

			var result MetricMetadata
			err := result.unmarshal(data)
			if err != nil {
				t.Errorf("type %d: unmarshal error: %v", mt, err)
			}
			if result.Type != mt {
				t.Errorf("type %d: got type %d", mt, result.Type)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// proto.go: WriteRequest.Unmarshal edge cases (covers unknown fields path)
// ---------------------------------------------------------------------------

func TestWriteRequest_Unmarshal_UnknownFields(t *testing.T) {
	t.Run("unknown field with varint", func(t *testing.T) {
		// Build a valid WriteRequest, then append unknown field
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
				},
			},
		}
		data, err := req.Marshal()
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		// Append unknown field 99, wire type 0 (varint), value 42
		data = append(data, byte(15<<3|0), 42)

		result := &WriteRequest{}
		err = result.Unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error with unknown field, got %v", err)
		}
		if len(result.Timeseries) != 1 {
			t.Errorf("expected 1 timeseries, got %d", len(result.Timeseries))
		}
	})

	t.Run("wrong wire type for timeseries", func(t *testing.T) {
		// Field 1 (timeseries) with wire type 0 (varint) instead of 2 (length delimited)
		data := []byte{0x08, 0x05} // field=1, wire=0
		result := &WriteRequest{}
		err := result.Unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type")
		}
	})

	t.Run("wrong wire type for metadata", func(t *testing.T) {
		// Field 3 (metadata) with wire type 0 (varint) instead of 2 (length delimited)
		data := []byte{0x18, 0x05} // field=3, wire=0
		result := &WriteRequest{}
		err := result.Unmarshal(data)
		if err == nil {
			t.Error("expected error for wrong wire type for metadata")
		}
	})

	t.Run("truncated timeseries data", func(t *testing.T) {
		data := []byte{0x0a, 0x80} // field=1, wire=2, truncated length
		result := &WriteRequest{}
		err := result.Unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated timeseries data")
		}
	})

	t.Run("truncated metadata data", func(t *testing.T) {
		data := []byte{0x1a, 0x80} // field=3, wire=2, truncated length
		result := &WriteRequest{}
		err := result.Unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated metadata data")
		}
	})

	t.Run("skip field error in unknown field", func(t *testing.T) {
		// Unknown field with wire type 1 (fixed64) but insufficient data
		data := []byte{byte(15<<3 | 1), 0x01, 0x02} // field=15, wire=1, only 2 bytes
		result := &WriteRequest{}
		err := result.Unmarshal(data)
		if err == nil {
			t.Error("expected error for insufficient data in skip")
		}
	})
}

// ---------------------------------------------------------------------------
// proto.go: TimeSeries.unmarshal edge cases
// ---------------------------------------------------------------------------

func TestTimeSeries_Unmarshal_EdgeCases(t *testing.T) {
	t.Run("wrong wire type for labels", func(t *testing.T) {
		data := []byte{0x08, 0x05} // field=1, wire=0 instead of 2
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("wrong wire type for samples", func(t *testing.T) {
		data := []byte{0x10, 0x05} // field=2, wire=0 instead of 2
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("wrong wire type for exemplars", func(t *testing.T) {
		data := []byte{0x18, 0x05} // field=3, wire=0 instead of 2
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("wrong wire type for histograms", func(t *testing.T) {
		data := []byte{0x20, 0x05} // field=4, wire=0 instead of 2
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("truncated label data", func(t *testing.T) {
		data := []byte{0x0a, 0x80}
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated label data")
		}
	})

	t.Run("truncated sample data", func(t *testing.T) {
		data := []byte{0x12, 0x80}
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated sample data")
		}
	})

	t.Run("truncated exemplar data", func(t *testing.T) {
		data := []byte{0x1a, 0x80}
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated exemplar data")
		}
	})

	t.Run("truncated histogram data", func(t *testing.T) {
		data := []byte{0x22, 0x80}
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error for truncated histogram data")
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
		var ts TimeSeries
		err := ts.unmarshal(data)
		if err == nil {
			t.Error("expected error for invalid tag")
		}
	})

	t.Run("unknown field in timeseries", func(t *testing.T) {
		ts := TimeSeries{
			Labels:  []Label{{Name: "__name__", Value: "test"}},
			Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
		}
		data, _ := ts.marshal()
		// Append unknown field 99, wire type 0 (varint)
		data = append(data, byte(15<<3|0), 0x01)

		var result TimeSeries
		err := result.unmarshal(data)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if len(result.Labels) != 1 {
			t.Errorf("expected 1 label, got %d", len(result.Labels))
		}
	})
}

// ---------------------------------------------------------------------------
// queue.go: processQueue edge cases (66.7% coverage)
// ---------------------------------------------------------------------------

func TestQueue_ProcessQueue_NilExporter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-processqueue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exp)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// Push a request
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	if err := q.Push(req); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	// Set exporter to nil
	q.SetExporter(nil)

	// processQueue should return false (no exporter)
	result := q.processQueue()
	if result {
		t.Error("expected false when exporter is nil")
	}
}

func TestQueue_ProcessQueue_ExportFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-exportfail-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{
		failCount: 1000,
		failErr:   errors.New("permanent failure"),
	}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exp)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// Push a request
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	if err := q.Push(req); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	// processQueue should return false (export failed)
	result := q.processQueue()
	if result {
		t.Error("expected false when export fails")
	}

	// Entry should still be in queue
	if q.Len() != 1 {
		t.Errorf("expected 1 entry in queue, got %d", q.Len())
	}
}

func TestQueue_ProcessQueue_EmptyQueue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-empty-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exp)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// processQueue on empty queue should return true
	result := q.processQueue()
	if !result {
		t.Error("expected true for empty queue")
	}
}

// ---------------------------------------------------------------------------
// queue.go: Export with errors and retries (70% coverage)
// ---------------------------------------------------------------------------

func TestQueue_Export_NilAndEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-nilexport-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exp)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// Export nil request
	if err := q.Export(context.Background(), nil); err != nil {
		t.Errorf("Export(nil) error = %v", err)
	}

	// Export empty request
	if err := q.Export(context.Background(), &WriteRequest{}); err != nil {
		t.Errorf("Export(empty) error = %v", err)
	}

	if q.Len() != 0 {
		t.Errorf("queue should be empty, got len=%d", q.Len())
	}
}

// ---------------------------------------------------------------------------
// queue.go: minDuration helper
// ---------------------------------------------------------------------------

func TestMinDuration(t *testing.T) {
	tests := []struct {
		a, b, want time.Duration
	}{
		{1 * time.Second, 2 * time.Second, 1 * time.Second},
		{5 * time.Minute, 3 * time.Minute, 3 * time.Minute},
		{time.Second, time.Second, time.Second},
	}
	for _, tt := range tests {
		got := minDuration(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minDuration(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// buffer.go: flush error paths (74.3% coverage)
// ---------------------------------------------------------------------------

// failingExporter simulates export failures
type failingExporter struct {
	mu        sync.Mutex
	callCount int
	failUntil int
	failErr   error
	exports   []*WriteRequest
}

func (f *failingExporter) Export(ctx context.Context, req *WriteRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.callCount <= f.failUntil {
		return f.failErr
	}
	f.exports = append(f.exports, req)
	return nil
}

func (f *failingExporter) Close() error {
	return nil
}

// mockLogAggregator implements LogAggregator for testing
type mockLogAggregator struct {
	mu     sync.Mutex
	errors []string
}

func (m *mockLogAggregator) Error(key string, message string, fields map[string]interface{}, datapoints int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, fmt.Sprintf("%s: %s", key, message))
}

func (m *mockLogAggregator) Stop() {}

func TestBuffer_Flush_ExportError(t *testing.T) {
	exp := &failingExporter{
		failUntil: 100,
		failErr:   errors.New("export error"),
	}
	stats := &mockStats{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data
	for i := 0; i < 5; i++ {
		buf.Add(&WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i)}},
				},
			},
		})
	}

	// Wait for flush attempt
	time.Sleep(150 * time.Millisecond)
	cancel()
	buf.Wait()

	// Should have recorded export errors
	if stats.exportErrors == 0 {
		t.Error("expected export errors to be recorded")
	}
}

func TestBuffer_Flush_ExportError_WithLogAggregator(t *testing.T) {
	exp := &failingExporter{
		failUntil: 100,
		failErr:   errors.New("export error"),
	}
	logAgg := &mockLogAggregator{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp, nil, nil, logAgg)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data
	for i := 0; i < 3; i++ {
		buf.Add(&WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i)}},
				},
			},
		})
	}

	// Wait for flush
	time.Sleep(150 * time.Millisecond)
	cancel()
	buf.Wait()

	// Log aggregator should have recorded errors
	logAgg.mu.Lock()
	errCount := len(logAgg.errors)
	logAgg.mu.Unlock()

	if errCount == 0 {
		t.Error("expected log aggregator to record errors")
	}
}

func TestBuffer_Flush_NilExporter(t *testing.T) {
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data
	buf.Add(&WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	})

	// Wait for flush attempt - should not panic
	time.Sleep(150 * time.Millisecond)
	cancel()
	buf.Wait()
}

func TestBuffer_Flush_MultipleBatches_WithMetadata(t *testing.T) {
	exp := &mockExporter{}
	stats := &mockStats{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  3,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data with metadata
	buf.Add(&WriteRequest{
		Timeseries: []TimeSeries{
			{Labels: []Label{{Name: "__name__", Value: "m1"}}, Samples: []Sample{{Value: 1.0, Timestamp: 1000}}},
			{Labels: []Label{{Name: "__name__", Value: "m2"}}, Samples: []Sample{{Value: 2.0, Timestamp: 2000}}},
			{Labels: []Label{{Name: "__name__", Value: "m3"}}, Samples: []Sample{{Value: 3.0, Timestamp: 3000}}},
			{Labels: []Label{{Name: "__name__", Value: "m4"}}, Samples: []Sample{{Value: 4.0, Timestamp: 4000}}},
			{Labels: []Label{{Name: "__name__", Value: "m5"}}, Samples: []Sample{{Value: 5.0, Timestamp: 5000}}},
		},
		Metadata: []MetricMetadata{
			{Type: MetricTypeCounter, MetricFamilyName: "m1"},
		},
	})

	time.Sleep(150 * time.Millisecond)
	cancel()
	buf.Wait()

	// Multiple batches should have been sent (5 items / 3 batch size = 2 batches)
	exported := exp.getExported()
	if len(exported) < 2 {
		t.Errorf("expected at least 2 batches, got %d", len(exported))
	}

	// Only first batch should have metadata
	if len(exported) > 0 && len(exported[0].Metadata) != 1 {
		t.Errorf("first batch should have metadata, got %d", len(exported[0].Metadata))
	}
	if len(exported) > 1 && len(exported[1].Metadata) != 0 {
		t.Errorf("second batch should not have metadata, got %d", len(exported[1].Metadata))
	}

	// Stats should record bytes sent
	if stats.sentBytes == 0 {
		t.Error("expected sentBytes > 0")
	}
}

// ---------------------------------------------------------------------------
// splitter.go: SplitTimeseries edge cases (80% coverage)
// ---------------------------------------------------------------------------

func TestSplitter_SplitTimeseries_Empty(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"ep1:9090", "ep2:9090"})
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	splitter := NewSplitter(keyBuilder, hashRing)

	// Empty timeseries
	result := splitter.SplitTimeseries(nil)
	if result != nil {
		t.Error("expected nil for nil timeseries")
	}

	result = splitter.SplitTimeseries([]TimeSeries{})
	if result != nil {
		t.Error("expected nil for empty timeseries")
	}
}

func TestSplitter_SplitTimeseries_AllToSameEndpoint(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"single-ep:9090"})
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	timeseries := []TimeSeries{
		{Labels: []Label{{Name: "__name__", Value: "m1"}}, Samples: []Sample{{Value: 1.0}}},
		{Labels: []Label{{Name: "__name__", Value: "m2"}}, Samples: []Sample{{Value: 2.0}}},
		{Labels: []Label{{Name: "__name__", Value: "m3"}}, Samples: []Sample{{Value: 3.0}}},
	}

	result := splitter.SplitTimeseries(timeseries)
	if len(result) != 1 {
		t.Errorf("expected 1 endpoint, got %d", len(result))
	}

	for _, ts := range result {
		if len(ts) != 3 {
			t.Errorf("expected 3 timeseries at endpoint, got %d", len(ts))
		}
	}
}
