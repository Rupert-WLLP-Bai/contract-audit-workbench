package model

import (
	"net/http"
	"strings"
	"testing"
)

// A System One payload of exactly 64 KiB is accepted; one byte more is not.
func TestMarshalSystemOnePayloadSizeBoundary(t *testing.T) {
	payload := systemOnePayload{
		Model: "model", Questions: map[string]systemOneQuestion{"candidate_0_relevance": {Type: "noul"}},
	}
	base, err := marshalSystemOnePayload(payload)
	if err != nil {
		t.Fatalf("base payload: %v", err)
	}
	padding := maxSystemOneRequestBytes - len(base)
	if padding <= 0 {
		t.Fatalf("unexpected base payload length %d", len(base))
	}
	payload.State = strings.Repeat("x", padding)
	body, err := marshalSystemOnePayload(payload)
	if err != nil || len(body) != maxSystemOneRequestBytes {
		t.Fatalf("exact-size payload: len=%d err=%v", len(body), err)
	}
	payload.State += "x"
	if _, err := marshalSystemOnePayload(payload); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

// A System One response of exactly 128 KiB is read and parsed; one byte more
// is refused by the size guard rather than by the parser.
func TestReadSystemOneResponseSizeBoundaries(t *testing.T) {
	client := New(Config{})
	_, _, _, err := client.readSystemOneResponse(
		response(http.StatusOK, strings.Repeat(" ", maxSystemOneResponseBytes+1)), "model", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds the size limit") {
		t.Fatalf("oversized response error = %v", err)
	}
	_, _, _, err = client.readSystemOneResponse(
		response(http.StatusOK, strings.Repeat(" ", maxSystemOneResponseBytes)), "model", nil)
	if err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("exact-size response error = %v", err)
	}
}
