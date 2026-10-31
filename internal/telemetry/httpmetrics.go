package telemetry

import (
	"log"
	"strconv"
	"time"
)

// The HTTP RED series extend the same bounded, metadata-only projection the
// execution exporter already maintains: rate and duration per bounded route
// template, with the response status only on the rate series. No case, user,
// tenant, worker, or any other high-cardinality business identifier is part of
// a series key, and nothing here replaces the durable audit events.

const (
	// httpSeriesCapacity bounds how many distinct method/route/status
	// combinations the boundary may allocate. A deployment serving more shapes
	// than the bound keeps answering, and the excess observations are counted
	// as dropped instead of growing memory.
	httpSeriesCapacity = 256
)

// Explicit, inclusive upper bounds in milliseconds. The final bucket is +Inf.
var httpDurationBounds = [...]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}

type httpHistogram struct {
	attributes []attribute
	start      string
	lastTime   int64
	count      uint64
	sum        float64
	buckets    [len(httpDurationBounds) + 1]uint64
	active     bool
}

type httpObservation struct {
	method     string
	route      string
	status     int
	durationMS int64
}

type httpCounterKey struct {
	method string
	route  string
	status int
}

func httpDurationKey(o httpObservation) [2]string {
	return [2]string{o.method, o.route}
}

// observeHTTP records one completed HTTP exchange. It never blocks on the
// delivery goroutine or the collector: a bounded mutex around map access is
// the only shared cost, mirroring the nonblocking queue path of Record.
func (e *Exporter) observeHTTP(o httpObservation) {
	if e == nil || o.status < 100 || o.status > 599 || o.durationMS < 0 {
		return
	}
	if o.method = httpMethodAttribute(o.method); o.method == "" {
		return
	}
	if o.route = sanitizeRoute(o.route); o.route == "" {
		o.route = "unmatched"
	}
	now := time.Now().UnixNano()
	e.httpMu.Lock()
	defer e.httpMu.Unlock()
	key := httpCounterKey{method: o.method, route: o.route, status: o.status}
	counter, ok := e.httpRequests[key]
	if !ok && len(e.httpRequests) >= httpSeriesCapacity {
		e.httpDropped.Add(1)
		return
	}
	if !ok {
		counter = &metricSeries{attributes: []attribute{
			stringAttribute("http.request.method", o.method),
			stringAttribute("http.route", o.route),
			stringAttribute("http.response.status_code", strconv.Itoa(o.status)),
		}, start: strconv.FormatInt(now, 10)}
		e.httpRequests[key] = counter
	}
	counter.count++
	counter.latestEnd = now
	counter.active = true
	duration, ok := e.httpDurations[httpDurationKey(o)]
	if !ok && len(e.httpDurations) >= httpSeriesCapacity {
		e.httpDropped.Add(1)
		return
	}
	if !ok {
		duration = &httpHistogram{attributes: []attribute{
			stringAttribute("http.request.method", o.method),
			stringAttribute("http.route", o.route),
		}, start: strconv.FormatInt(now, 10)}
		e.httpDurations[httpDurationKey(o)] = duration
	}
	duration.count++
	duration.sum += float64(o.durationMS)
	bucket := 0
	for bucket < len(httpDurationBounds) && float64(o.durationMS) > httpDurationBounds[bucket] {
		bucket++
	}
	duration.buckets[bucket]++
	duration.active = true
}

// drainHTTPMetrics snapshots and clears the active flag of every HTTP series
// that observed exchanges since the last send. The snapshot runs under the
// producer mutex, so producers either see the pre-send or the post-send state
// and no observation is lost between copy and reset.
func (e *Exporter) drainHTTPMetrics() ([]numberPoint, []histogramPoint) {
	now := time.Now().UnixNano()
	e.httpMu.Lock()
	events := make([]numberPoint, 0, len(e.httpRequests))
	points := make([]histogramPoint, 0, len(e.httpDurations))
	for _, series := range e.httpRequests {
		if !series.active {
			continue
		}
		series.active = false
		// Collection timestamps advance monotonically per series even when
		// observations arrive out of order across concurrent requests.
		at := max(now, series.lastTime+1)
		series.lastTime = at
		events = append(events, numberPoint{Attributes: series.attributes, Start: series.start,
			Time: strconv.FormatInt(at, 10), AsInt: strconv.FormatInt(series.count, 10)})
	}
	for _, series := range e.httpDurations {
		if !series.active {
			continue
		}
		series.active = false
		at := max(now, series.lastTime+1)
		series.lastTime = at
		buckets := make([]string, len(series.buckets))
		for i, count := range series.buckets {
			buckets[i] = strconv.FormatUint(count, 10)
		}
		points = append(points, histogramPoint{Attributes: series.attributes, Start: series.start,
			Time: strconv.FormatInt(at, 10), Count: strconv.FormatUint(series.count, 10), Sum: series.sum,
			BucketCounts: buckets, ExplicitBounds: httpDurationBounds[:]})
	}
	e.httpMu.Unlock()
	return events, points
}

// flushMetrics sends pending HTTP observations and, on the periodic tick only,
// collects runtime state. Shutdown exports counters without starting a new query.
func (e *Exporter) flushMetrics(collectRuntime bool) {
	if e == nil || e.ctx.Err() != nil {
		return
	}
	points, rejected, err := e.sendMetrics(nil, e.collectRuntimeMetrics(collectRuntime))
	if err != nil {
		rejected = points
	}
	e.metricsExported.Add(uint64(points - rejected))
	if rejected > 0 && e.metricsFailed.Add(uint64(rejected)) == uint64(rejected) {
		log.Print("OTLP metadata metric export failed; durable business records are unaffected")
	}
}
