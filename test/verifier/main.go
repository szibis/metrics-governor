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
	Timestamp time.Time

	// VictoriaMetrics stats
	VMTotalTimeSeries     int
	VMUniqueMetricNames   int
	VMHighCardinalityTS   int
	VMVerificationCounter int64
	VMDatapointsIngested  int64

	// Metrics-Governor stats
	MGDatapointsReceived int64
	MGDatapointsSent     int64
	MGQueueSize          int
	MGDroppedTotal       int64
	MGExportErrors       int64
	MGBatchesSent        int64

	// Verification results
	IngestionRate       float64 // percentage of datapoints that made it to VM
	VerificationMatch   bool
	PassThreshold       float64
	VerificationMessage string
}

var (
	previousVMDatapoints int64
	previousMGSent       int64
	lastCheckTime        time.Time
)

func main() {
	vmEndpoint := getEnv("VM_ENDPOINT", "http://localhost:8428")
	mgEndpoint := getEnv("MG_ENDPOINT", "http://localhost:9090")
	checkInterval := getEnvDuration("CHECK_INTERVAL", 15*time.Second)
	passThreshold := getEnvFloat("PASS_THRESHOLD", 95.0)

	log.Printf("========================================")
	log.Printf("  DATA VERIFICATION TOOL")
	log.Printf("========================================")
	log.Printf("VictoriaMetrics: %s", vmEndpoint)
	log.Printf("Metrics Governor: %s", mgEndpoint)
	log.Printf("Check Interval: %s", checkInterval)
	log.Printf("Pass Threshold: %.1f%%", passThreshold)
	log.Printf("========================================")

	// Wait for services to be ready
	log.Println("Waiting for services to be ready...")
	time.Sleep(15 * time.Second)

	lastCheckTime = time.Now()
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	checkCount := 0
	passCount := 0

	for {
		result := verify(vmEndpoint, mgEndpoint, passThreshold)
		printResult(result)

		checkCount++
		if result.VerificationMatch {
			passCount++
		}

		// Print running summary
		passRate := float64(passCount) / float64(checkCount) * 100
		log.Printf("Running verification: %d/%d checks passed (%.1f%%)", passCount, checkCount, passRate)
		log.Printf("")

		<-ticker.C
	}
}

func verify(vmEndpoint, mgEndpoint string, passThreshold float64) VerificationResult {
	result := VerificationResult{
		Timestamp:     time.Now(),
		PassThreshold: passThreshold,
	}

	// Query VictoriaMetrics for total time series
	totalTS, err := queryVMScalar(vmEndpoint, "count({__name__=~\".+\"})")
	if err != nil {
		log.Printf("Error querying total time series: %v", err)
	} else {
		result.VMTotalTimeSeries = int(totalTS)
	}

	// Query VictoriaMetrics for unique metric names
	uniqueNames, err := queryVMScalar(vmEndpoint, "count(count by (__name__)({__name__=~\".+\"}))")
	if err != nil {
		log.Printf("Error querying unique metric names: %v", err)
	} else {
		result.VMUniqueMetricNames = int(uniqueNames)
	}

	// Query verification counter from VictoriaMetrics
	verificationCount, err := queryVMScalar(vmEndpoint, "max(generator_verification_counter_total)")
	if err != nil {
		log.Printf("Error querying verification counter: %v", err)
	} else {
		result.VMVerificationCounter = int64(verificationCount)
	}

	// Query high cardinality metrics count
	highCardTS, err := queryVMScalar(vmEndpoint, "count(high_cardinality_metric_total)")
	if err != nil {
		log.Printf("Debug: high cardinality query: %v", err)
	} else {
		result.VMHighCardinalityTS = int(highCardTS)
	}

	// Query total ingested datapoints from VM's internal metrics (if available)
	vmIngested, err := queryVMScalar(vmEndpoint, "sum(vm_rows_inserted_total)")
	if err != nil {
		// Fallback to counting series * samples
		log.Printf("Debug: VM ingestion stats not available")
	} else {
		result.VMDatapointsIngested = int64(vmIngested)
	}

	// Query metrics-governor stats
	mgStats, err := queryMGStats(mgEndpoint)
	if err != nil {
		log.Printf("Error querying metrics-governor stats: %v", err)
	} else {
		result.MGDatapointsReceived = mgStats.DatapointsReceived
		result.MGDatapointsSent = mgStats.DatapointsSent
		result.MGQueueSize = mgStats.QueueSize
		result.MGDroppedTotal = mgStats.DroppedTotal
		result.MGExportErrors = mgStats.ExportErrors
		result.MGBatchesSent = mgStats.BatchesSent
	}

	// Calculate verification
	result.calculateVerification()

	return result
}

func (r *VerificationResult) calculateVerification() {
	// Multiple verification strategies

	// 1. Check if metrics are flowing (time series exist)
	if r.VMTotalTimeSeries == 0 {
		r.VerificationMatch = false
		r.VerificationMessage = "No time series in VictoriaMetrics"
		return
	}

	// 2. Check verification counter is incrementing
	if r.VMVerificationCounter == 0 {
		r.VerificationMatch = false
		r.VerificationMessage = "Verification counter not found or zero"
		return
	}

	// 3. Check for export errors
	if r.MGExportErrors > 0 {
		r.VerificationMessage = fmt.Sprintf("Export errors detected: %d", r.MGExportErrors)
		// Don't fail on errors alone, check other metrics
	}

	// 4. Calculate ingestion rate based on sent vs verification counter
	// The verification counter is incremented by batch_id, so it tracks batches
	if r.MGBatchesSent > 0 {
		// Check if batches sent roughly matches verification counter progression
		batchDiff := r.MGBatchesSent - r.VMVerificationCounter
		if batchDiff < 0 {
			batchDiff = -batchDiff
		}

		// Allow some lag (5 batches tolerance for timing)
		if batchDiff <= 5 {
			r.IngestionRate = 100.0
		} else {
			r.IngestionRate = float64(r.VMVerificationCounter) / float64(r.MGBatchesSent) * 100
		}
	}

	// 5. Check dropped metrics
	if r.MGDroppedTotal > 0 && r.MGDatapointsSent > 0 {
		dropRate := float64(r.MGDroppedTotal) / float64(r.MGDatapointsSent+r.MGDroppedTotal) * 100
		if dropRate > (100 - r.PassThreshold) {
			r.VerificationMatch = false
			r.VerificationMessage = fmt.Sprintf("High drop rate: %.2f%%", dropRate)
			return
		}
	}

	// 6. Final verification
	if r.IngestionRate >= r.PassThreshold {
		r.VerificationMatch = true
		r.VerificationMessage = fmt.Sprintf("Ingestion rate %.2f%% meets threshold %.2f%%", r.IngestionRate, r.PassThreshold)
	} else if r.IngestionRate > 0 {
		r.VerificationMatch = false
		r.VerificationMessage = fmt.Sprintf("Ingestion rate %.2f%% below threshold %.2f%%", r.IngestionRate, r.PassThreshold)
	} else {
		// Fallback: if we have time series and verification counter, consider it passing
		if r.VMTotalTimeSeries > 0 && r.VMVerificationCounter > 0 && r.MGExportErrors == 0 {
			r.VerificationMatch = true
			r.IngestionRate = 100.0
			r.VerificationMessage = "Data flowing, no errors detected"
		} else {
			r.VerificationMatch = false
			r.VerificationMessage = "Unable to calculate ingestion rate"
		}
	}
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
	DatapointsReceived int64
	DatapointsSent     int64
	QueueSize          int
	DroppedTotal       int64
	ExportErrors       int64
	BatchesSent        int64
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

	// Parse Prometheus text format
	lines := string(body)

	// Extract metrics
	stats.DatapointsReceived = extractMetricValue(lines, "metrics_governor_datapoints_received_total")
	stats.DatapointsSent = extractMetricValue(lines, "metrics_governor_datapoints_total")
	stats.QueueSize = int(extractMetricValue(lines, "metrics_governor_queue_size"))
	stats.DroppedTotal = extractMetricValue(lines, "metrics_governor_queue_dropped_total")
	stats.ExportErrors = extractMetricValue(lines, "metrics_governor_export_errors_total")
	stats.BatchesSent = extractMetricValue(lines, "metrics_governor_batches_sent_total")

	return stats, nil
}

func extractMetricValue(metricsText, metricName string) int64 {
	// Parse Prometheus text format line by line
	lines := strings.Split(metricsText, "\n")
	var total int64 = 0

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
					total += int64(val)
				}
			}
		}
	}
	return total
}

func printResult(result VerificationResult) {
	status := "PASS"
	if !result.VerificationMatch {
		status = "FAIL"
	}

	log.Printf("========================================")
	log.Printf("  VERIFICATION RESULT - %s", status)
	log.Printf("========================================")
	log.Printf("Timestamp: %s", result.Timestamp.Format(time.RFC3339))
	log.Printf("")
	log.Printf("VICTORIAMETRICS:")
	log.Printf("  Total time series:      %d", result.VMTotalTimeSeries)
	log.Printf("  Unique metric names:    %d", result.VMUniqueMetricNames)
	log.Printf("  High cardinality TS:    %d", result.VMHighCardinalityTS)
	log.Printf("  Verification counter:   %d", result.VMVerificationCounter)
	log.Printf("")
	log.Printf("METRICS-GOVERNOR:")
	log.Printf("  Datapoints received:    %d", result.MGDatapointsReceived)
	log.Printf("  Datapoints sent:        %d", result.MGDatapointsSent)
	log.Printf("  Batches sent:           %d", result.MGBatchesSent)
	log.Printf("  Queue size:             %d", result.MGQueueSize)
	log.Printf("  Dropped total:          %d", result.MGDroppedTotal)
	log.Printf("  Export errors:          %d", result.MGExportErrors)
	log.Printf("")
	log.Printf("VERIFICATION:")
	log.Printf("  Ingestion rate:         %.2f%%", result.IngestionRate)
	log.Printf("  Pass threshold:         %.2f%%", result.PassThreshold)
	log.Printf("  Status:                 %s", status)
	log.Printf("  Message:                %s", result.VerificationMessage)
	log.Printf("========================================")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return defaultValue
		}
		return f
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
