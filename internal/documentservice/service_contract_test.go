package documentservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func TestFromEnvRejectsUnsafeParseBounds(t *testing.T) {
	for _, name := range []string{
		"WORKER_DOCUMENT_TOKEN", "CORE_DOCUMENT_TOKEN", "DOCUMENT_CPU_CONCURRENCY", "DOCUMENT_OCR_CONCURRENCY",
		"DOCUMENT_QUEUE_CAPACITY", "DOCUMENT_PARSE_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("d", 32))

	for _, test := range []struct {
		name, value string
	}{
		{"DOCUMENT_CPU_CONCURRENCY", "0"},
		{"DOCUMENT_CPU_CONCURRENCY", "1025"},
		{"DOCUMENT_OCR_CONCURRENCY", "many"},
		{"DOCUMENT_QUEUE_CAPACITY", "-1"},
		{"DOCUMENT_PARSE_TIMEOUT", "0s"},
		{"DOCUMENT_PARSE_TIMEOUT", "31m"},
		{"DOCUMENT_PARSE_TIMEOUT", "not-a-duration"},
	} {
		t.Run(test.name+"="+test.value, func(t *testing.T) {
			for _, name := range []string{"DOCUMENT_CPU_CONCURRENCY", "DOCUMENT_OCR_CONCURRENCY", "DOCUMENT_QUEUE_CAPACITY", "DOCUMENT_PARSE_TIMEOUT"} {
				t.Setenv(name, "")
			}
			t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("d", 32))
			t.Setenv(test.name, test.value)
			config, err := FromEnv()
			if err == nil {
				t.Fatalf("unsafe %s=%q was accepted", test.name, test.value)
			}
			if config != (Config{}) {
				t.Fatalf("invalid environment returned a partial config: %+v", config)
			}
		})
	}
}

func TestFromEnvDefaultsAndAcceptsInclusiveBounds(t *testing.T) {
	for _, name := range []string{
		"WORKER_DOCUMENT_TOKEN", "CORE_DOCUMENT_TOKEN", "DOCUMENT_CPU_CONCURRENCY", "DOCUMENT_OCR_CONCURRENCY",
		"DOCUMENT_QUEUE_CAPACITY", "DOCUMENT_PARSE_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("WORKER_DOCUMENT_TOKEN", "  "+strings.Repeat("d", 32)+"  ")
	defaults, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.WorkerCredential != strings.Repeat("d", 32) || defaults.CPUConcurrency != 4 ||
		defaults.OCRConcurrency != 1 || defaults.QueueCapacity != 32 || defaults.ParseTimeout != 2*time.Minute {
		t.Fatalf("default config = %+v", defaults)
	}

	for _, tc := range []struct {
		name  string
		value string
		check func(Config) bool
	}{
		{"DOCUMENT_CPU_CONCURRENCY", "1", func(c Config) bool { return c.CPUConcurrency == 1 }},
		{"DOCUMENT_CPU_CONCURRENCY", "1024", func(c Config) bool { return c.CPUConcurrency == 1024 }},
		{"DOCUMENT_OCR_CONCURRENCY", "1", func(c Config) bool { return c.OCRConcurrency == 1 }},
		{"DOCUMENT_OCR_CONCURRENCY", "1024", func(c Config) bool { return c.OCRConcurrency == 1024 }},
		{"DOCUMENT_QUEUE_CAPACITY", "1", func(c Config) bool { return c.QueueCapacity == 1 }},
		{"DOCUMENT_QUEUE_CAPACITY", "1024", func(c Config) bool { return c.QueueCapacity == 1024 }},
		{"DOCUMENT_PARSE_TIMEOUT", "1ns", func(c Config) bool { return c.ParseTimeout == time.Nanosecond }},
		{"DOCUMENT_PARSE_TIMEOUT", "30m", func(c Config) bool { return c.ParseTimeout == 30*time.Minute }},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			for _, name := range []string{"DOCUMENT_CPU_CONCURRENCY", "DOCUMENT_OCR_CONCURRENCY", "DOCUMENT_QUEUE_CAPACITY", "DOCUMENT_PARSE_TIMEOUT"} {
				t.Setenv(name, "")
			}
			t.Setenv("WORKER_DOCUMENT_TOKEN", strings.Repeat("d", 32))
			t.Setenv(tc.name, tc.value)
			got, err := FromEnv()
			if err != nil || !tc.check(got) {
				t.Fatalf("FromEnv = %+v, %v; requested %s=%s", got, err, tc.name, tc.value)
			}
		})
	}
}

func TestNewRequiresAStorePrivateCredentialAndPositiveCapacities(t *testing.T) {
	valid := Config{WorkerCredential: strings.Repeat("d", 32), CPUConcurrency: 2, OCRConcurrency: 1, QueueCapacity: 3, ParseTimeout: time.Second}
	cases := []struct {
		name   string
		store  *store.Store
		config Config
	}{
		{"missing store", nil, valid},
		{"short delegated credential", &store.Store{}, func() Config { c := valid; c.WorkerCredential = "short"; return c }()},
		{"zero cpu permits", &store.Store{}, func() Config { c := valid; c.CPUConcurrency = 0; return c }()},
		{"zero ocr permits", &store.Store{}, func() Config { c := valid; c.OCRConcurrency = 0; return c }()},
		{"zero queue capacity", &store.Store{}, func() Config { c := valid; c.QueueCapacity = 0; return c }()},
		{"zero parse timeout", &store.Store{}, func() Config { c := valid; c.ParseTimeout = 0; return c }()},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.store, test.config); err == nil {
				t.Fatal("incomplete document service was constructed")
			}
		})
	}
}

func TestPublicParseFallbackValidatesKindAndPropagatesWorkErrors(t *testing.T) {
	called := 0
	workFailure := errors.New("authorized operation failed")
	for _, kind := range []string{"cpu", "ocr", "none"} {
		calledBefore := called
		err := WithPublicParse(context.Background(), func(context.Context) (string, error) { return kind, nil }, func(context.Context) error {
			called++
			return nil
		})
		if err != nil || called != calledBefore+1 {
			t.Fatalf("unwrapped %s work result: calls=%d err=%v", kind, called, err)
		}
	}
	if err := WithPublicParse(context.Background(), nil, func(context.Context) error { return nil }); err == nil {
		t.Fatal("missing operation resolver was accepted")
	}
	if err := WithPublicParse(context.Background(), func(context.Context) (string, error) { return "none", nil }, nil); err == nil {
		t.Fatal("missing mutation was accepted")
	}
	if err := WithPublicParse(context.Background(), func(context.Context) (string, error) { return "invalid", nil }, func(context.Context) error {
		called++
		return nil
	}); err == nil || called != 3 {
		t.Fatalf("unsupported parse kind reached mutation: calls=%d err=%v", called, err)
	}
	if err := WithPublicParse(context.Background(), func(context.Context) (string, error) { return "none", workFailure }, func(context.Context) error { return nil }); !errors.Is(err, workFailure) {
		t.Fatalf("authorization or classification error was lost: %v", err)
	}
	if err := WithPublicParse(context.Background(), func(context.Context) (string, error) { return "none", nil }, func(context.Context) error { return workFailure }); !errors.Is(err, workFailure) {
		t.Fatalf("mutation error was lost: %v", err)
	}
}

func TestPublicParseCapacityAndDeadlineReturnStableFaults(t *testing.T) {
	service := &Service{
		config: Config{ParseTimeout: 40 * time.Millisecond},
		queue:  make(chan struct{}, 1), cpu: make(chan struct{}, 1), ocr: make(chan struct{}, 1),
	}
	service.queue <- struct{}{}
	ctx := context.WithValue(context.Background(), publicServiceContextKey{}, service)
	err := WithPublicParse(ctx, func(context.Context) (string, error) { return "none", nil }, func(context.Context) error { return nil })
	var failure *fault.Error
	if !errors.As(err, &failure) || failure.Status != http.StatusServiceUnavailable || failure.Code != "document_capacity" {
		t.Fatalf("full parse queue returned %v, want retryable capacity boundary", err)
	}
	<-service.queue

	workStarted := make(chan struct{})
	err = WithPublicParse(ctx, func(context.Context) (string, error) { return "none", nil }, func(ctx context.Context) error {
		close(workStarted)
		<-ctx.Done()
		return ctx.Err()
	})
	<-workStarted
	if !errors.As(err, &failure) || failure.Status != http.StatusGatewayTimeout || failure.Code != "document_timeout" {
		t.Fatalf("expired parser work returned %v, want bounded timeout fault", err)
	}
}

func TestDocumentHealthAndDelegatedMethodErrorsAreStable(t *testing.T) {
	service, err := New(&store.Store{}, Config{WorkerCredential: strings.Repeat("d", 32), CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 1, ParseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler(nil)
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/health/live", http.StatusOK},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed},
		{http.MethodPost, "/health/ready", http.StatusMethodNotAllowed},
		{http.MethodGet, "/internal/v1/prepare", http.StatusMethodNotAllowed},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.status {
			t.Errorf("%s %s answered %d, want %d", test.method, test.path, recorder.Code, test.status)
		}
		if test.path == "/internal/v1/prepare" && recorder.Header().Get("Allow") != http.MethodPost {
			t.Errorf("prepare method error did not advertise POST: %v", recorder.Header())
		}
	}
}

func TestDelegatedHandlerRejectsAmbiguousCredentialsAndMalformedClaimsBeforePersistence(t *testing.T) {
	service, err := New(&store.Store{}, Config{WorkerCredential: strings.Repeat("d", 32), CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 1, ParseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		body   string
		keys   []string
		status int
	}{
		{"missing credential", `{}`, nil, http.StatusUnauthorized},
		{"duplicate credential", `{}`, []string{strings.Repeat("d", 32), strings.Repeat("d", 32)}, http.StatusUnauthorized},
		{"invalid JSON", `{`, []string{strings.Repeat("d", 32)}, http.StatusBadRequest},
		{"unknown claim field", `{"shell":"forbidden"}`, []string{strings.Repeat("d", 32)}, http.StatusBadRequest},
		{"trailing claim object", `{} {}`, []string{strings.Repeat("d", 32)}, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/internal/v1/prepare", strings.NewReader(test.body))
			for _, key := range test.keys {
				req.Header.Add("X-Contract-Service-Key", key)
			}
			recorder := httptest.NewRecorder()
			service.Handler(nil).ServeHTTP(recorder, req)
			if recorder.Code != test.status {
				t.Fatalf("untrusted delegated request answered %d, want %d: %s", recorder.Code, test.status, recorder.Body)
			}
			if recorder.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("delegated error did not use the JSON contract: %v", recorder.Header())
			}
			var body struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Fatalf("delegated error body is not stable JSON: body=%q err=%v", recorder.Body.String(), err)
			}
			if test.status == http.StatusUnauthorized && body.Code != "service_unauthenticated" {
				t.Fatalf("authentication error code=%q, want service_unauthenticated", body.Code)
			}
		})
	}
}

func TestDelegatedPrepareCredentialAndRequestSizeBoundaries(t *testing.T) {
	const credential = "dddddddddddddddddddddddddddddddd"
	service, err := New(&store.Store{}, Config{WorkerCredential: credential, CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 1, ParseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler(nil)
	for _, tc := range []struct {
		name string
		keys []string
		body string
		want int
	}{
		{"wrong credential precedes malformed body", []string{"wrong"}, `{`, http.StatusUnauthorized},
		{"duplicate credential precedes malformed body", []string{credential, credential}, `{`, http.StatusUnauthorized},
		{"malformed request", []string{credential}, `{`, http.StatusBadRequest},
		{"trailing request object", []string{credential}, `{} {}`, http.StatusBadRequest},
		{"oversized request", []string{credential}, `{"generation":1}` + strings.Repeat(" ", maxPrepareRequestBytes), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/internal/v1/prepare", strings.NewReader(tc.body))
			for _, key := range tc.keys {
				req.Header.Add("X-Contract-Service-Key", key)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("prepare answered %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}

	service.queue <- struct{}{}
	for _, body := range []string{
		`{}` + strings.Repeat(" ", maxPrepareRequestBytes-len(`{}`)),
		`{"generation":1}` + strings.Repeat(" ", maxPrepareRequestBytes-len(`{"generation":1}`)),
	} {
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/prepare", strings.NewReader(body))
		req.Header.Set("X-Contract-Service-Key", credential)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "capacity is full") {
			t.Fatalf("exact-size request answered %d %q, want capacity response", recorder.Code, recorder.Body.String())
		}
	}
}

func TestDelegatedPrepareCallerCancellationDoesNotEnterStore(t *testing.T) {
	const credential = "dddddddddddddddddddddddddddddddd"
	service, err := New(&store.Store{}, Config{WorkerCredential: credential, CPUConcurrency: 1, OCRConcurrency: 1, QueueCapacity: 1, ParseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/prepare", strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("X-Contract-Service-Key", credential)
	recorder := httptest.NewRecorder()
	service.Handler(nil).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("canceled prepare wrote status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestPrepareContextFailuresMapToTimeoutAndHideStoreDetails(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code int
	}{
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"store failure", errors.New("postgres password=secret"), http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writePrepareContextError(recorder, test.err)
			if recorder.Code != test.code || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("preparation failure answered %d %q", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	writePrepareContextError(recorder, context.Canceled)
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("canceled delegated request wrote a response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
