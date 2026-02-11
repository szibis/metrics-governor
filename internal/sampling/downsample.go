package sampling

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// Prometheus metrics for downsampling.
var (
	downsamplingInputTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_downsampling_input_total",
		Help: "Total datapoints ingested by downsampling",
	}, []string{"rule", "method"})

	downsamplingOutputTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_downsampling_output_total",
		Help: "Total datapoints emitted by downsampling",
	}, []string{"rule", "method"})

	downsamplingActiveSeries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_downsampling_active_series",
		Help: "Number of active series being downsampled",
	})
)

func init() {
	prometheus.MustRegister(downsamplingInputTotal)
	prometheus.MustRegister(downsamplingOutputTotal)
	prometheus.MustRegister(downsamplingActiveSeries)
}

// DownsampleMethod identifies a downsampling algorithm.
type DownsampleMethod string

const (
	// Aggregation-based: apply statistical function within a time window.
	DSAvg   DownsampleMethod = "avg"   // Mean value
	DSMin   DownsampleMethod = "min"   // Minimum value
	DSMax   DownsampleMethod = "max"   // Maximum value
	DSSum   DownsampleMethod = "sum"   // Sum of values
	DSCount DownsampleMethod = "count" // Number of datapoints
	DSLast  DownsampleMethod = "last"  // Last value (gauges)
	DSDelta DownsampleMethod = "delta" // End - start (counters)

	// Shape-preserving: Largest-Triangle-Three-Buckets.
	// Preserves visual characteristics, spikes, and trends.
	DSLTTB DownsampleMethod = "lttb"

	// Compression: Swinging Door Trending.
	// Discards points within a deadband; ideal for stable signals.
	DSSDT DownsampleMethod = "sdt"

	// Adaptive: adjusts keep rate based on data variance.
	// More points kept during volatile periods, fewer during stable periods.
	DSAdaptive DownsampleMethod = "adaptive"
)

// validMethods is the set of supported downsample methods.
var validMethods = map[DownsampleMethod]bool{
	DSAvg: true, DSMin: true, DSMax: true, DSSum: true,
	DSCount: true, DSLast: true, DSDelta: true,
	DSLTTB: true, DSSDT: true, DSAdaptive: true,
}

// DownsampleConfig holds parameters for a downsample rule.
type DownsampleConfig struct {
	Method         DownsampleMethod `yaml:"method"`
	Window         string           `yaml:"window"`          // Duration string (e.g. "1m", "5m")
	Resolution     int              `yaml:"resolution"`      // LTTB: target points per window
	Deviation      float64          `yaml:"deviation"`       // SDT: deadband threshold
	MinRate        float64          `yaml:"min_rate"`        // Adaptive: minimum keep rate (0.0-1.0)
	MaxRate        float64          `yaml:"max_rate"`        // Adaptive: maximum keep rate (0.0-1.0)
	VarianceWindow int              `yaml:"variance_window"` // Adaptive: samples for variance calc

	parsedWindow time.Duration
}

// validate checks the config and parses the window duration.
func (dc *DownsampleConfig) validate() error {
	if !validMethods[dc.Method] {
		return fmt.Errorf("unknown downsample method %q", dc.Method)
	}

	// Parse window for methods that need it.
	switch dc.Method {
	case DSAvg, DSMin, DSMax, DSSum, DSCount, DSLast, DSDelta, DSLTTB, DSAdaptive:
		if dc.Window == "" {
			return fmt.Errorf("downsample method %q requires a window", dc.Method)
		}
		d, err := time.ParseDuration(dc.Window)
		if err != nil {
			return fmt.Errorf("invalid window %q: %w", dc.Window, err)
		}
		if d <= 0 {
			return fmt.Errorf("window must be positive, got %v", d)
		}
		dc.parsedWindow = d
	case DSSDT:
		if dc.Deviation <= 0 {
			return fmt.Errorf("SDT requires deviation > 0, got %f", dc.Deviation)
		}
	}

	// Method-specific validation.
	if dc.Method == DSLTTB && dc.Resolution <= 0 {
		dc.Resolution = 10 // default: 10 points per window
	}
	if dc.Method == DSAdaptive {
		if dc.MinRate <= 0 {
			dc.MinRate = 0.1
		}
		if dc.MaxRate <= 0 || dc.MaxRate > 1.0 {
			dc.MaxRate = 1.0
		}
		if dc.MinRate > dc.MaxRate {
			dc.MinRate = dc.MaxRate
		}
		if dc.VarianceWindow <= 0 {
			dc.VarianceWindow = 20
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Series Processor Interface
// ---------------------------------------------------------------------------

// emittedPoint is a single downsampled output value.
type emittedPoint struct {
	timestamp uint64
	value     float64
}

// seriesProcessor handles downsampling for a single time series.
type seriesProcessor interface {
	// ingest processes a new datapoint; returns values ready to emit.
	ingest(ts uint64, value float64) []emittedPoint
	// flush returns all accumulated but not-yet-emitted data.
	flush() []emittedPoint
}

// ---------------------------------------------------------------------------
// Aggregation Processor (avg, min, max, sum, count, last, delta)
// ---------------------------------------------------------------------------

type aggProcessor struct {
	method   DownsampleMethod
	windowNs uint64 // window duration in nanoseconds

	count     int64
	sum       float64
	min       float64
	max       float64
	last      float64
	first     float64
	windowEnd uint64
	hasData   bool
}

func newAggProcessor(method DownsampleMethod, window time.Duration) *aggProcessor {
	return &aggProcessor{
		method:   method,
		windowNs: uint64(window),
	}
}

func (a *aggProcessor) ingest(ts uint64, value float64) []emittedPoint {
	windowStart := (ts / a.windowNs) * a.windowNs
	windowEnd := windowStart + a.windowNs

	var emitted []emittedPoint

	if a.hasData && windowEnd != a.windowEnd {
		// Window boundary crossed — emit aggregated value from previous window.
		emitted = append(emitted, emittedPoint{
			timestamp: a.windowEnd - 1, // end of completed window
			value:     a.aggregate(),
		})
		a.reset()
	}

	if !a.hasData {
		a.windowEnd = windowEnd
		a.first = value
		a.min = value
		a.max = value
		a.hasData = true
	}

	a.count++
	a.sum += value
	if value < a.min {
		a.min = value
	}
	if value > a.max {
		a.max = value
	}
	a.last = value

	return emitted
}

func (a *aggProcessor) aggregate() float64 {
	switch a.method {
	case DSAvg:
		if a.count == 0 {
			return 0
		}
		return a.sum / float64(a.count)
	case DSMin:
		return a.min
	case DSMax:
		return a.max
	case DSSum:
		return a.sum
	case DSCount:
		return float64(a.count)
	case DSLast:
		return a.last
	case DSDelta:
		return a.last - a.first
	default:
		return a.last
	}
}

func (a *aggProcessor) flush() []emittedPoint {
	if !a.hasData {
		return nil
	}
	ep := emittedPoint{
		timestamp: a.windowEnd - 1,
		value:     a.aggregate(),
	}
	a.reset()
	return []emittedPoint{ep}
}

func (a *aggProcessor) reset() {
	a.count = 0
	a.sum = 0
	a.min = 0
	a.max = 0
	a.last = 0
	a.first = 0
	a.hasData = false
}

// ---------------------------------------------------------------------------
// LTTB Processor (Largest-Triangle-Three-Buckets)
//
// Streaming variant: divides time into fixed-duration buckets
// (window / resolution). For each completed bucket, selects the point
// that maximizes the triangle area with the previous selected point
// and the bucket average — preserving spikes, trends, and visual shape.
// ---------------------------------------------------------------------------

type lttbProcessor struct {
	bucketDur uint64 // nanoseconds per bucket

	prevSelected  *emittedPoint  // last emitted representative
	currentBucket []emittedPoint // points accumulating in current bucket
	currentStart  uint64         // bucket-aligned start timestamp
	initialized   bool
}

func newLTTBProcessor(window time.Duration, resolution int) *lttbProcessor {
	if resolution <= 0 {
		resolution = 10
	}
	bucketDur := uint64(window) / uint64(resolution)
	if bucketDur == 0 {
		bucketDur = uint64(time.Second)
	}
	return &lttbProcessor{bucketDur: bucketDur}
}

func (l *lttbProcessor) ingest(ts uint64, value float64) []emittedPoint {
	point := emittedPoint{timestamp: ts, value: value}
	bucketStart := (ts / l.bucketDur) * l.bucketDur

	if !l.initialized {
		l.currentStart = bucketStart
		l.currentBucket = []emittedPoint{point}
		l.initialized = true
		return nil
	}

	if bucketStart == l.currentStart {
		l.currentBucket = append(l.currentBucket, point)
		return nil
	}

	// Bucket transition — select the best representative from completed bucket.
	selected := l.selectBest()
	var emitted []emittedPoint

	if l.prevSelected != nil {
		// Emit previous selection (it's now confirmed by the next bucket).
		emitted = append(emitted, *l.prevSelected)
	}

	l.prevSelected = &selected
	l.currentBucket = []emittedPoint{point}
	l.currentStart = bucketStart

	return emitted
}

// selectBest picks the point in the current bucket that maximizes the
// triangle area with prevSelected (left vertex) and the bucket average
// (right vertex). Without a previous selection, the first point is used.
func (l *lttbProcessor) selectBest() emittedPoint {
	if len(l.currentBucket) == 1 {
		return l.currentBucket[0]
	}

	// Bucket average (used as right reference in triangle, or as center for first bucket).
	avgTs := float64(0)
	avgVal := float64(0)
	for _, p := range l.currentBucket {
		avgTs += float64(p.timestamp)
		avgVal += p.value
	}
	n := float64(len(l.currentBucket))
	avgTs /= n
	avgVal /= n

	if l.prevSelected == nil {
		// No left reference yet — select the most extreme point
		// (largest absolute deviation from bucket average).
		bestDev := -1.0
		bestIdx := 0
		for i, p := range l.currentBucket {
			dev := math.Abs(p.value - avgVal)
			if dev > bestDev {
				bestDev = dev
				bestIdx = i
			}
		}
		return l.currentBucket[bestIdx]
	}

	// Standard LTTB: maximize triangle area with prevSelected (left) and
	// bucket average (right).
	bestArea := -1.0
	bestIdx := 0
	prev := l.prevSelected

	for i, p := range l.currentBucket {
		area := math.Abs(
			float64(prev.timestamp)*(p.value-avgVal)+
				float64(p.timestamp)*(avgVal-prev.value)+
				avgTs*(prev.value-p.value)) * 0.5
		if area > bestArea {
			bestArea = area
			bestIdx = i
		}
	}
	return l.currentBucket[bestIdx]
}

func (l *lttbProcessor) flush() []emittedPoint {
	var result []emittedPoint
	if l.prevSelected != nil {
		result = append(result, *l.prevSelected)
		l.prevSelected = nil
	}
	if len(l.currentBucket) > 0 {
		selected := l.selectBest()
		result = append(result, selected)
		l.currentBucket = nil
	}
	l.initialized = false
	return result
}

// ---------------------------------------------------------------------------
// SDT Processor (Swinging Door Trending)
//
// Maintains a compression corridor from the last emitted point.
// A new point is emitted only when a candidate falls outside the corridor
// (deadband), making it ideal for compressing near-constant signals.
// ---------------------------------------------------------------------------

type sdtProcessor struct {
	deviation float64

	lastEmitted *emittedPoint
	lastPoint   *emittedPoint
	upperSlope  float64
	lowerSlope  float64
	doorOpen    bool
}

func newSDTProcessor(deviation float64) *sdtProcessor {
	if deviation <= 0 {
		deviation = 1.0
	}
	return &sdtProcessor{deviation: deviation}
}

func (s *sdtProcessor) ingest(ts uint64, value float64) []emittedPoint {
	point := emittedPoint{timestamp: ts, value: value}

	if s.lastEmitted == nil {
		// Always emit the first point.
		s.lastEmitted = &point
		s.lastPoint = &point
		return []emittedPoint{point}
	}

	if ts <= s.lastEmitted.timestamp {
		return nil // out-of-order or duplicate
	}

	dt := float64(ts - s.lastEmitted.timestamp)

	if !s.doorOpen {
		// Initialize corridor from last emitted point.
		s.upperSlope = (value - s.lastEmitted.value + s.deviation) / dt
		s.lowerSlope = (value - s.lastEmitted.value - s.deviation) / dt
		s.doorOpen = true
		s.lastPoint = &point
		return nil
	}

	// Narrow the corridor.
	if newUpper := (value - s.lastEmitted.value + s.deviation) / dt; newUpper < s.upperSlope {
		s.upperSlope = newUpper
	}
	if newLower := (value - s.lastEmitted.value - s.deviation) / dt; newLower > s.lowerSlope {
		s.lowerSlope = newLower
	}

	// Corridor collapsed → value moved outside the deadband.
	if s.upperSlope < s.lowerSlope {
		emitted := *s.lastPoint
		s.lastEmitted = s.lastPoint
		s.doorOpen = false

		// Re-evaluate current point against new reference.
		if dt2 := float64(ts - s.lastEmitted.timestamp); dt2 > 0 {
			s.upperSlope = (value - s.lastEmitted.value + s.deviation) / dt2
			s.lowerSlope = (value - s.lastEmitted.value - s.deviation) / dt2
			s.doorOpen = true
		}
		s.lastPoint = &point
		return []emittedPoint{emitted}
	}

	s.lastPoint = &point
	return nil
}

func (s *sdtProcessor) flush() []emittedPoint {
	if s.lastPoint != nil && s.lastEmitted != nil &&
		s.lastPoint.timestamp != s.lastEmitted.timestamp {
		return []emittedPoint{*s.lastPoint}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Adaptive Processor
//
// Tracks the rolling coefficient of variation (CV = σ/μ) over a sliding
// window of recent values. CV is mapped linearly to a keep rate:
//
//	CV ≈ 0 (stable) → minRate   (discard most)
//	CV ≥ 1 (volatile) → maxRate (keep most)
//
// Selection is deterministic (1-in-N) so results are reproducible.
// ---------------------------------------------------------------------------

type adaptiveProcessor struct {
	minRate        float64
	maxRate        float64
	varianceWindow int

	values   []float64
	valIdx   int
	valFull  bool
	keepRate float64
	ops      int64
}

func newAdaptiveProcessor(minRate, maxRate float64, varianceWindow int) *adaptiveProcessor {
	if minRate <= 0 {
		minRate = 0.1
	}
	if maxRate <= 0 || maxRate > 1.0 {
		maxRate = 1.0
	}
	if minRate > maxRate {
		minRate = maxRate
	}
	if varianceWindow <= 0 {
		varianceWindow = 20
	}
	return &adaptiveProcessor{
		minRate:        minRate,
		maxRate:        maxRate,
		varianceWindow: varianceWindow,
		values:         make([]float64, varianceWindow),
		keepRate:       maxRate, // start conservative (keep everything)
	}
}

func (a *adaptiveProcessor) ingest(ts uint64, value float64) []emittedPoint {
	// Update rolling buffer.
	a.values[a.valIdx] = value
	a.valIdx = (a.valIdx + 1) % a.varianceWindow
	if a.valIdx == 0 {
		a.valFull = true
	}

	a.adjustRate()

	// Deterministic 1-in-N.
	a.ops++
	n := int64(1.0 / a.keepRate)
	if n <= 0 {
		n = 1
	}
	if a.ops%n == 0 {
		return []emittedPoint{{timestamp: ts, value: value}}
	}
	return nil
}

func (a *adaptiveProcessor) adjustRate() {
	count := a.varianceWindow
	if !a.valFull {
		count = a.valIdx
	}
	if count < 2 {
		a.keepRate = a.maxRate
		return
	}

	mean := 0.0
	for i := 0; i < count; i++ {
		mean += a.values[i]
	}
	mean /= float64(count)

	variance := 0.0
	for i := 0; i < count; i++ {
		d := a.values[i] - mean
		variance += d * d
	}
	variance /= float64(count)

	cv := 0.0
	if mean != 0 {
		cv = math.Sqrt(variance) / math.Abs(mean)
	}

	if cv >= 1.0 {
		a.keepRate = a.maxRate
	} else {
		a.keepRate = a.minRate + cv*(a.maxRate-a.minRate)
	}
}

func (a *adaptiveProcessor) flush() []emittedPoint {
	return nil // stateless pass-through; nothing to flush
}

// KeepRate returns the current adaptive keep rate (for testing).
func (a *adaptiveProcessor) keepRateValue() float64 { return a.keepRate }

// ---------------------------------------------------------------------------
// Downsample Engine
//
// Manages per-series processors with sharded locking for high concurrency.
// Uses dsShardCount independent shards to reduce mutex contention — each
// series is routed to a shard via FNV-1a hash of its key.
// Periodically garbage-collects stale series.
// ---------------------------------------------------------------------------

const dsShardCount = 32

type downsampleShard struct {
	mu       sync.Mutex
	series   map[string]seriesProcessor
	lastSeen map[string]time.Time
}

type downsampleEngine struct {
	shards     [dsShardCount]downsampleShard
	cleanupCtr atomic.Int64
}

func newDownsampleEngine() *downsampleEngine {
	de := &downsampleEngine{}
	for i := range de.shards {
		de.shards[i].series = make(map[string]seriesProcessor)
		de.shards[i].lastSeen = make(map[string]time.Time)
	}
	return de
}

// fnvShard computes an inline FNV-1a hash and returns the shard index.
func fnvShard(key string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h & (dsShardCount - 1) // bitmask (power-of-2 shard count)
}

func (de *downsampleEngine) ingestAndEmit(seriesKey string, cfg *DownsampleConfig, ts uint64, value float64) []emittedPoint {
	shard := &de.shards[fnvShard(seriesKey)]

	shard.mu.Lock()
	proc, ok := shard.series[seriesKey]
	if !ok {
		proc = createProcessor(cfg)
		shard.series[seriesKey] = proc
	}
	shard.lastSeen[seriesKey] = time.Now()
	result := proc.ingest(ts, value)
	shard.mu.Unlock()

	// Periodic cleanup every 10 000 ingestions (atomic, lock-free check).
	if de.cleanupCtr.Add(1)%10000 == 0 {
		de.cleanupAll(10 * time.Minute)
	}

	return result
}

// flushAll emits remaining accumulated data from every series processor.
func (de *downsampleEngine) flushAll() map[string][]emittedPoint {
	result := make(map[string][]emittedPoint)
	for i := range de.shards {
		shard := &de.shards[i]
		shard.mu.Lock()
		for key, proc := range shard.series {
			if pts := proc.flush(); len(pts) > 0 {
				result[key] = pts
			}
		}
		shard.series = make(map[string]seriesProcessor)
		shard.lastSeen = make(map[string]time.Time)
		shard.mu.Unlock()
	}
	return result
}

// seriesCount returns the current number of tracked series.
func (de *downsampleEngine) seriesCount() int {
	total := 0
	for i := range de.shards {
		shard := &de.shards[i]
		shard.mu.Lock()
		total += len(shard.series)
		shard.mu.Unlock()
	}
	return total
}

func (de *downsampleEngine) cleanupAll(staleDuration time.Duration) {
	now := time.Now()
	totalSeries := 0
	for i := range de.shards {
		shard := &de.shards[i]
		shard.mu.Lock()
		for key, lastSeen := range shard.lastSeen {
			if now.Sub(lastSeen) > staleDuration {
				delete(shard.series, key)
				delete(shard.lastSeen, key)
			}
		}
		totalSeries += len(shard.series)
		shard.mu.Unlock()
	}
	downsamplingActiveSeries.Set(float64(totalSeries))
}

// ---------------------------------------------------------------------------
// Processor Factory
// ---------------------------------------------------------------------------

func createProcessor(cfg *DownsampleConfig) seriesProcessor {
	switch cfg.Method {
	case DSAvg, DSMin, DSMax, DSSum, DSCount, DSLast, DSDelta:
		return newAggProcessor(cfg.Method, cfg.parsedWindow)
	case DSLTTB:
		return newLTTBProcessor(cfg.parsedWindow, cfg.Resolution)
	case DSSDT:
		return newSDTProcessor(cfg.Deviation)
	case DSAdaptive:
		return newAdaptiveProcessor(cfg.MinRate, cfg.MaxRate, cfg.VarianceWindow)
	default:
		return newAggProcessor(DSLast, cfg.parsedWindow)
	}
}

// ---------------------------------------------------------------------------
// OTLP Helper Functions
// ---------------------------------------------------------------------------

// buildDSSeriesKey creates a unique key from metric name + sorted attributes.
func buildDSSeriesKey(metricName string, attrs []*commonpb.KeyValue) string {
	if len(attrs) == 0 {
		return metricName
	}

	sorted := make([]*commonpb.KeyValue, len(attrs))
	copy(sorted, attrs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Key < sorted[j].Key
	})

	// Pre-size: metricName + each label contributes "|" + key + "=" + value.
	size := len(metricName)
	for _, kv := range sorted {
		size += 1 + len(kv.Key) + 1 + len(kv.Value.GetStringValue())
	}

	var b strings.Builder
	b.Grow(size)
	b.WriteString(metricName)
	for _, kv := range sorted {
		b.WriteByte('|')
		b.WriteString(kv.Key)
		b.WriteByte('=')
		b.WriteString(kv.Value.GetStringValue())
	}
	return b.String()
}

// getNumberValue extracts the float64 value from a NumberDataPoint.
func getNumberValue(dp *metricspb.NumberDataPoint) float64 {
	switch v := dp.Value.(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	default:
		return 0
	}
}

// cloneDatapointWithValue creates a new NumberDataPoint preserving
// attributes/flags but with a new timestamp and value.
func cloneDatapointWithValue(template *metricspb.NumberDataPoint, ts uint64, value float64) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		Attributes:        template.Attributes,
		StartTimeUnixNano: template.StartTimeUnixNano,
		TimeUnixNano:      ts,
		Value:             &metricspb.NumberDataPoint_AsDouble{AsDouble: value},
		Flags:             template.Flags,
	}
}
