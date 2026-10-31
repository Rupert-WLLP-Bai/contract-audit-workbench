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

type httpObservation struct {
	method     string
	route      string
	status     int
	durationMS int64
}

func httpCounterKey(o httpObservation) [3]string {
	return [3]string{o.method, o.route, strconv.Itoa(o.status)}
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
	start := strconv.FormatInt(now, 10)
	e.httpMu.Lock()
	defer e.httpMu.Unlock()
	counter, ok := e.httpRequests[httpCounterKey(o)]
	if !ok && len(e.httpRequests) >= httpSeriesCapacity {
		e.httpDropped.Add(1)
		return
	}
	if !ok {
		counter = &metricSeries{attributes: []attribute{
			stringAttribute("http.request.method", o.method),
			stringAttribute("http.route", o.route),
			stringAttribute("http.response.status_code", strconv.Itoa(o.status)),
		}, start: start}
		e.httpRequests[httpCounterKey(o)] = counter
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
		duration = &metricSeries{attributes: []attribute{
			stringAttribute("http.request.method", o.method),
			stringAttribute("http.route", o.route),
		}, start: start}
		e.httpDurations[httpDurationKey(o)] = duration
	}
	duration.duration = o.durationMS
	duration.hasDuration = true
	duration.latestEnd = now
	duration.active = true
}

// drainHTTPMetrics snapshots and clears the active flag of every HTTP series
// that observed exchanges since the last send. The snapshot runs under the
// producer mutex, so producers either see the pre-send or the post-send state
// and no observation is lost between copy and reset.
func (e *Exporter) drainHTTPMetrics() ([]numberPoint, []numberPoint) {
	now := time.Now().UnixNano()
	e.httpMu.Lock()
	counters := make([]*metricSeries, 0, len(e.httpRequests))
	for _, series := range e.httpRequests {
		if series.active {
			counters = append(counters, series)
		}
	}
	durations := make([]*metricSeries, 0, len(e.httpDurations))
	for _, series := range e.httpDurations {
		if series.active {
			durations = append(durations, series)
		}
	}
	events := make([]numberPoint, 0, len(counters))
	points := make([]numberPoint, 0, len(durations))
	for _, series := range counters {
		series.active = false
		// Collection timestamps advance monotonically per series even when
		// observations arrive out of order across concurrent requests.
		at := max(now, series.lastTime+1)
		series.lastTime = at
		events = append(events, numberPoint{Attributes: series.attributes, Start: series.start,
			Time: strconv.FormatInt(at, 10), AsInt: strconv.FormatInt(series.count, 10)})
	}
	for _, series := range durations {
		series.active = false
		if !series.hasDuration {
			continue
		}
		at := max(now, series.latestEnd, series.lastTime+1)
		series.lastTime = at
		points = append(points, numberPoint{Attributes: series.attributes,
			Time: strconv.FormatInt(series.latestEnd, 10), AsInt: strconv.FormatInt(series.duration, 10)})
	}
	e.httpMu.Unlock()
	return events, points
}

// hasHTTPObservations reports whether any exchange was recorded but not yet
// sent, so the periodic flush posts a metrics payload only when one exists.
func (e *Exporter) hasHTTPObservations() bool {
	e.httpMu.Lock()
	defer e.httpMu.Unlock()
	for _, series := range e.httpRequests {
		if series.active {
			return true
		}
	}
	for _, series := range e.httpDurations {
		if series.active {
			return true
		}
	}
	return false
}

// flushHTTPMetrics sends a metrics-only payload for exchanges recorded since
// the last delivery. It keeps the same accounting rules as the span-driven
// path: send failures never fail the process and never lose durable records.
func (e *Exporter) flushHTTPMetrics() {
	if e == nil || e.ctx.Err() != nil || !e.hasHTTPObservations() {
		return
	}
	points, rejected, err := e.sendMetrics(nil)
	if err != nil {
		rejected = points
	}
	e.metricsExported.Add(uint64(points - rejected))
	if rejected > 0 && e.metricsFailed.Add(uint64(rejected)) == uint64(rejected) {
		log.Print("OTLP metadata metric export failed; durable business records are unaffected")
	}
}
