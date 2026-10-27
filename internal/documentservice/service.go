package documentservice

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

const maxPrepareRequestBytes = 1 << 20

// Config bounds independent parse and OCR work plus the total number of active
// and waiting requests.
type Config struct {
	WorkerCredential string
	CPUConcurrency   int
	OCRConcurrency   int
	QueueCapacity    int
	ParseTimeout     time.Duration
}

// FromEnv reads only the document service's internal worker credential and
// bounded parse capacity settings.
func FromEnv() (Config, error) {
	config := Config{
		WorkerCredential: strings.TrimSpace(os.Getenv("WORKER_DOCUMENT_TOKEN")),
		CPUConcurrency:   4,
		OCRConcurrency:   1,
		QueueCapacity:    32,
		ParseTimeout:     2 * time.Minute,
	}
	for name, destination := range map[string]*int{
		"DOCUMENT_CPU_CONCURRENCY": &config.CPUConcurrency,
		"DOCUMENT_OCR_CONCURRENCY": &config.OCRConcurrency,
		"DOCUMENT_QUEUE_CAPACITY":  &config.QueueCapacity,
	} {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1024 {
			return Config{}, errors.New(name + " must be an integer in 1..1024")
		}
		*destination = value
	}
	if raw := strings.TrimSpace(os.Getenv("DOCUMENT_PARSE_TIMEOUT")); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 || value > 30*time.Minute {
			return Config{}, errors.New("DOCUMENT_PARSE_TIMEOUT must be a positive duration no longer than 30m")
		}
		config.ParseTimeout = value
	}
	return config, nil
}

// Service owns the public Document routes and authenticated delegated prepare
// endpoint for the Worker.
type Service struct {
	store  *store.Store
	config Config
	queue  chan struct{}
	cpu    chan struct{}
	ocr    chan struct{}
}

func New(st *store.Store, config Config) (*Service, error) {
	if st == nil || len(config.WorkerCredential) < 32 || config.CPUConcurrency < 1 || config.OCRConcurrency < 1 ||
		config.QueueCapacity < 1 || config.ParseTimeout <= 0 {
		return nil, errors.New("document service requires a Store, private worker credential, and positive bounded capacities")
	}
	return &Service{
		store: st, config: config, queue: make(chan struct{}, config.QueueCapacity),
		cpu: make(chan struct{}, config.CPUConcurrency), ocr: make(chan struct{}, config.OCRConcurrency),
	}, nil
}

type publicServiceContextKey struct{}

// WithPublicParse applies this document service's shared queue, parser capacity,
// and timeout to one authenticated public mutation. The resolver checks current
// authority and classifies server-side metadata before queue admission; the
// mutation must recheck authority when it commits.
func WithPublicParse(ctx context.Context, resolveKind func(context.Context) (string, error), work func(context.Context) error) error {
	if ctx == nil || resolveKind == nil || work == nil {
		return errors.New("public document parse operation is incomplete")
	}
	service, _ := ctx.Value(publicServiceContextKey{}).(*Service)
	if service == nil {
		kind, err := resolveKind(ctx)
		if err != nil {
			return err
		}
		if kind != "cpu" && kind != "ocr" && kind != "none" {
			return errors.New("public document parse kind is invalid")
		}
		return work(ctx)
	}
	return service.withPublicParse(ctx, resolveKind, work)
}

// Handler mounts delegated preparation separately from the Main-owned public
// service route filter. Public handlers opt into the shared parser capacity at
// their authorized mutation boundary through WithPublicParse.
func (s *Service) Handler(public http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/prepare", s.prepare)
	mux.HandleFunc("/health/live", healthLive)
	mux.HandleFunc("/health/ready", s.healthReady)
	if public == nil {
		public = http.NotFoundHandler()
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), publicServiceContextKey{}, s)
		public.ServeHTTP(w, r.WithContext(ctx))
	}))
	return mux
}

func (s *Service) withPublicParse(parent context.Context, resolveKind func(context.Context) (string, error), work func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, s.config.ParseTimeout)
	defer cancel()
	kind, err := resolveKind(ctx)
	if err == nil && kind != "cpu" && kind != "ocr" && kind != "none" {
		err = errors.New("public document parse kind is invalid")
	}
	if err == nil {
		err = s.withQueue(ctx, func(ctx context.Context) error {
			return s.withParser(ctx, kind, work)
		})
	}
	if errors.Is(err, errParseCapacityFull) {
		return fault.New(http.StatusServiceUnavailable, "document_capacity", "材料解析容量已满，请稍后重试")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fault.New(http.StatusGatewayTimeout, "document_timeout", "材料解析超时")
	}
	return err
}

var errParseCapacityFull = errors.New("document parse capacity is full")

func (s *Service) withQueue(ctx context.Context, work func(context.Context) error) error {
	select {
	case s.queue <- struct{}{}:
		defer func() { <-s.queue }()
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errParseCapacityFull
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return work(ctx)
}

func (s *Service) withParser(ctx context.Context, kind string, work func(context.Context) error) error {
	var gate chan struct{}
	switch kind {
	case "none":
		return work(ctx)
	case "cpu":
		gate = s.cpu
	case "ocr":
		gate = s.ocr
	default:
		return errors.New("document parse kind is invalid")
	}
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return work(ctx)
}

func healthLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "document"})
}

func (s *Service) healthReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.store.DocumentReady(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "service": "document"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": "document"})
}

func (s *Service) prepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	values := r.Header.Values("X-Contract-Service-Key")
	if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(s.config.WorkerCredential)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service authentication required", "code": "service_unauthenticated"})
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxPrepareRequestBytes)
	defer body.Close()
	var request store.PrepareDocumentRequest
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid preparation request"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid preparation request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.config.ParseTimeout)
	defer cancel()
	classified := false
	var frozen store.FrozenDocument
	err := s.withQueue(ctx, func(ctx context.Context) error {
		kind, err := s.store.DocumentPreparationKind(ctx, request)
		if err != nil {
			return err
		}
		classified = true
		return s.withParser(ctx, kind, func(ctx context.Context) error {
			var err error
			frozen, err = s.store.PrepareAttemptDocument(ctx, request)
			return err
		})
	})
	if err != nil {
		if errors.Is(err, errParseCapacityFull) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "document preparation capacity is full"})
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writePrepareContextError(w, err)
			return
		}
		if !classified {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "current delegated preparation authority is unavailable"})
			return
		}
		writePrepareContextError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, frozen)
}

func writePrepareContextError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "document preparation timed out"})
		return
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": "document preparation failed"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
