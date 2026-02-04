package prw

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/szibis/metrics-governor/internal/intern"
)

// This file provides protobuf encoding/decoding for PRW wire format.
// We use manual encoding to avoid external dependencies on prometheus/prompb.
// The wire format is compatible with prometheus/prompb.

// Protobuf wire types
const (
	wireVarint      = 0
	wireFixed64     = 1
	wireLengthDelim = 2
	wireFixed32     = 5
)

// Protobuf field numbers for WriteRequest
const (
	fieldWriteRequestTimeseries = 1
	fieldWriteRequestMetadata   = 3
)

// Protobuf field numbers for TimeSeries
const (
	fieldTimeSeriesLabels     = 1
	fieldTimeSeriesSamples    = 2
	fieldTimeSeriesExemplars  = 3
	fieldTimeSeriesHistograms = 4
)

// Protobuf field numbers for Label
const (
	fieldLabelName  = 1
	fieldLabelValue = 2
)

// Protobuf field numbers for Sample
const (
	fieldSampleValue     = 1
	fieldSampleTimestamp = 2
)

// Protobuf field numbers for Exemplar
const (
	fieldExemplarLabels    = 1
	fieldExemplarValue     = 2
	fieldExemplarTimestamp = 3
)

// Protobuf field numbers for Histogram
const (
	fieldHistogramCount          = 1
	fieldHistogramCountFloat     = 2
	fieldHistogramSum            = 3
	fieldHistogramSchema         = 4
	fieldHistogramZeroThreshold  = 5
	fieldHistogramZeroCount      = 6
	fieldHistogramZeroCountFloat = 7
	fieldHistogramNegativeSpans  = 8
	fieldHistogramNegativeDeltas = 9
	fieldHistogramNegativeCounts = 10
	fieldHistogramPositiveSpans  = 11
	fieldHistogramPositiveDeltas = 12
	fieldHistogramPositiveCounts = 13
	fieldHistogramResetHint      = 14
	fieldHistogramTimestamp      = 15
)

// Protobuf field numbers for BucketSpan
const (
	fieldBucketSpanOffset = 1
	fieldBucketSpanLength = 2
)

// Protobuf field numbers for MetricMetadata
const (
	fieldMetadataType             = 1
	fieldMetadataMetricFamilyName = 2
	fieldMetadataHelp             = 3
	fieldMetadataUnit             = 4
)

// Interning configuration
var (
	// labelNameIntern is used for interning label names (small fixed set)
	labelNameIntern = intern.CommonLabels()

	// labelValueIntern is used for interning short common label values
	labelValueIntern = intern.NewPool()

	// InternMaxValueLength is the maximum length for value interning.
	// Values longer than this are not interned to avoid unbounded memory growth.
	InternMaxValueLength = 64

	// InternEnabled controls whether string interning is enabled.
	// This can be set to false to disable interning for debugging or testing.
	InternEnabled = true
)

// UnmarshalWriteRequest deserializes a WriteRequest from protobuf wire format bytes.
func UnmarshalWriteRequest(data []byte) (*WriteRequest, error) {
	var req WriteRequest
	if err := req.Unmarshal(data); err != nil {
		return nil, err
	}
	return &req, nil
}

// Marshal encodes a WriteRequest to protobuf wire format.
func (req *WriteRequest) Marshal() ([]byte, error) {
	// Estimate size
	size := req.estimateSize()
	buf := make([]byte, 0, size)

	// Encode timeseries
	for i := range req.Timeseries {
		tsBytes, err := req.Timeseries[i].marshal()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal timeseries %d: %w", i, err)
		}
		buf = appendTagLengthDelim(buf, fieldWriteRequestTimeseries, tsBytes)
	}

	// Encode metadata (PRW 2.0)
	for i := range req.Metadata {
		mdBytes := req.Metadata[i].marshal()
		buf = appendTagLengthDelim(buf, fieldWriteRequestMetadata, mdBytes)
	}

	return buf, nil
}

// Unmarshal decodes a WriteRequest from protobuf wire format.
func (req *WriteRequest) Unmarshal(data []byte) error {
	req.Timeseries = nil
	req.Metadata = nil

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldWriteRequestTimeseries:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for timeseries: %d", wireType)
			}
			tsData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid timeseries length")
			}
			data = data[n:]

			var ts TimeSeries
			if err := ts.unmarshal(tsData); err != nil {
				return fmt.Errorf("failed to unmarshal timeseries: %w", err)
			}
			req.Timeseries = append(req.Timeseries, ts)

		case fieldWriteRequestMetadata:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for metadata: %d", wireType)
			}
			mdData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid metadata length")
			}
			data = data[n:]

			var md MetricMetadata
			if err := md.unmarshal(mdData); err != nil {
				return fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
			req.Metadata = append(req.Metadata, md)

		default:
			// Skip unknown fields
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (req *WriteRequest) estimateSize() int {
	size := 0
	for i := range req.Timeseries {
		size += 2 + req.Timeseries[i].estimateSize() // tag + length + data
	}
	for i := range req.Metadata {
		size += 2 + req.Metadata[i].estimateSize()
	}
	return size
}

// EstimateSize returns an estimate of the marshaled size in bytes.
// This is useful for tracking uncompressed payload sizes.
func (req *WriteRequest) EstimateSize() int {
	return req.estimateSize()
}

func (ts *TimeSeries) marshal() ([]byte, error) { //nolint:unparam // error kept for API consistency
	buf := make([]byte, 0, ts.estimateSize())

	// Encode labels
	for i := range ts.Labels {
		labelBytes := ts.Labels[i].marshal()
		buf = appendTagLengthDelim(buf, fieldTimeSeriesLabels, labelBytes)
	}

	// Encode samples
	for i := range ts.Samples {
		sampleBytes := ts.Samples[i].marshal()
		buf = appendTagLengthDelim(buf, fieldTimeSeriesSamples, sampleBytes)
	}

	// Encode exemplars (PRW 2.0)
	for i := range ts.Exemplars {
		exemplarBytes := ts.Exemplars[i].marshal()
		buf = appendTagLengthDelim(buf, fieldTimeSeriesExemplars, exemplarBytes)
	}

	// Encode histograms (PRW 2.0)
	for i := range ts.Histograms {
		histBytes := ts.Histograms[i].marshal()
		buf = appendTagLengthDelim(buf, fieldTimeSeriesHistograms, histBytes)
	}

	return buf, nil
}

func (ts *TimeSeries) unmarshal(data []byte) error {
	ts.Labels = nil
	ts.Samples = nil
	ts.Exemplars = nil
	ts.Histograms = nil

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldTimeSeriesLabels:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for labels: %d", wireType)
			}
			labelData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid label length")
			}
			data = data[n:]

			var l Label
			if err := l.unmarshal(labelData); err != nil {
				return fmt.Errorf("failed to unmarshal label: %w", err)
			}
			ts.Labels = append(ts.Labels, l)

		case fieldTimeSeriesSamples:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for sample: %d", wireType)
			}
			sampleData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid sample length")
			}
			data = data[n:]

			var s Sample
			if err := s.unmarshal(sampleData); err != nil {
				return fmt.Errorf("failed to unmarshal sample: %w", err)
			}
			ts.Samples = append(ts.Samples, s)

		case fieldTimeSeriesExemplars:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for exemplar: %d", wireType)
			}
			exemplarData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid exemplar length")
			}
			data = data[n:]

			var e Exemplar
			if err := e.unmarshal(exemplarData); err != nil {
				return fmt.Errorf("failed to unmarshal exemplar: %w", err)
			}
			ts.Exemplars = append(ts.Exemplars, e)

		case fieldTimeSeriesHistograms:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for histogram: %d", wireType)
			}
			histData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid histogram length")
			}
			data = data[n:]

			var h Histogram
			if err := h.unmarshal(histData); err != nil {
				return fmt.Errorf("failed to unmarshal histogram: %w", err)
			}
			ts.Histograms = append(ts.Histograms, h)

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (ts *TimeSeries) estimateSize() int {
	size := 0
	for i := range ts.Labels {
		size += 2 + ts.Labels[i].estimateSize()
	}
	for i := range ts.Samples {
		size += 2 + ts.Samples[i].estimateSize()
	}
	for i := range ts.Exemplars {
		size += 2 + ts.Exemplars[i].estimateSize()
	}
	for i := range ts.Histograms {
		size += 2 + ts.Histograms[i].estimateSize()
	}
	return size
}

func (l *Label) marshal() []byte {
	buf := make([]byte, 0, l.estimateSize())
	buf = appendTagLengthDelim(buf, fieldLabelName, []byte(l.Name))
	buf = appendTagLengthDelim(buf, fieldLabelValue, []byte(l.Value))
	return buf
}

func (l *Label) unmarshal(data []byte) error {
	l.Name = ""
	l.Value = ""

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldLabelName:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for name: %d", wireType)
			}
			nameBytes, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid name length")
			}
			data = data[n:]
			// Always intern label names (small fixed set like __name__, job, instance)
			if InternEnabled {
				l.Name = labelNameIntern.InternBytes(nameBytes)
			} else {
				l.Name = string(nameBytes)
			}

		case fieldLabelValue:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for value: %d", wireType)
			}
			valueBytes, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid value length")
			}
			data = data[n:]
			// Only intern short values to avoid unbounded memory growth
			if InternEnabled && len(valueBytes) <= InternMaxValueLength {
				l.Value = labelValueIntern.InternBytes(valueBytes)
			} else {
				l.Value = string(valueBytes)
			}

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

// LabelInternStats returns interning statistics for label names and values.
func LabelInternStats() (nameHits, nameMisses, valueHits, valueMisses uint64) {
	nameHits, nameMisses = labelNameIntern.Stats()
	valueHits, valueMisses = labelValueIntern.Stats()
	return
}

func (l *Label) estimateSize() int {
	return 2 + len(l.Name) + 2 + len(l.Value)
}

func (s *Sample) marshal() []byte {
	buf := make([]byte, 0, s.estimateSize())
	buf = appendTagFixed64(buf, fieldSampleValue, math.Float64bits(s.Value))
	buf = appendTagVarint(buf, fieldSampleTimestamp, uint64(s.Timestamp))
	return buf
}

func (s *Sample) unmarshal(data []byte) error {
	s.Value = 0
	s.Timestamp = 0

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldSampleValue:
			if wireType != wireFixed64 {
				return fmt.Errorf("invalid wire type for value: %d", wireType)
			}
			if len(data) < 8 {
				return fmt.Errorf("insufficient data for fixed64")
			}
			s.Value = math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))
			data = data[8:]

		case fieldSampleTimestamp:
			if wireType != wireVarint {
				return fmt.Errorf("invalid wire type for timestamp: %d", wireType)
			}
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid timestamp varint")
			}
			s.Timestamp = int64(v)
			data = data[n:]

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (s *Sample) estimateSize() int {
	return 1 + 8 + 1 + 10 // tag + fixed64 + tag + varint (max 10 bytes)
}

func (e *Exemplar) marshal() []byte {
	buf := make([]byte, 0, e.estimateSize())
	for i := range e.Labels {
		labelBytes := e.Labels[i].marshal()
		buf = appendTagLengthDelim(buf, fieldExemplarLabels, labelBytes)
	}
	buf = appendTagFixed64(buf, fieldExemplarValue, math.Float64bits(e.Value))
	buf = appendTagVarint(buf, fieldExemplarTimestamp, uint64(e.Timestamp))
	return buf
}

func (e *Exemplar) unmarshal(data []byte) error {
	e.Labels = nil
	e.Value = 0
	e.Timestamp = 0

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldExemplarLabels:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for labels: %d", wireType)
			}
			labelData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid label length")
			}
			data = data[n:]

			var l Label
			if err := l.unmarshal(labelData); err != nil {
				return fmt.Errorf("failed to unmarshal label: %w", err)
			}
			e.Labels = append(e.Labels, l)

		case fieldExemplarValue:
			if wireType != wireFixed64 {
				return fmt.Errorf("invalid wire type for value: %d", wireType)
			}
			if len(data) < 8 {
				return fmt.Errorf("insufficient data for fixed64")
			}
			e.Value = math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))
			data = data[8:]

		case fieldExemplarTimestamp:
			if wireType != wireVarint {
				return fmt.Errorf("invalid wire type for timestamp: %d", wireType)
			}
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid timestamp varint")
			}
			e.Timestamp = int64(v)
			data = data[n:]

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (e *Exemplar) estimateSize() int {
	size := 1 + 8 + 1 + 10 // value + timestamp
	for i := range e.Labels {
		size += 2 + e.Labels[i].estimateSize()
	}
	return size
}

func (h *Histogram) marshal() []byte {
	buf := make([]byte, 0, h.estimateSize())

	if h.Count != 0 {
		buf = appendTagVarint(buf, fieldHistogramCount, h.Count)
	}
	if h.CountFloat != 0 {
		buf = appendTagFixed64(buf, fieldHistogramCountFloat, math.Float64bits(h.CountFloat))
	}
	if h.Sum != 0 {
		buf = appendTagFixed64(buf, fieldHistogramSum, math.Float64bits(h.Sum))
	}
	if h.Schema != 0 {
		buf = appendTagVarintSigned(buf, fieldHistogramSchema, int64(h.Schema))
	}
	if h.ZeroThreshold != 0 {
		buf = appendTagFixed64(buf, fieldHistogramZeroThreshold, math.Float64bits(h.ZeroThreshold))
	}
	if h.ZeroCount != 0 {
		buf = appendTagVarint(buf, fieldHistogramZeroCount, h.ZeroCount)
	}
	if h.ZeroCountFloat != 0 {
		buf = appendTagFixed64(buf, fieldHistogramZeroCountFloat, math.Float64bits(h.ZeroCountFloat))
	}

	for i := range h.NegativeSpans {
		spanBytes := h.NegativeSpans[i].marshal()
		buf = appendTagLengthDelim(buf, fieldHistogramNegativeSpans, spanBytes)
	}

	if len(h.NegativeDeltas) > 0 {
		deltasBytes := marshalSignedVarints(h.NegativeDeltas)
		buf = appendTagLengthDelim(buf, fieldHistogramNegativeDeltas, deltasBytes)
	}

	if len(h.NegativeCounts) > 0 {
		countsBytes := marshalFixed64s(h.NegativeCounts)
		buf = appendTagLengthDelim(buf, fieldHistogramNegativeCounts, countsBytes)
	}

	for i := range h.PositiveSpans {
		spanBytes := h.PositiveSpans[i].marshal()
		buf = appendTagLengthDelim(buf, fieldHistogramPositiveSpans, spanBytes)
	}

	if len(h.PositiveDeltas) > 0 {
		deltasBytes := marshalSignedVarints(h.PositiveDeltas)
		buf = appendTagLengthDelim(buf, fieldHistogramPositiveDeltas, deltasBytes)
	}

	if len(h.PositiveCounts) > 0 {
		countsBytes := marshalFixed64s(h.PositiveCounts)
		buf = appendTagLengthDelim(buf, fieldHistogramPositiveCounts, countsBytes)
	}

	if h.ResetHint != 0 {
		buf = appendTagVarint(buf, fieldHistogramResetHint, uint64(h.ResetHint))
	}

	buf = appendTagVarint(buf, fieldHistogramTimestamp, uint64(h.Timestamp))

	return buf
}

func (h *Histogram) unmarshal(data []byte) error {
	*h = Histogram{} // Reset all fields

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldHistogramCount:
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid count varint")
			}
			h.Count = v
			data = data[n:]

		case fieldHistogramCountFloat:
			if len(data) < 8 {
				return fmt.Errorf("insufficient data for count_float")
			}
			h.CountFloat = math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))
			data = data[8:]

		case fieldHistogramSum:
			if len(data) < 8 {
				return fmt.Errorf("insufficient data for sum")
			}
			h.Sum = math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))
			data = data[8:]

		case fieldHistogramSchema:
			v, n := consumeSignedVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid schema varint")
			}
			h.Schema = int32(v)
			data = data[n:]

		case fieldHistogramZeroThreshold:
			if len(data) < 8 {
				return fmt.Errorf("insufficient data for zero_threshold")
			}
			h.ZeroThreshold = math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))
			data = data[8:]

		case fieldHistogramZeroCount:
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid zero_count varint")
			}
			h.ZeroCount = v
			data = data[n:]

		case fieldHistogramZeroCountFloat:
			if len(data) < 8 {
				return fmt.Errorf("insufficient data for zero_count_float")
			}
			h.ZeroCountFloat = math.Float64frombits(binary.LittleEndian.Uint64(data[:8]))
			data = data[8:]

		case fieldHistogramNegativeSpans:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for negative_spans: %d", wireType)
			}
			spanData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid span length")
			}
			data = data[n:]

			var span BucketSpan
			if err := span.unmarshal(spanData); err != nil {
				return fmt.Errorf("failed to unmarshal span: %w", err)
			}
			h.NegativeSpans = append(h.NegativeSpans, span)

		case fieldHistogramNegativeDeltas:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for negative_deltas: %d", wireType)
			}
			deltasData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid deltas length")
			}
			data = data[n:]
			h.NegativeDeltas = unmarshalSignedVarints(deltasData)

		case fieldHistogramNegativeCounts:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for negative_counts: %d", wireType)
			}
			countsData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid counts length")
			}
			data = data[n:]
			h.NegativeCounts = unmarshalFixed64s(countsData)

		case fieldHistogramPositiveSpans:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for positive_spans: %d", wireType)
			}
			spanData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid span length")
			}
			data = data[n:]

			var span BucketSpan
			if err := span.unmarshal(spanData); err != nil {
				return fmt.Errorf("failed to unmarshal span: %w", err)
			}
			h.PositiveSpans = append(h.PositiveSpans, span)

		case fieldHistogramPositiveDeltas:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for positive_deltas: %d", wireType)
			}
			deltasData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid deltas length")
			}
			data = data[n:]
			h.PositiveDeltas = unmarshalSignedVarints(deltasData)

		case fieldHistogramPositiveCounts:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for positive_counts: %d", wireType)
			}
			countsData, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid counts length")
			}
			data = data[n:]
			h.PositiveCounts = unmarshalFixed64s(countsData)

		case fieldHistogramResetHint:
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid reset_hint varint")
			}
			h.ResetHint = HistogramResetHint(v)
			data = data[n:]

		case fieldHistogramTimestamp:
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid timestamp varint")
			}
			h.Timestamp = int64(v)
			data = data[n:]

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (h *Histogram) estimateSize() int {
	// Rough estimate
	size := 100
	size += len(h.NegativeSpans) * 10
	size += len(h.NegativeDeltas) * 10
	size += len(h.NegativeCounts) * 8
	size += len(h.PositiveSpans) * 10
	size += len(h.PositiveDeltas) * 10
	size += len(h.PositiveCounts) * 8
	return size
}

func (span *BucketSpan) marshal() []byte {
	buf := make([]byte, 0, 10)
	buf = appendTagVarintSigned(buf, fieldBucketSpanOffset, int64(span.Offset))
	buf = appendTagVarint(buf, fieldBucketSpanLength, uint64(span.Length))
	return buf
}

func (span *BucketSpan) unmarshal(data []byte) error {
	span.Offset = 0
	span.Length = 0

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldBucketSpanOffset:
			if wireType != wireVarint {
				return fmt.Errorf("invalid wire type for offset: %d", wireType)
			}
			v, n := consumeSignedVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid offset varint")
			}
			span.Offset = int32(v)
			data = data[n:]

		case fieldBucketSpanLength:
			if wireType != wireVarint {
				return fmt.Errorf("invalid wire type for length: %d", wireType)
			}
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid length varint")
			}
			span.Length = uint32(v)
			data = data[n:]

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (md *MetricMetadata) marshal() []byte {
	buf := make([]byte, 0, md.estimateSize())
	if md.Type != 0 {
		buf = appendTagVarint(buf, fieldMetadataType, uint64(md.Type))
	}
	if md.MetricFamilyName != "" {
		buf = appendTagLengthDelim(buf, fieldMetadataMetricFamilyName, []byte(md.MetricFamilyName))
	}
	if md.Help != "" {
		buf = appendTagLengthDelim(buf, fieldMetadataHelp, []byte(md.Help))
	}
	if md.Unit != "" {
		buf = appendTagLengthDelim(buf, fieldMetadataUnit, []byte(md.Unit))
	}
	return buf
}

func (md *MetricMetadata) unmarshal(data []byte) error {
	*md = MetricMetadata{} // Reset

	for len(data) > 0 {
		fieldNum, wireType, n := consumeTag(data)
		if n < 0 {
			return fmt.Errorf("invalid tag")
		}
		data = data[n:]

		switch fieldNum {
		case fieldMetadataType:
			v, n := consumeVarint(data)
			if n < 0 {
				return fmt.Errorf("invalid type varint")
			}
			md.Type = MetricType(v)
			data = data[n:]

		case fieldMetadataMetricFamilyName:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for metric_family_name: %d", wireType)
			}
			nameBytes, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid name length")
			}
			data = data[n:]
			md.MetricFamilyName = string(nameBytes)

		case fieldMetadataHelp:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for help: %d", wireType)
			}
			helpBytes, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid help length")
			}
			data = data[n:]
			md.Help = string(helpBytes)

		case fieldMetadataUnit:
			if wireType != wireLengthDelim {
				return fmt.Errorf("invalid wire type for unit: %d", wireType)
			}
			unitBytes, n := consumeBytes(data)
			if n < 0 {
				return fmt.Errorf("invalid unit length")
			}
			data = data[n:]
			md.Unit = string(unitBytes)

		default:
			n, err := skipField(data, wireType)
			if err != nil {
				return fmt.Errorf("failed to skip unknown field %d: %w", fieldNum, err)
			}
			data = data[n:]
		}
	}

	return nil
}

func (md *MetricMetadata) estimateSize() int {
	return 2 + len(md.MetricFamilyName) + 2 + len(md.Help) + 2 + len(md.Unit) + 2
}

// Helper functions for protobuf encoding/decoding

func appendTagVarint(buf []byte, fieldNum int, v uint64) []byte {
	tag := uint64(fieldNum<<3) | wireVarint
	buf = appendVarint(buf, tag)
	buf = appendVarint(buf, v)
	return buf
}

func appendTagVarintSigned(buf []byte, fieldNum int, v int64) []byte {
	tag := uint64(fieldNum<<3) | wireVarint
	buf = appendVarint(buf, tag)
	// ZigZag encoding for signed integers
	buf = appendVarint(buf, uint64((v<<1)^(v>>63)))
	return buf
}

func appendTagFixed64(buf []byte, fieldNum int, v uint64) []byte {
	tag := uint64(fieldNum<<3) | wireFixed64
	buf = appendVarint(buf, tag)
	buf = append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
	return buf
}

func appendTagLengthDelim(buf []byte, fieldNum int, data []byte) []byte {
	tag := uint64(fieldNum<<3) | wireLengthDelim
	buf = appendVarint(buf, tag)
	buf = appendVarint(buf, uint64(len(data)))
	buf = append(buf, data...)
	return buf
}

func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	buf = append(buf, byte(v))
	return buf
}

func consumeTag(data []byte) (fieldNum int, wireType int, n int) {
	v, n := consumeVarint(data)
	if n < 0 {
		return 0, 0, -1
	}
	return int(v >> 3), int(v & 0x7), n
}

func consumeVarint(data []byte) (v uint64, n int) {
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		v |= uint64(b&0x7F) << (7 * i)
		if b < 0x80 {
			return v, i + 1
		}
	}
	return 0, -1
}

func consumeSignedVarint(data []byte) (v int64, n int) {
	uv, n := consumeVarint(data)
	if n < 0 {
		return 0, -1
	}
	// ZigZag decoding
	v = int64(uv>>1) ^ -int64(uv&1)
	return v, n
}

func consumeBytes(data []byte) (b []byte, n int) {
	length, vn := consumeVarint(data)
	if vn < 0 {
		return nil, -1
	}
	if uint64(len(data)-vn) < length {
		return nil, -1
	}
	return data[vn : vn+int(length)], vn + int(length)
}

func skipField(data []byte, wireType int) (n int, err error) {
	switch wireType {
	case wireVarint:
		_, n := consumeVarint(data)
		return n, nil
	case wireFixed64:
		if len(data) < 8 {
			return 0, fmt.Errorf("insufficient data for fixed64")
		}
		return 8, nil
	case wireLengthDelim:
		length, vn := consumeVarint(data)
		if vn < 0 {
			return 0, fmt.Errorf("invalid length")
		}
		return vn + int(length), nil
	case wireFixed32:
		if len(data) < 4 {
			return 0, fmt.Errorf("insufficient data for fixed32")
		}
		return 4, nil
	default:
		return 0, fmt.Errorf("unknown wire type: %d", wireType)
	}
}

func marshalSignedVarints(values []int64) []byte {
	buf := make([]byte, 0, len(values)*5)
	for _, v := range values {
		// ZigZag encoding
		buf = appendVarint(buf, uint64((v<<1)^(v>>63)))
	}
	return buf
}

func unmarshalSignedVarints(data []byte) []int64 {
	var values []int64
	for len(data) > 0 {
		v, n := consumeSignedVarint(data)
		if n < 0 {
			break
		}
		values = append(values, v)
		data = data[n:]
	}
	return values
}

func marshalFixed64s(values []float64) []byte {
	buf := make([]byte, len(values)*8)
	for i, v := range values {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

func unmarshalFixed64s(data []byte) []float64 {
	count := len(data) / 8
	values := make([]float64, count)
	for i := 0; i < count; i++ {
		values[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return values
}
