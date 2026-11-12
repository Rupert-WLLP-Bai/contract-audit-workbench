// File metadata (manifest mirror):
// Purpose: Projects durable metadata events through bounded OpenTelemetry SDK providers and instruments, preserving sampling, stable identity, shutdown and honest delivery statistics.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-11
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

// Package telemetry exports a bounded, metadata-only projection of durable events.
package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"contractaudit/internal/execution"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otlpmetric "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	spanQueueCapacity    = 256
	spanBatchCapacity    = 64
	responseLimit        = 64 << 10
	metricSeriesCapacity = 256
	exportTimeout        = 2 * time.Second
	exportFlushTimeout   = 3 * time.Second
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

// Stats reports only outcomes the SDK or this package can identify. The SDK
// does not expose batch-queue evictions. The HTTP boundary parses official
// protobuf acknowledgements so valid partial-success rejections remain exact;
// pending or unconfirmed exports remain Unknown.
type Stats struct {
	Exported       uint64
	Dropped        uint64
	FailedRequests uint64
	SampledOut     uint64
	// Unknown is the number of sampled, eligible records not acknowledged or explicitly
	// rejected. It can include spans still queued in the SDK, spans dropped
	// from the SDK batch queue, and exports without a valid acknowledgement.
	Unknown uint64

	MetricsExported uint64
	// MetricsFailed counts points explicitly rejected by a valid partial
	// acknowledgement.
	MetricsFailed uint64
	// MetricsUnknown counts data-point attempts in exports with no complete
	// acknowledgement. A later cumulative collection may resend those values.
	MetricsUnknown       uint64
	MetricFailedRequests uint64
	// HTTPDropped counts HTTP RED observations that exceeded the bounded
	// metric series allocation. They never block or fail the exchange.
	HTTPDropped uint64
}

type Exporter struct {
	ratio float64

	client         *http.Client
	traceProvider  *sdktrace.TracerProvider
	metricProvider *sdkmetric.MeterProvider
	tracer         trace.Tracer

	executionEvents metric.Int64Counter
	executionDur    metric.Int64ObservableGauge
	durationMu      sync.Mutex
	durations       map[[3]string]executionDuration
	durationReg     metric.Registration

	httpRequests    metric.Int64Counter
	httpDurations   metric.Int64Histogram
	httpMu          sync.Mutex
	httpRequestSet  map[httpCounterKey]struct{}
	httpDurationSet map[[2]string]struct{}

	runtime *runtimeMetrics

	mu      sync.Mutex
	closed  bool
	close   sync.Once
	closing atomic.Bool

	recorded        atomic.Uint64
	exported        atomic.Uint64
	dropped         atomic.Uint64
	failed          atomic.Uint64
	sampled         atomic.Uint64
	metricsExported atomic.Uint64
	metricsFailed   atomic.Uint64
	metricsUnknown  atomic.Uint64
	metricFailed    atomic.Uint64
	httpDropped     atomic.Uint64
}

type executionDuration struct {
	at      time.Time
	valueMS int64
}

type spanIDsContextKey struct{}

type spanIDs struct {
	trace trace.TraceID
	span  trace.SpanID
}

type deterministicIDGenerator struct{}

func (deterministicIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	if ids, ok := ctx.Value(spanIDsContextKey{}).(spanIDs); ok {
		return ids.trace, ids.span
	}
	var ids spanIDs
	_, _ = rand.Read(ids.trace[:])
	_, _ = rand.Read(ids.span[:])
	if !ids.trace.IsValid() {
		ids.trace[15] = 1
	}
	if !ids.span.IsValid() {
		ids.span[7] = 1
	}
	return ids.trace, ids.span
}

func (g deterministicIDGenerator) NewSpanID(ctx context.Context, _ trace.TraceID) trace.SpanID {
	if ids, ok := ctx.Value(spanIDsContextKey{}).(spanIDs); ok {
		return ids.span
	}
	_, span := g.NewIDs(ctx)
	return span
}

type projectedSpan struct {
	sdktrace.ReadOnlySpan
	resource *resource.Resource
}

func (s projectedSpan) Resource() *resource.Resource { return s.resource }

type projectedTraceExporter struct {
	next     sdktrace.SpanExporter
	resource *resource.Resource
	owner    *Exporter
}

func (e *projectedTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	projected := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		projected[i] = projectedSpan{ReadOnlySpan: span, resource: e.resource}
	}
	observation := &exportObservation{kind: traceExport, expected: uint64(len(spans))}
	ctx = context.WithValue(ctx, exportObservationContextKey{}, observation)
	if err := e.next.ExportSpans(ctx, projected); err != nil {
		if !observation.valid {
			e.owner.failed.Add(1)
			return errors.New("OTLP trace export failed")
		}
	}
	if !observation.valid {
		e.owner.failed.Add(1)
		return errors.New("OTLP trace acknowledgement was not confirmed")
	}
	accepted := observation.expected - observation.rejected
	e.owner.exported.Add(accepted)
	e.owner.dropped.Add(observation.rejected)
	return nil
}

func (e *projectedTraceExporter) Shutdown(ctx context.Context) error {
	if err := e.next.Shutdown(ctx); err != nil {
		return errors.New("OTLP trace exporter shutdown failed")
	}
	return nil
}

type projectedMetricExporter struct {
	next     sdkmetric.Exporter
	resource *resource.Resource
	owner    *Exporter
}

func (e *projectedMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.next.Temporality(kind)
}

func (e *projectedMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.next.Aggregation(kind)
}

func (e *projectedMetricExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	points := metricPointCount(data)
	projected := *data
	projected.Resource = e.resource
	observation := &exportObservation{kind: metricExport, expected: points}
	ctx = context.WithValue(ctx, exportObservationContextKey{}, observation)
	if err := e.next.Export(ctx, &projected); err != nil {
		if !observation.valid {
			e.owner.metricFailed.Add(1)
			e.owner.metricsUnknown.Add(points)
			return errors.New("OTLP metric export failed")
		}
	}
	if !observation.valid {
		e.owner.metricFailed.Add(1)
		e.owner.metricsUnknown.Add(points)
		return errors.New("OTLP metric acknowledgement was not confirmed")
	}
	e.owner.metricsExported.Add(observation.expected - observation.rejected)
	e.owner.metricsFailed.Add(observation.rejected)
	return nil
}

func (e *projectedMetricExporter) ForceFlush(ctx context.Context) error {
	if err := e.next.ForceFlush(ctx); err != nil {
		return errors.New("OTLP metric exporter flush failed")
	}
	return nil
}

func (e *projectedMetricExporter) Shutdown(ctx context.Context) error {
	if err := e.next.Shutdown(ctx); err != nil {
		return errors.New("OTLP metric exporter shutdown failed")
	}
	return nil
}

func metricPointCount(data *metricdata.ResourceMetrics) uint64 {
	var count uint64
	for _, scope := range data.ScopeMetrics {
		for _, item := range scope.Metrics {
			switch points := item.Data.(type) {
			case metricdata.Gauge[int64]:
				count += uint64(len(points.DataPoints))
			case metricdata.Gauge[float64]:
				count += uint64(len(points.DataPoints))
			case metricdata.Sum[int64]:
				count += uint64(len(points.DataPoints))
			case metricdata.Sum[float64]:
				count += uint64(len(points.DataPoints))
			case metricdata.Histogram[int64]:
				count += uint64(len(points.DataPoints))
			case metricdata.Histogram[float64]:
				count += uint64(len(points.DataPoints))
			case metricdata.ExponentialHistogram[int64]:
				count += uint64(len(points.DataPoints))
			case metricdata.ExponentialHistogram[float64]:
				count += uint64(len(points.DataPoints))
			case metricdata.Summary:
				count += uint64(len(points.DataPoints))
			}
		}
	}
	return count
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
	instance := metadata(cfg.InstanceID)
	if instance == "" {
		instance = service
	}
	traces, metrics := otlpEndpoints(u)
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	baseTransport.Proxy = nil
	client := &http.Client{
		Transport: observingTransport{next: baseTransport},
		Timeout:   exportTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	identity := []attribute.KeyValue{
		attribute.String("service.name", service),
		attribute.String("service.instance.id", instance),
	}
	if environment != "" {
		identity = append(identity, attribute.String("deployment.environment.name", environment))
	}
	identityResource := resource.NewSchemaless(identity...)

	traceExporter, err := otlptrace.New(context.Background(),
		otlptrace.WithEndpointURL(traces),
		otlptrace.WithHTTPClient(client),
		otlptrace.WithHeaders(map[string]string{}),
		otlptrace.WithCompression(otlptrace.NoCompression),
		otlptrace.WithRetry(otlptrace.RetryConfig{Enabled: false}),
		otlptrace.WithTimeout(exportTimeout),
		otlptrace.WithMaxResponseSize(responseLimit),
		otlptrace.WithEncoding(otlptrace.EncodingProtobuf),
	)
	if err != nil {
		client.CloseIdleConnections()
		return nil, errors.New("OTLP trace exporter configuration failed")
	}
	e := &Exporter{
		ratio:  cfg.SampleRatio,
		client: client, durations: make(map[[3]string]executionDuration),
		httpRequestSet: make(map[httpCounterKey]struct{}), httpDurationSet: make(map[[2]string]struct{}),
	}
	tracePipeline := &projectedTraceExporter{next: traceExporter, resource: identityResource, owner: e}
	e.traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithResource(identityResource),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithIDGenerator(deterministicIDGenerator{}),
		sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
			AttributeValueLengthLimit: 128, AttributeValueDepthLimit: 64, AttributeCountLimit: 6,
			EventCountLimit: 0, LinkCountLimit: 0,
			AttributePerEventCountLimit: 0, AttributePerLinkCountLimit: 0,
		}),
		sdktrace.WithBatcher(tracePipeline,
			sdktrace.WithMaxQueueSize(spanQueueCapacity),
			sdktrace.WithMaxExportBatchSize(spanBatchCapacity),
			sdktrace.WithBatchTimeout(500*time.Millisecond),
			sdktrace.WithExportTimeout(exportTimeout),
		),
	)
	e.tracer = e.traceProvider.Tracer("contractaudit.execution")

	metricExporter, err := otlpmetric.New(context.Background(),
		otlpmetric.WithEndpointURL(metrics),
		otlpmetric.WithHTTPClient(client),
		otlpmetric.WithHeaders(map[string]string{}),
		otlpmetric.WithCompression(otlpmetric.NoCompression),
		otlpmetric.WithRetry(otlpmetric.RetryConfig{Enabled: false}),
		otlpmetric.WithTimeout(exportTimeout),
		otlpmetric.WithMaxResponseSize(responseLimit),
		otlpmetric.WithTemporalitySelector(sdkmetric.CumulativeTemporalitySelector),
	)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), exportFlushTimeout)
		_ = e.traceProvider.Shutdown(ctx)
		cancel()
		client.CloseIdleConnections()
		return nil, errors.New("OTLP metric exporter configuration failed")
	}
	metricReader := sdkmetric.NewPeriodicReader(&projectedMetricExporter{
		next: metricExporter, resource: identityResource, owner: e,
	}, sdkmetric.WithInterval(httpFlushInterval), sdkmetric.WithTimeout(exportTimeout))
	views := []sdkmetric.View{
		sdkmetric.NewView(sdkmetric.Instrument{Name: "audit.http.duration_ms"}, sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: append([]float64(nil), httpDurationBounds[:]...), NoMinMax: true,
			},
		}),
		sdkmetric.NewView(sdkmetric.Instrument{Name: "audit.queue.wait_duration_ms"}, sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: append([]float64(nil), queueDurationBounds[:]...), NoMinMax: true,
			},
		}),
		sdkmetric.NewView(sdkmetric.Instrument{Name: "audit.queue.execution_duration_ms"}, sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: append([]float64(nil), queueDurationBounds[:]...), NoMinMax: true,
			},
		}),
	}
	e.metricProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(identityResource),
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithView(views...),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
		sdkmetric.WithCardinalityLimit(metricSeriesCapacity),
	)
	if err := e.configureExecutionMetrics(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), exportFlushTimeout)
		_ = e.metricProvider.Shutdown(ctx)
		_ = e.traceProvider.Shutdown(ctx)
		cancel()
		client.CloseIdleConnections()
		return nil, errors.New("OTLP metric instruments could not be configured")
	}
	if err := e.configureHTTPMetrics(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), exportFlushTimeout)
		_ = e.metricProvider.Shutdown(ctx)
		_ = e.traceProvider.Shutdown(ctx)
		cancel()
		client.CloseIdleConnections()
		return nil, errors.New("OTLP HTTP metrics could not be configured")
	}
	if err := e.configureRuntimeMetrics(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), exportFlushTimeout)
		_ = e.metricProvider.Shutdown(ctx)
		_ = e.traceProvider.Shutdown(ctx)
		cancel()
		client.CloseIdleConnections()
		return nil, errors.New("OTLP runtime metrics could not be configured")
	}
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

func (e *Exporter) configureExecutionMetrics() error {
	meter := e.metricProvider.Meter("contractaudit.execution")
	var err error
	e.executionEvents, err = meter.Int64Counter("audit.execution.events")
	if err != nil {
		return err
	}
	e.executionDur, err = meter.Int64ObservableGauge("audit.execution.duration_ms", metric.WithUnit("ms"))
	if err != nil {
		return err
	}
	e.durationReg, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		e.durationMu.Lock()
		defer e.durationMu.Unlock()
		for key, value := range e.durations {
			observer.ObserveInt64(e.executionDur, value.valueMS,
				metric.WithAttributes(executionAttributes(key)...))
		}
		return nil
	}, e.executionDur)
	return err
}

func executionAttributes(values [3]string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(values))
	for i, key := range [3]string{"audit.phase", "audit.node_kind", "audit.status"} {
		attrs = append(attrs, attribute.String(key, values[i]))
	}
	return attrs
}

func (e *Exporter) Record(runID, attemptID string, event execution.Event) {
	if !recordableEvent(e, event) {
		return
	}
	traceSeed := sha256.Sum256([]byte(attemptID))
	if e.ratio < 1 && float64(binary.BigEndian.Uint64(traceSeed[:8]))/18446744073709551616.0 >= e.ratio {
		e.sampled.Add(1)
		return
	}

	e.mu.Lock()
	e.recorded.Add(1)
	if e.closed {
		e.dropped.Add(1)
		e.mu.Unlock()
		return
	}
	e.recordExecutionMetrics(event)
	e.recordSpan(runID, attemptID, event, traceSeed)
	e.mu.Unlock()
}

func recordableEvent(e *Exporter, event execution.Event) bool {
	return e != nil && event.DurationMS != nil && *event.DurationMS >= 0 && !event.At.IsZero() && event.SpanID != ""
}

func (e *Exporter) recordExecutionMetrics(event execution.Event) {
	key := [3]string{
		metricPhase(event.Phase),
		metricNodeKind(event.NodeKind),
		metricStatus(event.Status),
	}
	e.durationMu.Lock()
	if _, ok := e.durations[key]; !ok && len(e.durations) >= metricSeriesCapacity-1 {
		key = [3]string{"__overflow__", "__overflow__", "__overflow__"}
	}
	if current, ok := e.durations[key]; !ok || !event.At.Before(current.at) {
		e.durations[key] = executionDuration{at: event.At, valueMS: *event.DurationMS}
	}
	e.durationMu.Unlock()
	e.executionEvents.Add(context.Background(), 1, metric.WithAttributes(executionAttributes(key)...))
}

func (e *Exporter) recordSpan(runID, attemptID string, event execution.Event, seed [32]byte) {
	var traceID trace.TraceID
	var spanID trace.SpanID
	copy(traceID[:], seed[:16])
	spanSeed := sha256.Sum256([]byte(event.SpanID))
	copy(spanID[:], spanSeed[:8])
	ids := spanIDs{trace: traceID, span: spanID}
	ctx := context.WithValue(context.Background(), spanIDsContextKey{}, ids)
	if event.ParentSpanID != "" && event.ParentSpanID != attemptID {
		parentSeed := sha256.Sum256([]byte(event.ParentSpanID))
		var parentID trace.SpanID
		copy(parentID[:], parentSeed[:8])
		parent := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: parentID, TraceFlags: trace.FlagsSampled, Remote: true,
		})
		ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
	}
	start := event.At.Add(-time.Duration(*event.DurationMS) * time.Millisecond)
	_, span := e.tracer.Start(ctx, metadata(event.Phase),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithTimestamp(start),
		trace.WithAttributes(
			attribute.String("audit.run_id", metadata(runID)),
			attribute.String("audit.attempt_id", metadata(attemptID)),
			attribute.String("audit.phase", metadata(event.Phase)),
			attribute.String("audit.node_kind", metadata(event.NodeKind)),
			attribute.String("audit.status", metadata(event.Status)),
			attribute.String("audit.error_code", metadata(event.ErrorCode)),
		),
	)
	switch event.Status {
	case "completed":
		span.SetStatus(codes.Ok, "")
	case "failed", "cancelled":
		span.SetStatus(codes.Error, "")
	}
	span.End(trace.WithTimestamp(event.At))
}

func (e *Exporter) Stats() Stats {
	if e == nil {
		return Stats{}
	}
	recorded, exported := e.recorded.Load(), e.exported.Load()
	unknown := uint64(0)
	resolved := exported + e.dropped.Load()
	if recorded > resolved {
		unknown = recorded - resolved
	}
	return Stats{
		Exported: exported, Dropped: e.dropped.Load(), FailedRequests: e.failed.Load(),
		SampledOut: e.sampled.Load(), Unknown: unknown,
		MetricsExported: e.metricsExported.Load(), MetricsFailed: e.metricsFailed.Load(),
		MetricsUnknown: e.metricsUnknown.Load(), MetricFailedRequests: e.metricFailed.Load(),
		HTTPDropped: e.httpDropped.Load(),
	}
}

// Close drains the SDK processors within three seconds. Export failures never
// become application shutdown errors.
func (e *Exporter) Close() {
	if e == nil {
		return
	}
	e.close.Do(func() {
		e.closing.Store(true)
		e.mu.Lock()
		e.closed = true
		e.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), exportFlushTimeout)
		defer cancel()
		done := make(chan struct{}, 2)
		go func() {
			_ = e.traceProvider.Shutdown(ctx)
			done <- struct{}{}
		}()
		go func() {
			_ = e.metricProvider.Shutdown(ctx)
			done <- struct{}{}
		}()
		timer := time.NewTimer(exportFlushTimeout)
		defer timer.Stop()
		for i := 0; i < 2; i++ {
			select {
			case <-done:
			case <-ctx.Done():
				i = 2
			case <-timer.C:
				i = 2
			}
		}
		e.client.CloseIdleConnections()
		stats := e.Stats()
		log.Printf("OTLP metadata exporter stopped: exported=%d unknown=%d dropped=%d failed_requests=%d sampled_out=%d",
			stats.Exported, stats.Unknown, stats.Dropped, stats.FailedRequests, stats.SampledOut)
	})
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

func isASCIIDigit(c rune) bool { return c >= '0' && c <= '9' }
