package telemetry

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"

	metricresponse "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traceresponse "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type exportKind uint8

const (
	traceExport exportKind = iota + 1
	metricExport
)

type exportObservationContextKey struct{}

type exportObservation struct {
	kind     exportKind
	expected uint64
	rejected uint64
	valid    bool
}

type observingTransport struct {
	next http.RoundTripper
}

func (t observingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.next.RoundTrip(request)
	if err != nil || response == nil {
		return response, err
	}
	observation, _ := request.Context().Value(exportObservationContextKey{}).(*exportObservation)
	if observation == nil {
		return response, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	_ = response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	if readErr != nil || len(data) > responseLimit {
		response.StatusCode = http.StatusBadGateway
		return response, nil
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || response.StatusCode != http.StatusOK || mediaType != "application/x-protobuf" {
		response.StatusCode = http.StatusBadGateway
		return response, nil
	}
	rejected, valid := parseExportAcknowledgement(observation.kind, data, observation.expected)
	if !valid {
		response.StatusCode = http.StatusBadGateway
		return response, nil
	}
	observation.rejected = rejected
	observation.valid = true
	return response, nil
}

func parseExportAcknowledgement(kind exportKind, data []byte, expected uint64) (uint64, bool) {
	switch kind {
	case traceExport:
		response := new(traceresponse.ExportTraceServiceResponse)
		if err := proto.Unmarshal(data, response); err != nil {
			return 0, false
		}
		if response.PartialSuccess == nil {
			return 0, true
		}
		rejected := uint64(response.PartialSuccess.RejectedSpans)
		if rejected > expected || (rejected == 0 && response.PartialSuccess.ErrorMessage != "") {
			return 0, false
		}
		return rejected, true
	case metricExport:
		response := new(metricresponse.ExportMetricsServiceResponse)
		if err := proto.Unmarshal(data, response); err != nil {
			return 0, false
		}
		if response.PartialSuccess == nil {
			return 0, true
		}
		rejected := uint64(response.PartialSuccess.RejectedDataPoints)
		if rejected > expected || (rejected == 0 && response.PartialSuccess.ErrorMessage != "") {
			return 0, false
		}
		return rejected, true
	default:
		return 0, false
	}
}

var _ http.RoundTripper = observingTransport{}
