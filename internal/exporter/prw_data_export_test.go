package exporter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	"github.com/szibis/metrics-governor/internal/prw"
)

// =============================================================================
// PRWExporter.ExportData
// =============================================================================

func TestPRWExporter_ExportData_Success(t *testing.T) {
	var receivedBody []byte
	var receivedEncoding string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	// Create a valid PRW request and serialize it
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	data, err := req.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	err = exp.ExportData(context.Background(), data)
	if err != nil {
		t.Fatalf("ExportData() error = %v", err)
	}

	if len(receivedBody) == 0 {
		t.Error("expected non-empty body")
	}
	if receivedEncoding != "snappy" {
		t.Errorf("Content-Encoding = %q, want snappy", receivedEncoding)
	}
}

func TestPRWExporter_ExportData_EmptyData(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	// Empty data should return nil
	err = exp.ExportData(context.Background(), nil)
	if err != nil {
		t.Errorf("ExportData(nil) error = %v", err)
	}

	err = exp.ExportData(context.Background(), []byte{})
	if err != nil {
		t.Errorf("ExportData(empty) error = %v", err)
	}
}

func TestPRWExporter_ExportData_ExtraLabelsError(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		VMMode: true,
		VMOptions: VMRemoteWriteOptions{
			ExtraLabels: map[string]string{
				"env": "prod",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	data := []byte("some data")
	err = exp.ExportData(context.Background(), data)
	if !errors.Is(err, ErrExtraLabelsRequireDeserialize) {
		t.Errorf("expected ErrExtraLabelsRequireDeserialize, got %v", err)
	}
}

func TestPRWExporter_ExportData_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "m"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	data, _ := req.Marshal()

	err = exp.ExportData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 400 {
			t.Errorf("expected status 400, got %d", exportErr.StatusCode)
		}
		var clientErr *PRWClientError
		if !errors.As(err, &clientErr) {
			t.Error("expected PRWClientError inner error")
		}
	} else {
		t.Errorf("expected *ExportError, got %T", err)
	}
}

func TestPRWExporter_ExportData_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "m"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	data, _ := req.Marshal()

	err = exp.ExportData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 500 {
			t.Errorf("expected status 500, got %d", exportErr.StatusCode)
		}
		var serverErr *PRWServerError
		if !errors.As(err, &serverErr) {
			t.Error("expected PRWServerError inner error")
		}
	}
}

func TestPRWExporter_ExportData_ConnectionError(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: "http://127.0.0.1:1",
		Timeout:  1 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "m"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	data, _ := req.Marshal()

	err = exp.ExportData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestPRWExporter_ExportData_EmptyResponseBody(t *testing.T) {
	// Server returns error with empty body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "m"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	data, _ := req.Marshal()

	err = exp.ExportData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for 503 response")
	}

	// Should have generated a default message
	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.Message == "" {
			t.Error("expected non-empty error message")
		}
	}
}

func TestPRWExporter_ExportData_VersionV2Header(t *testing.T) {
	var receivedVersion string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedVersion = r.Header.Get("X-Prometheus-Remote-Write-Version")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Version:  prw.Version2,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "m"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	data, _ := req.Marshal()

	err = exp.ExportData(context.Background(), data)
	if err != nil {
		t.Fatalf("ExportData() error = %v", err)
	}

	if receivedVersion != "2.0.0" {
		t.Errorf("X-Prometheus-Remote-Write-Version = %q, want 2.0.0", receivedVersion)
	}
}

// =============================================================================
// PRWExporter.CompressData
// =============================================================================

func TestPRWExporter_CompressData_Snappy(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	data := []byte("test data for compression, repeating content repeating content repeating content")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "snappy" {
		t.Errorf("encoding = %q, want snappy", encoding)
	}
	if len(compressed) == 0 {
		t.Error("expected non-empty compressed data")
	}
}

func TestPRWExporter_CompressData_Zstd(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		VMMode: true,
		VMOptions: VMRemoteWriteOptions{
			Compression: "zstd",
		},
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	data := []byte("test data for zstd compression, repeating content repeating content")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "zstd" {
		t.Errorf("encoding = %q, want zstd", encoding)
	}
	if len(compressed) == 0 {
		t.Error("expected non-empty compressed data")
	}
}

func TestPRWExporter_CompressData_NoCompression(t *testing.T) {
	exp := &PRWExporter{
		compression: compression.TypeNone,
	}

	data := []byte("test data")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "" {
		t.Errorf("encoding = %q, want empty", encoding)
	}
	if string(compressed) != string(data) {
		t.Error("expected data unchanged when compression is none")
	}
}

func TestPRWExporter_CompressData_EmptyType(t *testing.T) {
	exp := &PRWExporter{
		compression: "",
	}

	data := []byte("test data")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "" {
		t.Errorf("encoding = %q, want empty", encoding)
	}
	if string(compressed) != string(data) {
		t.Error("expected data unchanged when compression type is empty")
	}
}

// =============================================================================
// PRWExporter.SendCompressed
// =============================================================================

func TestPRWExporter_SendCompressed_Success(t *testing.T) {
	var receivedEncoding string
	var receivedContentType string
	var receivedVersion string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		receivedContentType = r.Header.Get("Content-Type")
		receivedVersion = r.Header.Get("X-Prometheus-Remote-Write-Version")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	compressedData := []byte("compressed-data")
	err = exp.SendCompressed(context.Background(), compressedData, "snappy", 100)
	if err != nil {
		t.Fatalf("SendCompressed() error = %v", err)
	}

	if receivedContentType != "application/x-protobuf" {
		t.Errorf("Content-Type = %q, want application/x-protobuf", receivedContentType)
	}
	if receivedEncoding != "snappy" {
		t.Errorf("Content-Encoding = %q, want snappy", receivedEncoding)
	}
	if receivedVersion != "0.1.0" {
		t.Errorf("Version header = %q, want 0.1.0", receivedVersion)
	}
}

func TestPRWExporter_SendCompressed_ExtraLabelsError(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		VMMode: true,
		VMOptions: VMRemoteWriteOptions{
			ExtraLabels: map[string]string{"env": "test"},
		},
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "snappy", 4)
	if !errors.Is(err, ErrExtraLabelsRequireDeserialize) {
		t.Errorf("expected ErrExtraLabelsRequireDeserialize, got %v", err)
	}
}

func TestPRWExporter_SendCompressed_NoEncoding(t *testing.T) {
	var receivedEncoding string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "", 4)
	if err != nil {
		t.Fatalf("SendCompressed() error = %v", err)
	}
	if receivedEncoding != "" {
		t.Errorf("Content-Encoding = %q, want empty", receivedEncoding)
	}
}

func TestPRWExporter_SendCompressed_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "snappy", 4)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 400 {
			t.Errorf("expected status 400, got %d", exportErr.StatusCode)
		}
	}
}

func TestPRWExporter_SendCompressed_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "snappy", 4)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 500 {
			t.Errorf("expected status 500, got %d", exportErr.StatusCode)
		}
	}
}

func TestPRWExporter_SendCompressed_ConnectionError(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: "http://127.0.0.1:1",
		Timeout:  1 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "snappy", 4)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestPRWExporter_SendCompressed_EmptyBody(t *testing.T) {
	// Server returns error status with empty body — should generate default message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "snappy", 4)
	if err == nil {
		t.Fatal("expected error for 503 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.Message == "" {
			t.Error("expected non-empty message for empty body error")
		}
	}
}

func TestPRWExporter_SendCompressed_V2Header(t *testing.T) {
	var receivedVersion string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedVersion = r.Header.Get("X-Prometheus-Remote-Write-Version")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Version:  prw.Version2,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.SendCompressed(context.Background(), []byte("data"), "snappy", 4)
	if err != nil {
		t.Fatalf("SendCompressed() error = %v", err)
	}

	if receivedVersion != "2.0.0" {
		t.Errorf("Version = %q, want 2.0.0", receivedVersion)
	}
}
