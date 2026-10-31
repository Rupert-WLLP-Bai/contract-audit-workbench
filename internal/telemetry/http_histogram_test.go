package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The collector consumes cumulative, non-cumulative-bucket OTLP histograms;
// individual observations at a boundary must land in that boundary's bucket.
func TestHTTPHistogramCollectorPayload(t *testing.T) {
	payloads := make(chan []byte, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		payloads <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer collector.Close()
	exporter, err := New(Config{Endpoint: collector.URL, Service: "audit.edge", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, duration := range []int64{0, 1, 2, 3, 60001} {
		exporter.observeHTTP(httpObservation{method: "GET", route: "/health", status: 200, durationMS: duration})
	}
	exporter.Close()
	var payload struct {
		Resources []struct {
			Scopes []struct {
				Metrics []struct {
					Name      string `json:"name"`
					Unit      string `json:"unit"`
					Histogram *struct {
						Temporality int `json:"aggregationTemporality"`
						Points      []struct {
							Count   string    `json:"count"`
							Sum     float64   `json:"sum"`
							Buckets []string  `json:"bucketCounts"`
							Bounds  []float64 `json:"explicitBounds"`
							Start   string    `json:"startTimeUnixNano"`
							Time    string    `json:"timeUnixNano"`
						} `json:"dataPoints"`
					} `json:"histogram"`
				} `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
	}
	select {
	case body := <-payloads:
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("collector received no metric payload")
	}
	found := false
	for _, resource := range payload.Resources {
		for _, scope := range resource.Scopes {
			for _, metric := range scope.Metrics {
				if metric.Name != "audit.http.duration_ms" {
					continue
				}
				found = true
				if metric.Unit != "ms" || metric.Histogram == nil || metric.Histogram.Temporality != 2 || len(metric.Histogram.Points) != 1 {
					t.Fatalf("unexpected histogram metadata: %+v", metric)
				}
				point := metric.Histogram.Points[0]
				if point.Count != "5" || point.Sum != 60007 || point.Start == "" || point.Time == "" ||
					len(point.Bounds) != 15 || len(point.Buckets) != 16 || point.Bounds[0] != 1 || point.Bounds[14] != 60000 ||
					point.Buckets[0] != "2" || point.Buckets[1] != "1" || point.Buckets[2] != "1" || point.Buckets[15] != "1" {
					t.Fatalf("incorrect histogram observations: %+v", point)
				}
				for _, count := range point.Buckets[3:15] {
					if count != "0" {
						t.Fatalf("unexpected intermediate bucket: %+v", point.Buckets)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("collector received no HTTP duration histogram")
	}
}
