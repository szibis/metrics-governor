package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// VictoriaMetrics query response structures
type VMQueryResponse struct {
	Status string `json:"status"`
	Data   VMData `json:"data"`
}

type VMData struct {
	ResultType string     `json:"resultType"`
	Result     []VMResult `json:"result"`
}

type VMResult struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`  // [timestamp, value]
	Values [][]interface{}   `json:"values"` // for range queries
}

type VerificationResult struct {
	Timestamp          time.Time
	TotalMetrics       int
	TotalDatapoints    int64
	UniqueTimeSeries   int
	VerificationMatch  bool
	ExpectedCount      int64
	ActualCount        int64
	MissingPercentage  float64
	HighCardinalityTS  int
	QueuedItems        int
	DroppedItems       int64
	ExportErrors       int64
}

func main() {
	vmEndpoint := getEnv("VM_ENDPOINT", "http://localhost:8428")
	mgEndpoint := getEnv("MG_ENDPOINT", "http://localhost:9090")
	checkInterval := getEnvDuration("CHECK_INTERVAL", 30*time.Second)
	verificationMetric := getEnv("VERIFICATION_METRIC", "generator_verification_counter")

	log.Printf("========================================")
	log.Printf("  DATA VERIFICATION TOOL")
	log.Printf("========================================")
	log.Printf("VictoriaMetrics: %s", vmEndpoint)
	log.Printf("Metrics Governor: %s", mgEndpoint)
	log.Printf("Check Interval: %s", checkInterval)
	log.Printf("========================================")

	// Wait for services to be ready
	log.Println("Waiting for services to be ready...")
	time.Sleep(10 * time.Second)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		result := verify(vmEndpoint, mgEndpoint, verificationMetric)
		printResult(result)
		<-ticker.C
	}
}

func verify(vmEndpoint, mgEndpoint, verificationMetric string) VerificationResult {
	result := VerificationResult{
		Timestamp: time.Now(),
	}

	// Query VictoriaMetrics for total metrics
	totalMetrics, err := queryVMScalar(vmEndpoint, "count({__name__=~\".+\"})")
	if err != nil {
		log.Printf("Error querying total metrics: %v", err)
	} else {
		result.TotalMetrics = int(totalMetrics)
	}

	// Query VictoriaMetrics for unique time series
	uniqueTS, err := queryVMScalar(vmEndpoint, "count(count by (__name__)({__name__=~\".+\"}))")
	if err != nil {
		log.Printf("Error querying unique time series: %v", err)
	} else {
		result.UniqueTimeSeries = int(uniqueTS)
	}

	// Query verification counter from VictoriaMetrics
	actualCount, err := queryVMScalar(vmEndpoint, fmt.Sprintf("max(%s)", verificationMetric))
	if err != nil {
		log.Printf("Error querying verification counter: %v", err)
	} else {
		result.ActualCount = int64(actualCount)
	}

	// Query high cardinality metrics count
	highCardTS, err := queryVMScalar(vmEndpoint, "count(high_cardinality_metric)")
	if err != nil {
		log.Printf("Error querying high cardinality metrics: %v", err)
	} else {
		result.HighCardinalityTS = int(highCardTS)
	}

	// Query metrics-governor stats
	mgStats, err := queryMGStats(mgEndpoint)
	if err != nil {
		log.Printf("Error querying metrics-governor stats: %v", err)
	} else {
		result.ExpectedCount = mgStats.DatapointsSent
		result.QueuedItems = mgStats.QueueSize
		result.DroppedItems = mgStats.DroppedTotal
		result.ExportErrors = mgStats.ExportErrors
	}

	// Calculate verification match
	if result.ExpectedCount > 0 && result.ActualCount > 0 {
		result.MissingPercentage = float64(result.ExpectedCount-result.ActualCount) / float64(result.ExpectedCount) * 100
		// Allow 5% tolerance for timing differences
		result.VerificationMatch = result.MissingPercentage < 5.0
	}

	return result
}

func queryVMScalar(endpoint, query string) (float64, error) {
	u, err := url.Parse(endpoint + "/api/v1/query")
	if err != nil {
		return 0, err
	}

	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var vmResp VMQueryResponse
	if err := json.Unmarshal(body, &vmResp); err != nil {
		return 0, err
	}

	if vmResp.Status != "success" {
		return 0, fmt.Errorf("query failed: %s", vmResp.Status)
	}

	if len(vmResp.Data.Result) == 0 {
		return 0, nil
	}

	if len(vmResp.Data.Result[0].Value) >= 2 {
		valStr, ok := vmResp.Data.Result[0].Value[1].(string)
		if ok {
			return strconv.ParseFloat(valStr, 64)
		}
	}

	return 0, fmt.Errorf("unexpected response format")
}

type MGStats struct {
	DatapointsSent int64
	QueueSize      int
	DroppedTotal   int64
	ExportErrors   int64
}

func queryMGStats(endpoint string) (MGStats, error) {
	stats := MGStats{}

	// Query Prometheus metrics from metrics-governor
	resp, err := http.Get(endpoint + "/metrics")
	if err != nil {
		return stats, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}

	// Parse Prometheus text format (simplified)
	lines := string(body)

	// Extract metrics_governor_datapoints_total
	stats.DatapointsSent = extractMetricValue(lines, "metrics_governor_datapoints_total")
	stats.QueueSize = int(extractMetricValue(lines, "metrics_governor_queue_size"))
	stats.DroppedTotal = extractMetricValue(lines, "metrics_governor_queue_dropped_total")
	stats.ExportErrors = extractMetricValue(lines, "metrics_governor_export_errors_total")

	return stats, nil
}

func extractMetricValue(metricsText, metricName string) int64 {
	// Parse Prometheus text format line by line
	lines := strings.Split(metricsText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if line starts with metric name
		if strings.HasPrefix(line, metricName) {
			// Handle metrics with labels: metric_name{labels} value
			// or simple metrics: metric_name value
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				// Last field should be the value
				valueStr := parts[len(parts)-1]
				val, err := strconv.ParseFloat(valueStr, 64)
				if err == nil {
					return int64(val)
				}
			}
		}
	}
	return 0
}

func printResult(result VerificationResult) {
	status := "✅ PASS"
	if !result.VerificationMatch && result.ExpectedCount > 0 {
		status = "❌ FAIL"
	}

	log.Printf("========================================")
	log.Printf("  VERIFICATION RESULT - %s", status)
	log.Printf("========================================")
	log.Printf("Timestamp: %s", result.Timestamp.Format(time.RFC3339))
	log.Printf("")
	log.Printf("VICTORIAMETRICS:")
	log.Printf("  Total metrics:          %d", result.TotalMetrics)
	log.Printf("  Unique time series:     %d", result.UniqueTimeSeries)
	log.Printf("  High cardinality TS:    %d", result.HighCardinalityTS)
	log.Printf("  Verification counter:   %d", result.ActualCount)
	log.Printf("")
	log.Printf("METRICS-GOVERNOR:")
	log.Printf("  Expected datapoints:    %d", result.ExpectedCount)
	log.Printf("  Queue size:             %d", result.QueuedItems)
	log.Printf("  Dropped total:          %d", result.DroppedItems)
	log.Printf("  Export errors:          %d", result.ExportErrors)
	log.Printf("")
	log.Printf("VERIFICATION:")
	log.Printf("  Match:                  %v", result.VerificationMatch)
	log.Printf("  Missing:                %.2f%%", result.MissingPercentage)
	log.Printf("========================================")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return defaultValue
		}
		return d
	}
	return defaultValue
}
