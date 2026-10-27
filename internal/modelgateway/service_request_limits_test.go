package modelgateway

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/store"
)

// gatewayTrackedBody records how much of a request body a handler consumed so a
// test can prove a rejected request never buffered the payload.
type gatewayTrackedBody struct {
	source io.Reader
	reads  int
	bytes  int64
	closed bool
}

func (b *gatewayTrackedBody) Read(p []byte) (int, error) {
	b.reads++
	n, err := b.source.Read(p)
	b.bytes += int64(n)
	return n, err
}

func (b *gatewayTrackedBody) Close() error {
	b.closed = true
	return nil
}

// gatewayPaddingReader repeats one small block for as long as the caller reads,
// so an oversized fixture is described without allocating it.
type gatewayPaddingReader string

func (r gatewayPaddingReader) Read(p []byte) (int, error) {
	return copy(p, r), nil
}

// A request that declares more bytes than the gateway accepts is invalid on its
// own terms, so the gateway must refuse it without buffering the body at all.
func TestDecodeRequestRejectsDeclaredOversizeWithoutReading(t *testing.T) {
	body := &gatewayTrackedBody{source: strings.NewReader("must not be read")}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.ContentLength = maxRequestBytes + 1
	request.Header.Set("X-Audit-Execution-Scope", "{}")

	_, _, _, _, err := decodeRequest(httptest.NewRecorder(), request)
	if err == nil || err.Error() != "request body is invalid" {
		t.Fatalf("declared oversized body error = %v", err)
	}
	if body.reads != 0 || body.bytes != 0 {
		t.Fatalf("declared oversized body was read: reads=%d bytes=%d", body.reads, body.bytes)
	}
	if !body.closed {
		t.Fatal("declared oversized body was not released")
	}
}

// A body declared exactly at the limit is still subject to the ordinary
// validation, so the early refusal must not become an off-by-one rejection.
func TestDecodeRequestAtDeclaredLimitStillValidates(t *testing.T) {
	for _, item := range []struct {
		name      string
		payload   string
		scope     string
		wantError bool
	}{
		{name: "valid", payload: `{"model":"ocr","max_tokens":1}`, scope: "{}"},
		{name: "invalid JSON", payload: "{", scope: "{}", wantError: true},
		{name: "unknown scope field", payload: `{"model":"ocr","max_tokens":1}`, scope: `{"unexpected":true}`, wantError: true},
	} {
		t.Run(item.name, func(t *testing.T) {
			body := &gatewayTrackedBody{source: strings.NewReader(item.payload)}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
			request.ContentLength = maxRequestBytes
			request.Header.Set("X-Audit-Execution-Scope", item.scope)

			payload, _, _, _, err := decodeRequest(httptest.NewRecorder(), request)
			if (err != nil) != item.wantError {
				t.Fatalf("declared boundary body validation error = %v", err)
			}
			if !item.wantError && string(payload) != item.payload {
				t.Fatalf("decoded body = %q", payload)
			}
			if body.reads == 0 {
				t.Fatal("declared boundary body was refused before validation")
			}
			if !body.closed {
				t.Fatal("declared boundary body was not released")
			}
		})
	}
}

// Requests without a declared length cannot be refused from headers alone, so
// the streaming limit must still bound them at the same maximum.
func TestDecodeRequestRejectsUnknownLengthOversize(t *testing.T) {
	const prefix = `{"model":"ocr","max_tokens":1,"image":"`
	const suffix = `"}`
	padding := maxRequestBytes + 1 - len(prefix) - len(suffix)
	body := &gatewayTrackedBody{source: io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(gatewayPaddingReader(strings.Repeat("A", 32<<10)), int64(padding)),
		strings.NewReader(suffix),
	)}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.ContentLength = -1
	request.Header.Set("X-Audit-Execution-Scope", "{}")

	_, _, _, _, err := decodeRequest(httptest.NewRecorder(), request)
	if err == nil || err.Error() != "request body is invalid" {
		t.Fatalf("unknown-length oversized body error = %v", err)
	}
	// The limit may probe one byte past the maximum to prove the body is
	// longer, but it must stop there instead of draining the whole stream.
	if body.bytes < int64(maxRequestBytes) || body.bytes > int64(maxRequestBytes)+1 {
		t.Fatalf("unknown-length oversized body consumed %d bytes", body.bytes)
	}
	if !body.closed {
		t.Fatal("unknown-length oversized body was not released")
	}
}

// Over real HTTP, a declared oversize must be answered from the headers: the
// server replies before the client streams any of the payload and then closes
// the connection instead of waiting for bytes it will never accept.
func TestGatewayRefusesDeclaredOversizeBeforeTheBodyArrives(t *testing.T) {
	config := gatewayTestConfig("openai-compatible", "")
	service, err := New(&store.Store{}, config)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	t.Cleanup(server.Close)

	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nContent-Type: application/json\r\n"+
		"X-Contract-Service-Key: %s\r\nX-Audit-Execution-Scope: {}\r\nExpect: 100-continue\r\n\r\n",
		server.Listener.Addr(), maxRequestBytes+1, config.DocumentCredential)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(responseBody), "invalid OpenAI-compatible request") {
		t.Fatalf("early rejection status=%d body=%s", response.StatusCode, responseBody)
	}
	switch _, err := reader.ReadByte(); {
	case err == nil:
		t.Fatal("server sent unexpected bytes after refusing the request")
	case errors.Is(err, io.EOF):
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatalf("server waited for the refused body instead of closing: %v", err)
		}
	}
}
