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
	// Environment labels the deployment environment, for example the APP_ENV
	// value. An empty value omits the attribute instead of exporting an empty
	// label.
	Environment string
}

// resourceAttributes builds the OTLP resource identity shared by every payload:
// the logical service, the replica instance, and the deployment environment
// when one is configured.
func (e *Exporter) resourceAttributes() []attribute {
	attributes := []attribute{
		stringAttribute("service.name", e.service),
		stringAttribute("service.instance.id", e.instance),
	}
	if e.environment != "" {
		attributes = append(attributes, stringAttribute("deployment.environment.name", e.environment))
	}
	return attributes
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
	// HTTPDropped counts HTTP RED observations that exceeded the bounded
	// metric series allocation. They never block or fail the exchange.
	HTTPDropped uint64
}

type Exporter struct {
	tracesEndpoint  string
	metricsEndpoint string
	service         string
	instance        string
	environment     string
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

	// httpMu guards the HTTP RED aggregates. Producers run on HTTP handler
	// goroutines; the delivery goroutine drains the active series.
	httpMu        sync.Mutex
	httpRequests  map[httpCounterKey]*metricSeries
	httpDurations map[[2]string]*httpHistogram
	httpDropped   atomic.Uint64

	runtimeMu      sync.Mutex
	runtimeSource  func(context.Context) RuntimeSnapshot
	queueWait      runtimeHistogram
	queueExecution runtimeHistogram
	sampleCtx      context.Context
	cancelSample   context.CancelFunc
	metricStart    string
	queueClaims    atomic.Uint64
	queueReclaims  atomic.Uint64
	quotaQueue     atomic.Uint64
	quotaModel     atomic.Uint64
	quotaTool      atomic.Uint64
	queueErrors    [4]atomic.Uint64
	outboxErrors   [4]atomic.Uint64
}

// New is disabled when Endpoint is empty. No network request is made at startup.
// Endpoint is the collector base URL. /v1/traces and /v1/metrics are appended.
func New(cfg Config) (*Exporter, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil
	}
	u, err := parseCollectorEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if err := validateSampleRatio(cfg.SampleRatio); err != nil {
		return nil, err
	}
	service := metadata(cfg.Service)
	if service == "" {
		return nil, errors.New("OTLP service identity is required")
	}
	environment := strings.TrimSpace(cfg.Environment)
	// service.instance.id separates replicas behind one logical service. An
	// unset/invalid value falls back to the service identity so a misconfigured
	// deployment stays exportable instead of silently dropping telemetry.
	instance := metadata(cfg.InstanceID)
	if instance == "" {
		instance = service
	}
	traces, metrics := otlpEndpoints(u)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	ctx, cancel := context.WithCancel(context.Background())
	sampleCtx, cancelSample := context.WithCancel(ctx)
	e := &Exporter{
		tracesEndpoint: traces, metricsEndpoint: metrics, service: service, instance: instance,
		environment: environment, ratio: cfg.SampleRatio,
		client: &http.Client{Transport: transport, Timeout: 2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		queue: make(chan span, queueCapacity), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		sampleCtx: sampleCtx, cancelSample: cancelSample, metricStart: strconv.FormatInt(time.Now().UnixNano(), 10),
		metricSeries:  make(map[[3]string]*metricSeries),
		httpRequests:  make(map[httpCounterKey]*metricSeries),
		httpDurations: make(map[[2]string]*httpHistogram),
	}
	go e.run()
	return e, nil
}

func parseCollectorEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("OTLP endpoint must be an HTTP(S) collector base URL without credentials, query or fragment")
	}
	return u, nil
}

func validateSampleRatio(ratio float64) error {
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
		return errors.New("OTLP sampling ratio must be finite and between zero and one")
	}
	return nil
}

func otlpEndpoints(u *url.URL) (string, string) {
	basePath := strings.TrimRight(u.Path, "/")
	traces := *u
	traces.Path = basePath + "/v1/traces"
	traces.RawPath = ""
	metrics := *u
	metrics.Path = basePath + "/v1/metrics"
	metrics.RawPath = ""
	return traces.String(), metrics.String()
}

// Record never waits for the network or for queue capacity. Its event must have
// already been committed. Provider text, model names and usage are not exported.
func (e *Exporter) Record(runID, attemptID string, event execution.Event) {
	if !recordableEvent(e, event) {
		return
	}
	trace := sha256.Sum256([]byte(attemptID))
	if e.ratio < 1 && float64(binary.BigEndian.Uint64(trace[:8]))/18446744073709551616.0 >= e.ratio {
		e.sampled.Add(1)
		return
	}
	e.enqueue(buildSpan(runID, attemptID, event, trace))
}

func recordableEvent(e *Exporter, event execution.Event) bool {
	return e != nil && event.DurationMS != nil && *event.DurationMS >= 0 && !event.At.IsZero() && event.SpanID != ""
}

func buildSpan(runID, attemptID string, event execution.Event, trace [32]byte) span {
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
	return item
}

func (e *Exporter) enqueue(item span) {
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
		HTTPDropped: e.httpDropped.Load(),
	}
}

// Close drains within a fixed deadline, then cancels delivery and counts loss.
// Export failures never become application shutdown errors.
func (e *Exporter) Close() {
	if e == nil {
		return
	}
	e.close.Do(func() {
		e.cancelSample()
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
	// A deployment that only serves HTTP (the edge gateway, the tool gateway)
	// never enqueues an execution span, so RED aggregates flush on their own
	// schedule instead of waiting for a span that may never arrive.
	ticker := time.NewTicker(httpFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case first, ok := <-e.queue:
			if !ok {
				e.flushMetrics(false)
				return
			}
			e.deliver(e.collectBatch(first))
		case <-ticker.C:
			e.flushMetrics(true)
		}
	}
}

func (e *Exporter) collectBatch(first span) []span {
	batch := make([]span, 0, batchCapacity)
	batch = append(batch, first)
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
	return batch
}

func (e *Exporter) deliver(batch []span) {
	if e.ctx.Err() != nil {
		e.dropped.Add(uint64(len(batch)))
		return
	}
	rejected, err := e.send(batch)
	if err != nil {
		e.dropped.Add(uint64(len(batch)))
		if e.failed.Add(1) == 1 {
			log.Print("OTLP metadata export failed; durable business records are unaffected")
		}
		return
	}
	e.exported.Add(uint64(len(batch) - rejected))
	e.dropped.Add(uint64(rejected))
	points, metricRejected, merr := e.sendMetrics(batch, nil)
	if merr != nil {
		metricRejected = points
	}
	e.metricsExported.Add(uint64(points - metricRejected))
	if metricRejected > 0 && e.metricsFailed.Add(uint64(metricRejected)) == uint64(metricRejected) {
		log.Print("OTLP metadata metric export failed; durable business records are unaffected")
	}
}

func (e *Exporter) send(batch []span) (int, error) {
	var payload exportRequest
	payload.ResourceSpans[0].Resource.Attributes = e.resourceAttributes()
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
	data, err := readOTLPResponse(response, "OTLP response was unsuccessful or exceeded its bound", "OTLP acknowledgement must be a JSON object")
	if err != nil {
		return 0, err
	}
	return traceRejection(data, len(batch))
}

func readOTLPResponse(response *http.Response, failure, objectFailure string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || len(data) > responseLimit || response.StatusCode != http.StatusOK || media != "application/json" {
		return nil, errors.New(failure)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, errors.New(objectFailure)
	}
	return data, nil
}

func traceRejection(data []byte, count int) (int, error) {
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
	if err != nil || rejected < 0 || rejected > int64(count) {
		return 0, errors.New("OTLP rejection count was invalid")
	}
	return int(rejected), nil
}

// Only the delivery goroutine accesses metricSeries. Record keeps its bounded,
// nonblocking queue path and never waits for metric aggregation or the collector.
// The HTTP RED series and periodic runtime samples use their own scopes.
func (e *Exporter) sendMetrics(batch []span, runtimeMetrics []metric) (int, int, error) {
	active := e.aggregateMetrics(batch)
	events, durations := metricPoints(active)
	httpEvents, httpDurations := e.drainHTTPMetrics()
	points := len(events) + len(durations) + len(httpEvents) + len(httpDurations)
	for _, item := range runtimeMetrics {
		if item.Sum != nil {
			points += len(item.Sum.DataPoints)
		}
		if item.Gauge != nil {
			points += len(item.Gauge.DataPoints)
		}
		if item.Histogram != nil {
			points += len(item.Histogram.DataPoints)
		}
	}
	if points == 0 {
		return 0, 0, nil
	}
	var payload metricsRequest
	payload.ResourceMetrics[0].Resource.Attributes = e.resourceAttributes()
	metrics := []metric{
		{Name: "audit.execution.events", Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true, DataPoints: events}},
		{Name: "audit.execution.duration_ms", Gauge: &gaugeMetric{DataPoints: durations}},
	}
	payload.ResourceMetrics[0].ScopeMetrics = []scopeMetrics{
		{Scope: scopeName("contractaudit.execution"), Metrics: metrics},
	}
	if len(httpEvents) > 0 || len(httpDurations) > 0 {
		payload.ResourceMetrics[0].ScopeMetrics = append(payload.ResourceMetrics[0].ScopeMetrics, scopeMetrics{
			Scope: scopeName("contractaudit.http"), Metrics: []metric{
				{Name: "audit.http.requests", Sum: &sumMetric{AggregationTemporality: 2, IsMonotonic: true, DataPoints: httpEvents}},
				{Name: "audit.http.duration_ms", Unit: "ms", Histogram: &histogramMetric{AggregationTemporality: 2, DataPoints: httpDurations}},
			},
		})
	}
	if len(runtimeMetrics) > 0 {
		payload.ResourceMetrics[0].ScopeMetrics = append(payload.ResourceMetrics[0].ScopeMetrics, scopeMetrics{
			Scope: scopeName("contractaudit.runtime"), Metrics: runtimeMetrics,
		})
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
	data, err := readOTLPResponse(response, "OTLP metric response was unsuccessful or exceeded its bound", "OTLP metric acknowledgement must be a JSON object")
	if err != nil {
		return points, 0, err
	}
	return metricRejection(data, points)
}

func (e *Exporter) aggregateMetrics(batch []span) []*metricSeries {
	active := make([]*metricSeries, 0, len(batch))
	for i := range batch {
		item := &batch[i]
		series := e.seriesFor(item)
		series.count++
		if !series.active {
			series.active = true
			active = append(active, series)
		}
		series.observeDuration(item)
	}
	return active
}

func (e *Exporter) seriesFor(item *span) *metricSeries {
	key := [3]string{
		metricPhase(attributeValue(item.Attributes[:], "audit.phase")),
		metricNodeKind(attributeValue(item.Attributes[:], "audit.node_kind")),
		metricStatus(attributeValue(item.Attributes[:], "audit.status")),
	}
	if series := e.metricSeries[key]; series != nil {
		return series
	}
	return e.newMetricSeries(key, item.Start)
}

func (e *Exporter) newMetricSeries(key [3]string, start string) *metricSeries {
	// Reserve one of the 256 series for overflow. The reserved tuple
	// retains event totals without keeping arbitrary new label values.
	if len(e.metricSeries) >= metricSeriesCapacity-1 {
		key = [3]string{"__overflow__", "__overflow__", "__overflow__"}
		if series := e.metricSeries[key]; series != nil {
			return series
		}
	}
	series := &metricSeries{attributes: metricAttributes(key), start: start}
	e.metricSeries[key] = series
	return series
}

func (s *metricSeries) observeDuration(item *span) {
	startNano, startErr := strconv.ParseInt(item.Start, 10, 64)
	endNano, endErr := strconv.ParseInt(item.End, 10, 64)
	if startErr == nil && endErr == nil && endNano >= startNano &&
		(!s.hasDuration || endNano >= s.latestEnd) {
		s.duration = (endNano - startNano) / int64(time.Millisecond)
		s.latestEnd = endNano
		s.hasDuration = true
	}
}

func metricPoints(active []*metricSeries) ([]numberPoint, []numberPoint) {
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
	return events, durations
}

func metricRejection(data []byte, points int) (int, int, error) {
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
		if !metadataChar(c) {
			return ""
		}
	}
	return value
}

func metadataChar(c rune) bool {
	return isASCIILetter(c) || isASCIIDigit(c) || c == '_' || c == '-' || c == '.'
}

func isASCIILetter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isASCIIDigit(c rune) bool {
	return c >= '0' && c <= '9'
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
			Attributes []attribute `json:"attributes"`
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
	AsInt      string      `json:"asInt,omitempty"`
	AsDouble   *float64    `json:"asDouble,omitempty"`
}

type sumMetric struct {
	AggregationTemporality int           `json:"aggregationTemporality"`
	IsMonotonic            bool          `json:"isMonotonic"`
	DataPoints             []numberPoint `json:"dataPoints"`
}

type gaugeMetric struct {
	DataPoints []numberPoint `json:"dataPoints"`
}

type histogramPoint struct {
	Attributes     []attribute `json:"attributes,omitempty"`
	Start          string      `json:"startTimeUnixNano"`
	Time           string      `json:"timeUnixNano"`
	Count          string      `json:"count"`
	Sum            float64     `json:"sum"`
	BucketCounts   []string    `json:"bucketCounts"`
	ExplicitBounds []float64   `json:"explicitBounds"`
}

type histogramMetric struct {
	AggregationTemporality int              `json:"aggregationTemporality"`
	DataPoints             []histogramPoint `json:"dataPoints"`
}

type metric struct {
	Name      string           `json:"name"`
	Unit      string           `json:"unit,omitempty"`
	Sum       *sumMetric       `json:"sum,omitempty"`
	Gauge     *gaugeMetric     `json:"gauge,omitempty"`
	Histogram *histogramMetric `json:"histogram,omitempty"`
}

type metricsRequest struct {
	ResourceMetrics [1]struct {
		Resource struct {
			Attributes []attribute `json:"attributes"`
		} `json:"resource"`
		ScopeMetrics []scopeMetrics `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type scopeMetrics struct {
	Scope struct {
		Name string `json:"name"`
	} `json:"scope"`
	Metrics []metric `json:"metrics"`
}

func scopeName(name string) (scope struct {
	Name string `json:"name"`
}) {
	scope.Name = name
	return scope
}
