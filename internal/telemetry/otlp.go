// Package telemetry exports a bounded, metadata-only projection of durable events.
package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"contractaudit/internal/execution"
)

const (
	queueCapacity        = 256
	batchCapacity        = 64
	responseLimit        = 64 << 10
	metricSeriesCapacity = 256
)

type Config struct {
	Endpoint    string
	Service     string
	InstanceID  string
	SampleRatio float64
}

// Stats separates trace delivery from metric data-point delivery. MetricsExported
// counts accepted data points. MetricsFailed counts rejected or unacknowledged
// data points. Both metric counters count points, not events or requests.
type Stats struct {
	Exported        uint64
	Dropped         uint64
	FailedRequests  uint64
	SampledOut      uint64
	MetricsExported uint64
	MetricsFailed   uint64
}

type Exporter struct {
	tracesEndpoint  string
	metricsEndpoint string
	service         string
	instance        string
	ratio           float64
	client          *http.Client
	queue           chan span
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	mu              sync.Mutex
	closed          bool
	close           sync.Once
	exported        atomic.Uint64
	dropped         atomic.Uint64
	failed          atomic.Uint64
	sampled         atomic.Uint64
	metricsExported atomic.Uint64
	metricsFailed   atomic.Uint64
	metricSeries    map[[3]string]*metricSeries
}

// New is disabled when Endpoint is empty. No network request is made at startup.
// Endpoint is the collector base URL. /v1/traces and /v1/metrics are appended.
func New(cfg Config) (*Exporter, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("OTLP endpoint must be an HTTP(S) collector base URL without credentials, query or fragment")
	}
	if math.IsNaN(cfg.SampleRatio) || math.IsInf(cfg.SampleRatio, 0) || cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return nil, errors.New("OTLP sampling ratio must be finite and between zero and one")
	}
	service := metadata(cfg.Service)
	if service == "" {
		return nil, errors.New("OTLP service identity is required")
	}
	// service.instance.id separates replicas behind one logical service. An
	// unset/invalid value falls back to the service identity so a misconfigured
	// deployment stays exportable instead of silently dropping telemetry.
	instance := metadata(cfg.InstanceID)
	if instance == "" {
		instance = service
	}
	basePath := strings.TrimRight(u.Path, "/")
	traces := *u
	traces.Path = basePath + "/v1/traces"
	traces.RawPath = ""
	metrics := *u
	metrics.Path = basePath + "/v1/metrics"
	metrics.RawPath = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	ctx, cancel := context.WithCancel(context.Background())
	e := &Exporter{
		tracesEndpoint: traces.String(), metricsEndpoint: metrics.String(), service: service, instance: instance, ratio: cfg.SampleRatio,
		client: &http.Client{Transport: transport, Timeout: 2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		queue: make(chan span, queueCapacity), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		metricSeries: make(map[[3]string]*metricSeries),
	}
	go e.run()
	return e, nil
}

// Record never waits for the network or for queue capacity. Its event must have
// already been committed. Provider text, model names and usage are not exported.
func (e *Exporter) Record(runID, attemptID string, event execution.Event) {
	if e == nil || event.DurationMS == nil || *event.DurationMS < 0 || event.At.IsZero() || event.SpanID == "" {
		return
	}
	trace := sha256.Sum256([]byte(attemptID))
	if e.ratio < 1 && float64(binary.BigEndian.Uint64(trace[:8]))/18446744073709551616.0 >= e.ratio {
		e.sampled.Add(1)
		return
	}
	item := span{
		TraceID: hex.EncodeToString(trace[:16]), SpanID: spanID(event.SpanID),
		Name: metadata(event.Phase), Kind: 1, Flags: 1,
		Start: strconv.FormatInt(event.At.Add(-time.Duration(*event.DurationMS)*time.Millisecond).UnixNano(), 10),
		End:   strconv.FormatInt(event.At.UnixNano(), 10),
		Attributes: [6]attribute{
			stringAttribute("audit.run_id", metadata(runID)),
			stringAttribute("audit.attempt_id", metadata(attemptID)),
			stringAttribute("audit.phase", metadata(event.Phase)),
			stringAttribute("audit.node_kind", metadata(event.NodeKind)),
			stringAttribute("audit.status", metadata(event.Status)),
			stringAttribute("audit.error_code", metadata(event.ErrorCode)),
		},
	}
	if event.ParentSpanID != "" && event.ParentSpanID != attemptID {
		item.ParentSpanID = spanID(event.ParentSpanID)
	}
	switch event.Status {
	case "completed":
		item.Status.Code = 1
	case "failed", "cancelled":
		item.Status.Code = 2
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		e.dropped.Add(1)
		return
	}
	select {
	case e.queue <- item:
	default:
		e.dropped.Add(1)
	}
}

func (e *Exporter) Stats() Stats {
	if e == nil {
		return Stats{}
	}
	return Stats{
		Exported: e.exported.Load(), Dropped: e.dropped.Load(), FailedRequests: e.failed.Load(), SampledOut: e.sampled.Load(),
		MetricsExported: e.metricsExported.Load(), MetricsFailed: e.metricsFailed.Load(),
	}
}

// Close drains within a fixed deadline, then cancels delivery and counts loss.
// Export failures never become application shutdown errors.
func (e *Exporter) Close() {
	if e == nil {
		return
	}
	e.close.Do(func() {
		e.mu.Lock()
		e.closed = true
		close(e.queue)
		e.mu.Unlock()
		timer := time.AfterFunc(3*time.Second, e.cancel)
		defer timer.Stop()
		<-e.done
		e.cancel()
		e.client.CloseIdleConnections()
		stats := e.Stats()
		log.Printf("OTLP metadata exporter stopped: exported=%d dropped=%d failed_requests=%d sampled_out=%d", stats.Exported, stats.Dropped, stats.FailedRequests, stats.SampledOut)
	})
}

func (e *Exporter) run() {
	defer close(e.done)
	batch := make([]span, 0, batchCapacity)
	for first := range e.queue {
		batch = append(batch[:0], first)
	drain:
		for len(batch) < batchCapacity {
			select {
			case next, ok := <-e.queue:
				if !ok {
					break drain
				}
				batch = append(batch, next)
			default:
				break drain
			}
		}
		if e.ctx.Err() != nil {
			e.dropped.Add(uint64(len(batch)))
			continue
		}
		rejected, err := e.send(batch)
		if err != nil {
			e.dropped.Add(uint64(len(batch)))
			if e.failed.Add(1) == 1 {
				log.Print("OTLP metadata export failed; durable business records are unaffected")
			}
			continue
		}
		e.exported.Add(uint64(len(batch) - rejected))
		e.dropped.Add(uint64(rejected))
		points, metricRejected, merr := e.sendMetrics(batch)
		if merr != nil {
			metricRejected = points
		}
		e.metricsExported.Add(uint64(points - metricRejected))
		if metricRejected > 0 && e.metricsFailed.Add(uint64(metricRejected)) == uint64(metricRejected) {
			log.Print("OTLP metadata metric export failed; durable business records are unaffected")
		}
	}
}

func (e *Exporter) send(batch []span) (int, error) {
	var payload exportRequest
	payload.ResourceSpans[0].Resource.Attributes[0] = stringAttribute("service.name", e.service)
	payload.ResourceSpans[0].Resource.Attributes[1] = stringAttribute("service.instance.id", e.instance)
	payload.ResourceSpans[0].ScopeSpans[0].Scope.Name = "contractaudit.execution"
	payload.ResourceSpans[0].ScopeSpans[0].Spans = batch
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.tracesEndpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || len(data) > responseLimit || response.StatusCode != http.StatusOK || media != "application/json" {
		return 0, errors.New("OTLP response was unsuccessful or exceeded its bound")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return 0, errors.New("OTLP acknowledgement must be a JSON object")
	}
	var result struct {
		PartialSuccess *struct {
			RejectedSpans json.Number `json:"rejectedSpans"`
		} `json:"partialSuccess"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, errors.New("OTLP response was not a JSON trace acknowledgement")
	}
	if result.PartialSuccess == nil || result.PartialSuccess.RejectedSpans == "" {
		return 0, nil
	}
	rejected, err := result.PartialSuccess.RejectedSpans.Int64()
	if err != nil || rejected < 0 || rejected > int64(len(batch)) {
		return 0, errors.New("OTLP rejection count was invalid")
	}
	return int(rejected), nil
}

// Only the delivery goroutine accesses metricSeries. Record keeps its bounded,
// nonblocking queue path and never waits for metric aggregation or the collector.
func (e *Exporter) sendMetrics(batch []span) (int, int, error) {
	active := make([]*metricSeries, 0, len(batch))
	for i := range batch {
		item := &batch[i]
		key := [3]string{
			attributeValue(item.Attributes[:], "audit.phase"),
			attributeValue(item.Attributes[:], "audit.node_kind"),
			attributeValue(item.Attributes[:], "audit.status"),
		}
		series := e.metricSeries[key]
		if series == nil {
			// Reserve one of the 256 series for overflow. The reserved tuple
			// retains event totals without keeping arbitrary new label values.
			if len(e.metricSeries) >= metricSeriesCapacity-1 {
				key = [3]string{"__overflow__", "__overflow__", "__overflow__"}
				series = e.metricSeries[key]
			}
			if series == nil {
				series = &metricSeries{attributes: metricAttributes(key), start: item.Start}
				e.metricSeries[key] = series
			}
		}
		series.count++
		if !series.active {
			series.active = true
			active = append(active, series)
		}
		startNano, startErr := strconv.ParseInt(item.Start, 10, 64)
		endNano, endErr := strconv.ParseInt(item.End, 10, 64)
		if startErr == nil && endErr == nil && endNano >= startNano &&
			(!series.hasDuration || endNano >= series.latestEnd) {
			series.duration = (endNano - startNano) / int64(time.Millisecond)
			series.latestEnd = endNano
			series.hasDuration = true
		}
	}
	events := make([]numberPoint, 0, len(active))
	durations := make([]numberPoint, 0, len(active))
	now := time.Now().UnixNano()
	for _, series := range active {
		series.active = false
		// Collection timestamps advance even when events arrive out of order
		// or a later batch has the same event timestamp as an earlier batch.
		at := max(now, series.latestEnd, series.lastTime+1)
		series.lastTime = at
		events = append(events, numberPoint{Attributes: series.attributes, Start: series.start,
			Time: strconv.FormatInt(at, 10), AsInt: strconv.FormatInt(series.count, 10)})
		if series.hasDuration {
			durations = append(durations, numberPoint{Attributes: series.attributes,
				Time: strconv.FormatInt(series.latestEnd, 10), AsInt: strconv.FormatInt(series.duration, 10)})
		}
	}
	points := len(events) + len(durations)
	var payload metricsRequest
	payload.ResourceMetrics[0].Resource.Attributes[0] = stringAttribute("service.name", e.service)
	payload.ResourceMetrics[0].Resource.Attributes[1] = stringAttribute("service.instance.id", e.instance)
	payload.ResourceMetrics[0].ScopeMetrics[0].Scope.Name = "contractaudit.execution"
	payload.ResourceMetrics[0].ScopeMetrics[0].Metrics = []metric{
		{Name: "audit.execution.events", Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true, DataPoints: events}},
		{Name: "audit.execution.duration_ms", Gauge: &gaugeMetric{DataPoints: durations}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return points, 0, err
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.metricsEndpoint, bytes.NewReader(body))
	if err != nil {
		return points, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := e.client.Do(req)
	if err != nil {
		return points, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || len(data) > responseLimit || response.StatusCode != http.StatusOK || media != "application/json" {
		return points, 0, errors.New("OTLP metric response was unsuccessful or exceeded its bound")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return points, 0, errors.New("OTLP metric acknowledgement must be a JSON object")
	}
	var result struct {
		PartialSuccess *struct {
			RejectedDataPoints json.Number `json:"rejectedDataPoints"`
		} `json:"partialSuccess"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return points, 0, errors.New("OTLP response was not a JSON metric acknowledgement")
	}
	if result.PartialSuccess == nil || result.PartialSuccess.RejectedDataPoints == "" {
		return points, 0, nil
	}
	rejected, err := result.PartialSuccess.RejectedDataPoints.Int64()
	if err != nil || rejected < 0 || rejected > int64(points) {
		return points, 0, errors.New("OTLP metric rejection count was invalid")
	}
	return points, int(rejected), nil
}

func metricAttributes(values [3]string) []attribute {
	attrs := make([]attribute, 0, len(values))
	for i, key := range [3]string{"audit.phase", "audit.node_kind", "audit.status"} {
		if values[i] != "" {
			attrs = append(attrs, stringAttribute(key, values[i]))
		}
	}
	return attrs
}

func attributeValue(attrs []attribute, key string) string {
	for i := range attrs {
		if attrs[i].Key == key {
			return attrs[i].Value.StringValue
		}
	}
	return ""
}

// metricSeries is cumulative for the lifetime of the exporter, including after
// rejected or unacknowledged sends. The next snapshot can recover its total.
type metricSeries struct {
	attributes  []attribute
	start       string
	count       int64
	lastTime    int64
	latestEnd   int64
	duration    int64
	hasDuration bool
	active      bool
}

func metadata(value string) string {
	if len(value) > 128 {
		return ""
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return ""
		}
	}
	return value
}

func spanID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

type attribute struct {
	Key   string `json:"key"`
	Value struct {
		StringValue string `json:"stringValue"`
	} `json:"value"`
}

func stringAttribute(key, value string) attribute {
	item := attribute{Key: key}
	item.Value.StringValue = value
	return item
}

type span struct {
	TraceID      string       `json:"traceId"`
	SpanID       string       `json:"spanId"`
	ParentSpanID string       `json:"parentSpanId,omitempty"`
	Name         string       `json:"name"`
	Kind         int          `json:"kind"`
	Flags        int          `json:"flags"`
	Start        string       `json:"startTimeUnixNano"`
	End          string       `json:"endTimeUnixNano"`
	Attributes   [6]attribute `json:"attributes"`
	Status       struct {
		Code int `json:"code"`
	} `json:"status"`
}

type exportRequest struct {
	ResourceSpans [1]struct {
		Resource struct {
			Attributes [2]attribute `json:"attributes"`
		} `json:"resource"`
		ScopeSpans [1]struct {
			Scope struct {
				Name string `json:"name"`
			} `json:"scope"`
			Spans []span `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

type numberPoint struct {
	Attributes []attribute `json:"attributes,omitempty"`
	Start      string      `json:"startTimeUnixNano,omitempty"`
	Time       string      `json:"timeUnixNano"`
	AsInt      string      `json:"asInt"`
}

type sumMetric struct {
	AggregationTemporality int           `json:"aggregationTemporality"`
	IsMonotonic            bool          `json:"isMonotonic"`
	DataPoints             []numberPoint `json:"dataPoints"`
}

type gaugeMetric struct {
	DataPoints []numberPoint `json:"dataPoints"`
}

type metric struct {
	Name  string       `json:"name"`
	Sum   *sumMetric   `json:"sum,omitempty"`
	Gauge *gaugeMetric `json:"gauge,omitempty"`
}

type metricsRequest struct {
	ResourceMetrics [1]struct {
		Resource struct {
			Attributes [2]attribute `json:"attributes"`
		} `json:"resource"`
		ScopeMetrics [1]struct {
			Scope struct {
				Name string `json:"name"`
			} `json:"scope"`
			Metrics []metric `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}
