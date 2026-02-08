package sampling

import (
	"math"
	"sort"
)

// seriesAccumulator accumulates values from multiple series within
// an aggregation window and produces a single result.
type seriesAccumulator interface {
	Add(value float64)
	Result() float64
	Reset()
}

// newAccumulator creates an accumulator for the given aggregation function.
func newAccumulator(fn AggFunc) seriesAccumulator {
	switch fn {
	case AggSum:
		return &sumAccum{}
	case AggAvg:
		return &avgAccum{}
	case AggMin:
		return &minAccum{}
	case AggMax:
		return &maxAccum{}
	case AggCount:
		return &countAccum{}
	case AggLast:
		return &lastAccum{}
	case AggIncrease:
		return &increaseAccum{}
	case AggRate:
		return &rateAccum{}
	case AggStddev:
		return &stddevAccum{}
	case AggStdvar:
		return &stdvarAccum{}
	default:
		return &lastAccum{}
	}
}

// --- sumAccum ---

type sumAccum struct {
	sum float64
}

func (a *sumAccum) Add(v float64)   { a.sum += v }
func (a *sumAccum) Result() float64 { return a.sum }
func (a *sumAccum) Reset()          { a.sum = 0 }

// --- avgAccum ---

type avgAccum struct {
	sum   float64
	count int64
}

func (a *avgAccum) Add(v float64) { a.sum += v; a.count++ }
func (a *avgAccum) Result() float64 {
	if a.count == 0 {
		return 0
	}
	return a.sum / float64(a.count)
}
func (a *avgAccum) Reset() { a.sum = 0; a.count = 0 }

// --- minAccum ---

type minAccum struct {
	min     float64
	hasData bool
}

func (a *minAccum) Add(v float64) {
	if !a.hasData || v < a.min {
		a.min = v
		a.hasData = true
	}
}
func (a *minAccum) Result() float64 {
	if !a.hasData {
		return 0
	}
	return a.min
}
func (a *minAccum) Reset() { a.min = 0; a.hasData = false }

// --- maxAccum ---

type maxAccum struct {
	max     float64
	hasData bool
}

func (a *maxAccum) Add(v float64) {
	if !a.hasData || v > a.max {
		a.max = v
		a.hasData = true
	}
}
func (a *maxAccum) Result() float64 {
	if !a.hasData {
		return 0
	}
	return a.max
}
func (a *maxAccum) Reset() { a.max = 0; a.hasData = false }

// --- countAccum ---

type countAccum struct {
	count int64
}

func (a *countAccum) Add(_ float64)   { a.count++ }
func (a *countAccum) Result() float64 { return float64(a.count) }
func (a *countAccum) Reset()          { a.count = 0 }

// --- lastAccum ---

type lastAccum struct {
	last    float64
	hasData bool
}

func (a *lastAccum) Add(v float64) { a.last = v; a.hasData = true }
func (a *lastAccum) Result() float64 {
	if !a.hasData {
		return 0
	}
	return a.last
}
func (a *lastAccum) Reset() { a.last = 0; a.hasData = false }

// --- increaseAccum ---
// Tracks first and last values to compute the increase (last - first).

type increaseAccum struct {
	first   float64
	last    float64
	hasData bool
}

func (a *increaseAccum) Add(v float64) {
	if !a.hasData {
		a.first = v
		a.hasData = true
	}
	a.last = v
}
func (a *increaseAccum) Result() float64 {
	if !a.hasData {
		return 0
	}
	return a.last - a.first
}
func (a *increaseAccum) Reset() { a.first = 0; a.last = 0; a.hasData = false }

// --- rateAccum ---
// Like increase but returns increase per second.
// Requires knowing the window duration — set via setWindow().

type rateAccum struct {
	increaseAccum
	windowSecs float64
}

func (a *rateAccum) Result() float64 {
	if !a.hasData || a.windowSecs <= 0 {
		return 0
	}
	return (a.last - a.first) / a.windowSecs
}
func (a *rateAccum) Reset() { a.increaseAccum.Reset() }

// setWindow sets the window duration for rate calculation.
func (a *rateAccum) setWindow(secs float64) { a.windowSecs = secs }

// --- stddevAccum ---
// Uses Welford's online algorithm for numerical stability.

type stddevAccum struct {
	count int64
	mean  float64
	m2    float64
}

func (a *stddevAccum) Add(v float64) {
	a.count++
	delta := v - a.mean
	a.mean += delta / float64(a.count)
	delta2 := v - a.mean
	a.m2 += delta * delta2
}
func (a *stddevAccum) Result() float64 {
	if a.count < 2 {
		return 0
	}
	return math.Sqrt(a.m2 / float64(a.count))
}
func (a *stddevAccum) Reset() { a.count = 0; a.mean = 0; a.m2 = 0 }

// --- stdvarAccum ---
// Same as stddev but returns variance (no sqrt).

type stdvarAccum struct {
	stddevAccum
}

func (a *stdvarAccum) Result() float64 {
	if a.count < 2 {
		return 0
	}
	return a.m2 / float64(a.count)
}

// --- quantilesAccum ---
// Collects all values and computes exact quantiles at flush time.
// Memory: O(n) where n is datapoints per window — suitable for
// pre-aggregated data (not raw high-cardinality).

type quantilesAccum struct {
	values    []float64
	quantiles []float64 // target quantile levels (e.g. 0.5, 0.9, 0.99)
}

func newQuantilesAccum(quantiles []float64) *quantilesAccum {
	return &quantilesAccum{
		quantiles: quantiles,
	}
}

func (a *quantilesAccum) Add(v float64) {
	a.values = append(a.values, v)
}

// Result returns the first quantile value. For multiple quantiles,
// use Results() which returns all quantile values.
func (a *quantilesAccum) Result() float64 {
	results := a.Results()
	if len(results) > 0 {
		return results[0]
	}
	return 0
}

// Results computes all requested quantile values.
func (a *quantilesAccum) Results() []float64 {
	if len(a.values) == 0 {
		results := make([]float64, len(a.quantiles))
		return results
	}

	sorted := make([]float64, len(a.values))
	copy(sorted, a.values)
	sort.Float64s(sorted)

	results := make([]float64, len(a.quantiles))
	for i, q := range a.quantiles {
		results[i] = quantileFromSorted(sorted, q)
	}
	return results
}

func (a *quantilesAccum) Reset() {
	a.values = a.values[:0]
}

// quantileFromSorted computes the q-th quantile from a sorted slice
// using linear interpolation (same method as Go's sort.Search approach).
func quantileFromSorted(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}

	idx := q * float64(len(sorted)-1)
	lower := int(idx)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
