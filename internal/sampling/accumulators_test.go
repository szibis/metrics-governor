package sampling

import (
	"math"
	"testing"
)

func TestSumAccum(t *testing.T) {
	a := &sumAccum{}
	a.Add(1)
	a.Add(2)
	a.Add(3)
	if got := a.Result(); got != 6 {
		t.Errorf("sum = %f, want 6", got)
	}
	a.Reset()
	if got := a.Result(); got != 0 {
		t.Errorf("after reset = %f, want 0", got)
	}
}

func TestAvgAccum(t *testing.T) {
	a := &avgAccum{}
	a.Add(2)
	a.Add(4)
	a.Add(6)
	if got := a.Result(); got != 4 {
		t.Errorf("avg = %f, want 4", got)
	}
}

func TestAvgAccum_Empty(t *testing.T) {
	a := &avgAccum{}
	if got := a.Result(); got != 0 {
		t.Errorf("avg(empty) = %f, want 0", got)
	}
}

func TestMinAccum(t *testing.T) {
	a := &minAccum{}
	a.Add(5)
	a.Add(2)
	a.Add(8)
	if got := a.Result(); got != 2 {
		t.Errorf("min = %f, want 2", got)
	}
}

func TestMinAccum_Empty(t *testing.T) {
	a := &minAccum{}
	if got := a.Result(); got != 0 {
		t.Errorf("min(empty) = %f, want 0", got)
	}
}

func TestMaxAccum(t *testing.T) {
	a := &maxAccum{}
	a.Add(5)
	a.Add(2)
	a.Add(8)
	if got := a.Result(); got != 8 {
		t.Errorf("max = %f, want 8", got)
	}
}

func TestCountAccum(t *testing.T) {
	a := &countAccum{}
	a.Add(1)
	a.Add(2)
	a.Add(3)
	if got := a.Result(); got != 3 {
		t.Errorf("count = %f, want 3", got)
	}
}

func TestLastAccum(t *testing.T) {
	a := &lastAccum{}
	a.Add(1)
	a.Add(5)
	a.Add(3)
	if got := a.Result(); got != 3 {
		t.Errorf("last = %f, want 3", got)
	}
}

func TestIncreaseAccum(t *testing.T) {
	a := &increaseAccum{}
	a.Add(10)
	a.Add(15)
	a.Add(20)
	if got := a.Result(); got != 10 {
		t.Errorf("increase = %f, want 10 (20-10)", got)
	}
}

func TestIncreaseAccum_Empty(t *testing.T) {
	a := &increaseAccum{}
	if got := a.Result(); got != 0 {
		t.Errorf("increase(empty) = %f, want 0", got)
	}
}

func TestRateAccum(t *testing.T) {
	a := &rateAccum{}
	a.setWindow(60) // 60 seconds
	a.Add(100)
	a.Add(160)
	// increase = 60, window = 60s, rate = 1.0/s
	if got := a.Result(); got != 1.0 {
		t.Errorf("rate = %f, want 1.0", got)
	}
}

func TestRateAccum_ZeroWindow(t *testing.T) {
	a := &rateAccum{}
	a.setWindow(0)
	a.Add(100)
	a.Add(200)
	if got := a.Result(); got != 0 {
		t.Errorf("rate(zero window) = %f, want 0", got)
	}
}

func TestStddevAccum(t *testing.T) {
	a := &stddevAccum{}
	// Values: 2, 4, 4, 4, 5, 5, 7, 9
	// Mean = 5, Population stddev = 2.0
	vals := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	for _, v := range vals {
		a.Add(v)
	}
	got := a.Result()
	if math.Abs(got-2.0) > 0.01 {
		t.Errorf("stddev = %f, want ~2.0", got)
	}
}

func TestStddevAccum_SingleValue(t *testing.T) {
	a := &stddevAccum{}
	a.Add(5)
	if got := a.Result(); got != 0 {
		t.Errorf("stddev(single) = %f, want 0", got)
	}
}

func TestStdvarAccum(t *testing.T) {
	a := &stdvarAccum{}
	vals := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	for _, v := range vals {
		a.Add(v)
	}
	got := a.Result()
	if math.Abs(got-4.0) > 0.01 {
		t.Errorf("stdvar = %f, want ~4.0", got)
	}
}

func TestQuantilesAccum(t *testing.T) {
	a := newQuantilesAccum([]float64{0.5, 0.9, 0.99})

	// Add 100 values: 1..100
	for i := 1; i <= 100; i++ {
		a.Add(float64(i))
	}

	results := a.Results()
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// P50 should be ~50.5
	if math.Abs(results[0]-50.5) > 1 {
		t.Errorf("p50 = %f, want ~50.5", results[0])
	}

	// P90 should be ~90.1
	if math.Abs(results[1]-90.1) > 1 {
		t.Errorf("p90 = %f, want ~90.1", results[1])
	}

	// P99 should be ~99.01
	if math.Abs(results[2]-99.01) > 1 {
		t.Errorf("p99 = %f, want ~99.01", results[2])
	}
}

func TestQuantilesAccum_Empty(t *testing.T) {
	a := newQuantilesAccum([]float64{0.5})
	results := a.Results()
	if results[0] != 0 {
		t.Errorf("p50(empty) = %f, want 0", results[0])
	}
}

func TestQuantilesAccum_SingleValue(t *testing.T) {
	a := newQuantilesAccum([]float64{0.5, 0.99})
	a.Add(42)
	results := a.Results()
	if results[0] != 42 || results[1] != 42 {
		t.Errorf("quantiles(single) = %v, want [42, 42]", results)
	}
}

func TestQuantilesAccum_Reset(t *testing.T) {
	a := newQuantilesAccum([]float64{0.5})
	a.Add(1)
	a.Add(2)
	a.Reset()
	a.Add(100)
	results := a.Results()
	if results[0] != 100 {
		t.Errorf("after reset, p50 = %f, want 100", results[0])
	}
}

func TestNewAccumulator(t *testing.T) {
	tests := []struct {
		fn   AggFunc
		kind string
	}{
		{AggSum, "*sampling.sumAccum"},
		{AggAvg, "*sampling.avgAccum"},
		{AggMin, "*sampling.minAccum"},
		{AggMax, "*sampling.maxAccum"},
		{AggCount, "*sampling.countAccum"},
		{AggLast, "*sampling.lastAccum"},
		{AggIncrease, "*sampling.increaseAccum"},
		{AggRate, "*sampling.rateAccum"},
		{AggStddev, "*sampling.stddevAccum"},
		{AggStdvar, "*sampling.stdvarAccum"},
	}
	for _, tt := range tests {
		t.Run(string(tt.fn), func(t *testing.T) {
			a := newAccumulator(tt.fn)
			if a == nil {
				t.Fatal("newAccumulator returned nil")
			}
		})
	}
}

func TestQuantileFromSorted(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5}
	tests := []struct {
		q    float64
		want float64
	}{
		{0.0, 1},
		{0.25, 2},
		{0.5, 3},
		{0.75, 4},
		{1.0, 5},
	}
	for _, tt := range tests {
		got := quantileFromSorted(sorted, tt.q)
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("quantile(%f) = %f, want %f", tt.q, got, tt.want)
		}
	}
}
