package prw

import (
	"sync"
	"testing"
	"time"
)

func TestNewLimitsEnforcer(t *testing.T) {
	tests := []struct {
		name   string
		config LimitsConfig
		dryRun bool
	}{
		{
			name:   "empty config",
			config: LimitsConfig{},
			dryRun: false,
		},
		{
			name: "single rule with metric pattern",
			config: LimitsConfig{
				Rules: []LimitRule{
					{
						Name:                   "test_rule",
						MetricPattern:          "^test_.*",
						MaxDatapointsPerSecond: 1000,
					},
				},
			},
			dryRun: false,
		},
		{
			name: "rule with label patterns",
			config: LimitsConfig{
				Rules: []LimitRule{
					{
						Name:          "label_rule",
						MetricPattern: ".*",
						LabelPatterns: map[string]string{
							"job":      "^prod-.*",
							"instance": "^10\\..*",
						},
						MaxCardinality: 100,
					},
				},
			},
			dryRun: true,
		},
		{
			name: "invalid metric regex",
			config: LimitsConfig{
				Rules: []LimitRule{
					{
						Name:          "bad_rule",
						MetricPattern: "[invalid",
					},
				},
			},
			dryRun: false,
		},
		{
			name: "invalid label regex",
			config: LimitsConfig{
				Rules: []LimitRule{
					{
						Name: "bad_label_rule",
						LabelPatterns: map[string]string{
							"job": "[invalid",
						},
					},
				},
			},
			dryRun: false,
		},
		{
			name: "rule with explicit action",
			config: LimitsConfig{
				Rules: []LimitRule{
					{
						Name:   "log_rule",
						Action: LimitActionLog,
					},
				},
			},
			dryRun: false,
		},
		{
			name: "rule with default action",
			config: LimitsConfig{
				Rules: []LimitRule{
					{
						Name:          "drop_rule",
						MetricPattern: ".*",
					},
				},
			},
			dryRun: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enforcer := NewLimitsEnforcer(tt.config, tt.dryRun)
			if enforcer == nil {
				t.Fatal("expected non-nil enforcer")
			}
			if enforcer.dryRun != tt.dryRun {
				t.Errorf("dryRun = %v, want %v", enforcer.dryRun, tt.dryRun)
			}
			if enforcer.cardinalityMap == nil {
				t.Error("expected non-nil cardinalityMap")
			}
			if enforcer.datapointCounts == nil {
				t.Error("expected non-nil datapointCounts")
			}
		})
	}
}

func TestLimitsEnforcerProcess(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{}, false)
		result := enforcer.Process(nil)
		if result != nil {
			t.Error("expected nil result for nil request")
		}
	})

	t.Run("empty rules", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{}, false)
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
			},
		}
		result := enforcer.Process(req)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if len(result.Timeseries) != 1 {
			t.Errorf("expected 1 timeseries, got %d", len(result.Timeseries))
		}
	})

	t.Run("datapoints per second limit - drop", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:                   "rate_limit",
					MetricPattern:          "^test_.*",
					MaxDatapointsPerSecond: 2,
					Action:                 LimitActionDrop,
				},
			},
		}, false)

		// First request with 2 samples should pass
		req1 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{
						{Value: 1.0, Timestamp: 1234567890},
						{Value: 2.0, Timestamp: 1234567891},
					},
				},
			},
		}
		result1 := enforcer.Process(req1)
		if result1 == nil || len(result1.Timeseries) != 1 {
			t.Error("first request should pass")
		}

		// Second request should be dropped (exceeds limit)
		req2 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric2"}},
					Samples: []Sample{{Value: 3.0, Timestamp: 1234567892}},
				},
			},
		}
		result2 := enforcer.Process(req2)
		if result2 != nil {
			t.Error("second request should be dropped (nil result)")
		}
	})

	t.Run("datapoints per second limit - dry run", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:                   "rate_limit",
					MetricPattern:          "^test_.*",
					MaxDatapointsPerSecond: 1,
					Action:                 LimitActionDrop,
				},
			},
		}, true) // dry run enabled

		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{
						{Value: 1.0, Timestamp: 1234567890},
						{Value: 2.0, Timestamp: 1234567891},
					},
				},
			},
		}
		result := enforcer.Process(req)
		// In dry run mode, data should not be dropped
		if result == nil || len(result.Timeseries) != 1 {
			t.Error("dry run should not drop data")
		}
	})

	t.Run("cardinality limit - drop", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:           "cardinality_limit",
					MetricPattern:  "^test_.*",
					MaxCardinality: 2,
					Action:         LimitActionDrop,
				},
			},
		}, false)

		// First two unique series should pass
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "a"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "b"}},
					Samples: []Sample{{Value: 2.0, Timestamp: 1234567890}},
				},
				// Third unique series should be dropped
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "c"}},
					Samples: []Sample{{Value: 3.0, Timestamp: 1234567890}},
				},
			},
		}
		result := enforcer.Process(req)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if len(result.Timeseries) != 2 {
			t.Errorf("expected 2 timeseries (third dropped), got %d", len(result.Timeseries))
		}
	})

	t.Run("cardinality limit - same series not counted twice", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:           "cardinality_limit",
					MetricPattern:  "^test_.*",
					MaxCardinality: 2,
					Action:         LimitActionDrop,
				},
			},
		}, false)

		// Same series twice should only count once
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "a"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "a"}},
					Samples: []Sample{{Value: 2.0, Timestamp: 1234567891}},
				},
			},
		}
		result := enforcer.Process(req)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if len(result.Timeseries) != 2 {
			t.Errorf("expected 2 timeseries (same series not double counted), got %d", len(result.Timeseries))
		}
	})

	t.Run("log action does not drop", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:                   "log_only",
					MetricPattern:          "^test_.*",
					MaxDatapointsPerSecond: 1,
					Action:                 LimitActionLog,
				},
			},
		}, false)

		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{
						{Value: 1.0, Timestamp: 1234567890},
						{Value: 2.0, Timestamp: 1234567891},
					},
				},
			},
		}
		result := enforcer.Process(req)
		// Log action should not drop data
		if result == nil || len(result.Timeseries) != 1 {
			t.Error("log action should not drop data")
		}
	})

	t.Run("metric pattern non-match passes through", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:                   "strict_limit",
					MetricPattern:          "^prod_.*",
					MaxDatapointsPerSecond: 0, // would block all if matched
				},
			},
		}, false)

		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
			},
		}
		result := enforcer.Process(req)
		if result == nil || len(result.Timeseries) != 1 {
			t.Error("non-matching metrics should pass through")
		}
	})

	t.Run("label pattern matching", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:          "label_limit",
					MetricPattern: ".*",
					LabelPatterns: map[string]string{
						"job": "^prod-.*",
					},
					MaxDatapointsPerSecond: 2,
					Action:                 LimitActionDrop,
				},
			},
		}, false)

		// This should be limited (matches job pattern)
		req1 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{
						{Name: "__name__", Value: "test_metric"},
						{Name: "job", Value: "prod-api"},
					},
					Samples: []Sample{
						{Value: 1.0, Timestamp: 1234567890},
					},
				},
			},
		}
		result1 := enforcer.Process(req1)
		if result1 == nil || len(result1.Timeseries) != 1 {
			t.Error("first matching request should pass")
		}

		// Second matching request (within limit)
		req2 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{
						{Name: "__name__", Value: "test_metric2"},
						{Name: "job", Value: "prod-web"},
					},
					Samples: []Sample{{Value: 3.0, Timestamp: 1234567892}},
				},
			},
		}
		result2 := enforcer.Process(req2)
		if result2 == nil || len(result2.Timeseries) != 1 {
			t.Error("second matching request within limit should pass")
		}

		// Third matching request exceeds limit
		req3 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{
						{Name: "__name__", Value: "test_metric3"},
						{Name: "job", Value: "prod-db"},
					},
					Samples: []Sample{{Value: 4.0, Timestamp: 1234567893}},
				},
			},
		}
		result3 := enforcer.Process(req3)
		if result3 != nil {
			t.Error("third matching request should be dropped (exceeds limit)")
		}

		// Non-matching job should pass regardless
		req4 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{
						{Name: "__name__", Value: "test_metric4"},
						{Name: "job", Value: "staging-api"},
					},
					Samples: []Sample{{Value: 5.0, Timestamp: 1234567894}},
				},
			},
		}
		result4 := enforcer.Process(req4)
		if result4 == nil || len(result4.Timeseries) != 1 {
			t.Error("non-matching label pattern should pass")
		}
	})

	t.Run("counter reset after 1 second", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:                   "rate_limit",
					MetricPattern:          "^test_.*",
					MaxDatapointsPerSecond: 1,
					Action:                 LimitActionDrop,
				},
			},
		}, false)

		// First request uses up the limit
		req1 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
			},
		}
		enforcer.Process(req1)

		// Force reset by advancing lastReset
		enforcer.mu.Lock()
		enforcer.lastReset = time.Now().Add(-2 * time.Second)
		enforcer.mu.Unlock()

		// Second request should now pass after reset
		req2 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric2"}},
					Samples: []Sample{{Value: 2.0, Timestamp: 1234567891}},
				},
			},
		}
		result := enforcer.Process(req2)
		if result == nil || len(result.Timeseries) != 1 {
			t.Error("after counter reset, request should pass")
		}
	})

	t.Run("all timeseries dropped returns nil", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{
			Rules: []LimitRule{
				{
					Name:                   "rate_limit",
					MetricPattern:          "^test_.*",
					MaxDatapointsPerSecond: 1,
					Action:                 LimitActionDrop,
				},
			},
		}, false)

		// First request uses up the limit
		req1 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
			},
		}
		enforcer.Process(req1)

		// Second request should be fully dropped
		req2 := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric2"}},
					Samples: []Sample{{Value: 2.0, Timestamp: 1234567891}},
				},
			},
		}
		result := enforcer.Process(req2)
		if result != nil {
			t.Error("when all timeseries dropped, result should be nil")
		}
	})

	t.Run("metadata preserved", func(t *testing.T) {
		enforcer := NewLimitsEnforcer(LimitsConfig{}, false)
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
				},
			},
			Metadata: []MetricMetadata{
				{Type: MetricTypeCounter, Help: "test help", Unit: "bytes"},
			},
		}
		result := enforcer.Process(req)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if len(result.Metadata) != 1 {
			t.Errorf("expected metadata to be preserved, got %d", len(result.Metadata))
		}
	})
}

func TestLimitsEnforcerMatchesRule(t *testing.T) {
	tests := []struct {
		name     string
		rule     LimitRule
		ts       *TimeSeries
		expected bool
	}{
		{
			name: "no patterns matches nothing",
			rule: LimitRule{
				Name: "empty_rule",
			},
			ts: &TimeSeries{
				Labels: []Label{{Name: "__name__", Value: "test_metric"}},
			},
			expected: false,
		},
		{
			name: "metric pattern matches",
			rule: LimitRule{
				Name:          "metric_rule",
				MetricPattern: "^test_.*",
			},
			ts: &TimeSeries{
				Labels: []Label{{Name: "__name__", Value: "test_metric"}},
			},
			expected: true,
		},
		{
			name: "metric pattern does not match",
			rule: LimitRule{
				Name:          "metric_rule",
				MetricPattern: "^prod_.*",
			},
			ts: &TimeSeries{
				Labels: []Label{{Name: "__name__", Value: "test_metric"}},
			},
			expected: false,
		},
		{
			name: "label pattern matches",
			rule: LimitRule{
				Name: "label_rule",
				LabelPatterns: map[string]string{
					"job": "^prod-.*",
				},
			},
			ts: &TimeSeries{
				Labels: []Label{
					{Name: "__name__", Value: "test_metric"},
					{Name: "job", Value: "prod-api"},
				},
			},
			expected: true,
		},
		{
			name: "label pattern does not match",
			rule: LimitRule{
				Name: "label_rule",
				LabelPatterns: map[string]string{
					"job": "^prod-.*",
				},
			},
			ts: &TimeSeries{
				Labels: []Label{
					{Name: "__name__", Value: "test_metric"},
					{Name: "job", Value: "staging-api"},
				},
			},
			expected: false,
		},
		{
			name: "missing label does not match pattern",
			rule: LimitRule{
				Name: "label_rule",
				LabelPatterns: map[string]string{
					"environment": "^prod$",
				},
			},
			ts: &TimeSeries{
				Labels: []Label{{Name: "__name__", Value: "test_metric"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := LimitsConfig{
				Rules: []LimitRule{tt.rule},
			}
			enforcer := NewLimitsEnforcer(config, false)
			// Get the compiled rule
			compiledRule := &enforcer.config.Rules[0]
			result := enforcer.matchesRule(tt.ts, compiledRule)
			if result != tt.expected {
				t.Errorf("matchesRule() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestLimitsEnforcerStats(t *testing.T) {
	enforcer := NewLimitsEnforcer(LimitsConfig{
		Rules: []LimitRule{
			{
				Name:           "cardinality_limit",
				MetricPattern:  "^test_.*",
				MaxCardinality: 1,
				Action:         LimitActionDrop,
			},
		},
	}, false)

	// Initial stats should be zero
	dropped, violations := enforcer.Stats()
	if dropped != 0 || violations != 0 {
		t.Errorf("initial stats should be 0, got dropped=%d, violations=%d", dropped, violations)
	}

	// Process request that will cause violations
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "a"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
			},
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "b"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 1234567890}},
			},
		},
	}
	enforcer.Process(req)

	dropped, violations = enforcer.Stats()
	if dropped != 1 {
		t.Errorf("expected 1 dropped, got %d", dropped)
	}
	if violations != 1 {
		t.Errorf("expected 1 violation, got %d", violations)
	}
}

func TestLimitsEnforcerResetStats(t *testing.T) {
	enforcer := NewLimitsEnforcer(LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "rate_limit",
				MetricPattern:          "^test_.*",
				MaxDatapointsPerSecond: 1,
				Action:                 LimitActionDrop,
			},
		},
	}, false)

	// First request uses up the limit
	req1 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
			},
		},
	}
	enforcer.Process(req1)

	// Second request exceeds limit and gets dropped
	req2 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric2"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 1234567891}},
			},
		},
	}
	enforcer.Process(req2)

	// Verify we have stats
	dropped, violations := enforcer.Stats()
	if dropped == 0 || violations == 0 {
		t.Errorf("expected non-zero stats before reset, got dropped=%d, violations=%d", dropped, violations)
	}

	// Reset stats
	enforcer.ResetStats()

	// Verify stats are reset
	dropped, violations = enforcer.Stats()
	if dropped != 0 || violations != 0 {
		t.Errorf("expected zero stats after reset, got dropped=%d, violations=%d", dropped, violations)
	}
}

func TestLimitsEnforcerResetCardinality(t *testing.T) {
	enforcer := NewLimitsEnforcer(LimitsConfig{
		Rules: []LimitRule{
			{
				Name:           "cardinality_limit",
				MetricPattern:  "^test_.*",
				MaxCardinality: 2,
				Action:         LimitActionDrop,
			},
		},
	}, false)

	// Fill up cardinality
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "a"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1234567890}},
			},
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "b"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 1234567890}},
			},
		},
	}
	enforcer.Process(req)

	// Third unique series should be dropped
	req2 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "c"}},
				Samples: []Sample{{Value: 3.0, Timestamp: 1234567890}},
			},
		},
	}
	result := enforcer.Process(req2)
	if result != nil {
		t.Error("expected third series to be dropped before cardinality reset")
	}

	// Reset cardinality
	enforcer.ResetCardinality()

	// Now new series should be accepted
	req3 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "instance", Value: "d"}},
				Samples: []Sample{{Value: 4.0, Timestamp: 1234567890}},
			},
		},
	}
	result = enforcer.Process(req3)
	if result == nil || len(result.Timeseries) != 1 {
		t.Error("after cardinality reset, new series should be accepted")
	}
}

func TestLimitsEnforcerStop(t *testing.T) {
	enforcer := NewLimitsEnforcer(LimitsConfig{}, false)
	// Stop should not panic
	enforcer.Stop()
}

func TestLimitsEnforcerConcurrency(t *testing.T) {
	enforcer := NewLimitsEnforcer(LimitsConfig{
		Rules: []LimitRule{
			{
				Name:           "cardinality_limit",
				MetricPattern:  "^test_.*",
				MaxCardinality: 1000,
				Action:         LimitActionDrop,
			},
		},
	}, false)

	var wg sync.WaitGroup
	numGoroutines := 10
	requestsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				req := &WriteRequest{
					Timeseries: []TimeSeries{
						{
							Labels:  []Label{{Name: "__name__", Value: "test_metric"}, {Name: "id", Value: string(rune('a' + id))}},
							Samples: []Sample{{Value: float64(j), Timestamp: int64(1234567890 + j)}},
						},
					},
				}
				enforcer.Process(req)
			}
		}(i)
	}

	// Also test concurrent Stats/Reset calls
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			enforcer.Stats()
			if i%10 == 0 {
				enforcer.ResetStats()
			}
		}
	}()

	wg.Wait()
}

func TestLimitActions(t *testing.T) {
	if LimitActionDrop != "drop" {
		t.Errorf("expected LimitActionDrop = 'drop', got %s", LimitActionDrop)
	}
	if LimitActionLog != "log" {
		t.Errorf("expected LimitActionLog = 'log', got %s", LimitActionLog)
	}
}
