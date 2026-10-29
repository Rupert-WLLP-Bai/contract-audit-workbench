// Package externalread implements the authorized, read-only Mock OSS connector.
//
// The connector performs exactly one GET against a deployment-configured base
// URL. Callers supply an object identifier, never a URL, method, header or
// credential. Every read is bounded in time and response size. Transport and
// source failures are turned into unavailable or rejected observations with a
// reason code so that downstream verification can only conclude "insufficient";
// they are never reported as a successful read.
package externalread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"contractaudit/internal/auditext"
)

const (
	// DefaultMaxResponseBytes bounds one record response when Config leaves
	// MaxResponseBytes unset.
	DefaultMaxResponseBytes int64 = 1 << 20

	// MaxResponseBytesLimit is the largest configurable response bound.
	MaxResponseBytesLimit int64 = 4 << 20

	// DefaultPayloadPrefix is the synthetic payload location used when Config
	// leaves PayloadPrefix unset.
	DefaultPayloadPrefix = "fixture://oss/"

	// DefaultTimeout bounds one read when Config leaves Timeout unset.
	DefaultTimeout = 5 * time.Second

	recordsSegment    = "records"
	snapshotSeparator = "\x00"
)

// reason codes emitted by this connector. They are stable identifiers; the
// verifier maps them to evidence-insufficient outcomes rather than success.
const (
	reasonReadSucceeded          = "read_succeeded"
	reasonSourceStale            = "source_stale"
	reasonSourceTimeout          = "source_timeout"
	reasonSourceUnavailable      = "source_unavailable"
	reasonSourceNotFound         = "source_not_found"
	reasonSourceRateLimited      = "source_rate_limited"
	reasonSourceRedirectRejected = "source_redirect_rejected"
	reasonSourceResponseTooLarge = "source_response_too_large"
	reasonSourceResponseInvalid  = "source_response_invalid"
	reasonSourceUpdatedAtInvalid = "source_updated_at_invalid"
	reasonRecordVersionInvalid   = "record_version_invalid"
	reasonExternalObjectMismatch = "external_object_mismatch"
	reasonObjectNotAllowed       = "external_object_not_allowed"
	reasonRequiredFieldMissing   = "required_field_missing"
	reasonEffectiveIntervalMiss  = "effective_interval_missing"
)

// urlSchemeSeparator marks a caller-supplied absolute URL. Object ids must be
// identifiers, so any scheme separator is rejected even though PathEscape
// would otherwise keep it inside the configured path.
const urlSchemeSeparator = "://"

// Config is the deployment-owned connector configuration. BaseURL fixes the
// only endpoint the connector will ever call; nothing in a read request can
// change it.
type Config struct {
	ConnectorID      string
	BaseURL          string
	SourceSystem     string
	AllowedObjects   []string
	PayloadPrefix    string
	Freshness        time.Duration
	Timeout          time.Duration
	MaxResponseBytes int64
	HTTPClient       *http.Client
	Clock            func() time.Time
}

// Connector is an immutable, concurrency-safe read-only connector.
type Connector struct {
	id               string
	baseURL          *url.URL
	sourceSystem     string
	allowedObjects   map[string]struct{}
	payloadPrefix    string
	freshness        time.Duration
	timeout          time.Duration
	maxResponseBytes int64
	client           *http.Client
	clock            func() time.Time
}

var _ auditext.ReadConnector = (*Connector)(nil)

// New validates the deployment configuration and builds a connector.
func New(cfg Config) (*Connector, error) {
	if err := validateConnectorIdentity(cfg); err != nil {
		return nil, err
	}
	base, err := parseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	allowed, err := allowedObjectSet(cfg.AllowedObjects)
	if err != nil {
		return nil, err
	}
	prefix, err := configuredPayloadPrefix(cfg.PayloadPrefix)
	if err != nil {
		return nil, err
	}
	maxResponseBytes, err := configuredResponseLimit(cfg.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	timeout, freshness := configuredReadTiming(cfg.Timeout, cfg.Freshness)
	client := &http.Client{}
	if cfg.HTTPClient != nil {
		copied := *cfg.HTTPClient
		client = &copied
	}
	// Never follow redirects: a redirect would escape the deployment-pinned
	// endpoint. ErrUseLastResponse returns the 3xx response unmodified.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Connector{
		id:               cfg.ConnectorID,
		baseURL:          base,
		sourceSystem:     cfg.SourceSystem,
		allowedObjects:   allowed,
		payloadPrefix:    prefix,
		freshness:        freshness,
		timeout:          timeout,
		maxResponseBytes: maxResponseBytes,
		client:           client,
		clock:            clock,
	}, nil
}

func configuredReadTiming(timeout, freshness time.Duration) (time.Duration, time.Duration) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if freshness < 0 {
		freshness = 0
	}
	return timeout, freshness
}

func validateConnectorIdentity(cfg Config) error {
	if err := auditext.ValidateID(cfg.ConnectorID); err != nil {
		return fmt.Errorf("%w: connector id", auditext.ErrInvalidRequest)
	}
	if err := auditext.ValidateID(cfg.SourceSystem); err != nil {
		return fmt.Errorf("%w: source system", auditext.ErrInvalidRequest)
	}
	return nil
}

func allowedObjectSet(objectIDs []string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{}, len(objectIDs))
	for _, objectID := range objectIDs {
		if !validObjectID(objectID) {
			return nil, fmt.Errorf("%w: allowed object", auditext.ErrInvalidRequest)
		}
		allowed[objectID] = struct{}{}
	}
	return allowed, nil
}

func configuredPayloadPrefix(prefix string) (string, error) {
	if prefix == "" {
		prefix = DefaultPayloadPrefix
	}
	if len(prefix)+sha256.Size*2 > 512 {
		return "", fmt.Errorf("%w: payload prefix", auditext.ErrInvalidRequest)
	}
	return prefix, nil
}

func configuredResponseLimit(limit int64) (int64, error) {
	if limit < 0 || limit > MaxResponseBytesLimit {
		return 0, fmt.Errorf("%w: max response bytes", auditext.ErrInvalidRequest)
	}
	if limit == 0 {
		return DefaultMaxResponseBytes, nil
	}
	return limit, nil
}

// ID returns the registered connector identifier.
func (c *Connector) ID() string { return c.id }

// Read performs one bounded read-only GET and returns a normalized observation.
//
// Invalid requests and source authorization failures return an error. Source
// transport failures and unusable records return an observation with
// availability unavailable/rejected and a nil error, so callers cannot mistake
// them for evidence.
func (c *Connector) Read(ctx context.Context, req auditext.ReadRequest) (auditext.Observation, error) {
	if err := validateReadRequest(c, req); err != nil {
		return auditext.Observation{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	observedAt := c.clock()
	if len(c.allowedObjects) > 0 {
		if _, ok := c.allowedObjects[req.ExternalObjectID]; !ok {
			return c.observation(req, observedAt, nil, nil, auditext.AvailabilityRejected, reasonObjectNotAllowed, false)
		}
	}
	result, err := c.readSourceRecord(ctx, req, observedAt)
	if err != nil {
		return auditext.Observation{}, err
	}
	return c.observation(req, observedAt, result.data, result.payload, result.availability, result.reason, result.withPayload)
}

func validateReadRequest(c *Connector, req auditext.ReadRequest) error {
	if err := req.Scope.Validate(); err != nil {
		return fmt.Errorf("%w: authorized scope", auditext.ErrInvalidRequest)
	}
	if req.ConnectorID != c.id {
		return fmt.Errorf("%w: connector id", auditext.ErrInvalidRequest)
	}
	if err := auditext.ValidateID(req.BindingVersion); err != nil {
		return fmt.Errorf("%w: binding version", auditext.ErrInvalidRequest)
	}
	if !validObjectID(req.ExternalObjectID) {
		return fmt.Errorf("%w: external object id", auditext.ErrInvalidRequest)
	}
	return nil
}

type sourceReadResult struct {
	data         *recordData
	payload      []byte
	availability auditext.Availability
	reason       string
	withPayload  bool
}

func (c *Connector) readSourceRecord(ctx context.Context, req auditext.ReadRequest, observedAt time.Time) (sourceReadResult, error) {
	callCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.endpoint(req.ExternalObjectID), nil)
	if err != nil {
		return sourceReadResult{}, fmt.Errorf("%w: connector request", auditext.ErrInvalidRequest)
	}
	// No caller credentials exist on the request and none are added here.
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return failedSourceRead(auditext.AvailabilityUnavailable, reasonForError(callCtx, err)), nil
	}
	defer resp.Body.Close()
	statusResult, err := sourceStatusResult(resp.StatusCode)
	if err != nil {
		// Do not surface an unauthorized response body or source metadata.
		return sourceReadResult{}, err
	}
	if statusResult.reason != "" {
		return statusResult, nil
	}
	payload, bodyFailure := readSourcePayload(resp, c.maxResponseBytes, callCtx)
	if bodyFailure.reason != "" {
		return bodyFailure, nil
	}
	data, ok := parseRecord(payload)
	if !ok {
		return failedSourceRead(auditext.AvailabilityRejected, reasonSourceResponseInvalid), nil
	}
	return normalizeSourceRecord(data, payload, req, observedAt, c.freshness), nil
}

func failedSourceRead(availability auditext.Availability, reason string) sourceReadResult {
	return sourceReadResult{availability: availability, reason: reason}
}

func sourceStatusResult(status int) (sourceReadResult, error) {
	if status >= 300 && status < 400 {
		return failedSourceRead(auditext.AvailabilityRejected, reasonSourceRedirectRejected), nil
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return sourceReadResult{}, auditext.ErrUnauthorized
	}
	if result, matched := knownUnavailableStatus(status); matched {
		return result, nil
	}
	if status >= 500 || status < 200 || status >= 300 {
		return failedSourceRead(auditext.AvailabilityUnavailable, reasonSourceUnavailable), nil
	}
	return sourceReadResult{}, nil
}

func knownUnavailableStatus(status int) (sourceReadResult, bool) {
	switch status {
	case http.StatusNotFound, http.StatusGone:
		return failedSourceRead(auditext.AvailabilityUnavailable, reasonSourceNotFound), true
	case http.StatusTooManyRequests:
		return failedSourceRead(auditext.AvailabilityUnavailable, reasonSourceRateLimited), true
	case http.StatusRequestTimeout:
		return failedSourceRead(auditext.AvailabilityUnavailable, reasonSourceTimeout), true
	}
	return sourceReadResult{}, false
}

func readSourcePayload(resp *http.Response, maxBytes int64, ctx context.Context) ([]byte, sourceReadResult) {
	if resp.ContentLength > maxBytes {
		return nil, failedSourceRead(auditext.AvailabilityRejected, reasonSourceResponseTooLarge)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, failedSourceRead(auditext.AvailabilityUnavailable, reasonForError(ctx, err))
	}
	if int64(len(payload)) > maxBytes {
		return nil, failedSourceRead(auditext.AvailabilityRejected, reasonSourceResponseTooLarge)
	}
	return payload, sourceReadResult{}
}

func normalizeSourceRecord(data *recordData, payload []byte, req auditext.ReadRequest, observedAt time.Time, freshness time.Duration) sourceReadResult {
	if reason := rejectedRecordReason(data, req.ExternalObjectID); reason != "" {
		return sourceReadResult{data: data, payload: payload, availability: auditext.AvailabilityRejected, reason: reason}
	}

	availability := auditext.AvailabilityAvailable
	reason := reasonReadSucceeded
	if freshness > 0 && data.hasUpdatedAt && observedAt.Sub(data.updatedAt) > freshness {
		availability = auditext.AvailabilityStale
		reason = reasonSourceStale
	}
	return sourceReadResult{data: data, payload: payload, availability: availability, reason: reason, withPayload: true}
}

func rejectedRecordReason(data *recordData, externalObjectID string) string {
	switch {
	case !data.recordIDValid || data.recordID != externalObjectID:
		return reasonExternalObjectMismatch
	case data.versionInvalid:
		return reasonRecordVersionInvalid
	case data.intervalInvalid:
		return reasonEffectiveIntervalMiss
	case data.updatedAtInvalid:
		return reasonSourceUpdatedAtInvalid
	case !data.hasField:
		return reasonRequiredFieldMissing
	default:
		return ""
	}
}

// observation assembles and validates one observation. Tenant and case always
// come from the server-bound scope, never from the response body.
func (c *Connector) observation(
	req auditext.ReadRequest,
	observedAt time.Time,
	data *recordData,
	payload []byte,
	availability auditext.Availability,
	reason string,
	withPayload bool,
) (auditext.Observation, error) {
	recordID := req.ExternalObjectID
	recordVersion := ""
	observation := auditext.Observation{
		SchemaVersion:        auditext.ExternalObservationSchema,
		TenantID:             req.Scope.TenantID,
		CaseID:               req.Scope.CaseID,
		ConnectorID:          c.id,
		SourceSystem:         c.sourceSystem,
		ObjectBindingVersion: req.BindingVersion,
		ExternalObjectID:     req.ExternalObjectID,
		ObservedAt:           observedAt,
		// A failed read has no source-declared interval. Validate requires a
		// non-empty ordered interval, so use a fixed sentinel that cannot
		// cover any business instant.
		EffectiveFrom:  noIntervalFrom,
		EffectiveUntil: noIntervalUntil,
		Availability:   availability,
		ReasonCode:     reason,
		DataClass:      auditext.DataClassSynthetic,
	}
	if data != nil {
		recordID, recordVersion = applyRecordMetadata(&observation, recordID, data, availability)
	}
	observation.RecordID = recordID
	observation.SnapshotID = c.snapshotID(req.ExternalObjectID, recordID, recordVersion, observedAt, payload)
	if withPayload {
		payloadRef := c.payloadPrefix + observation.SnapshotID
		observation.PayloadRef = &payloadRef
	}
	if err := observation.Validate(req.Scope); err != nil {
		return auditext.Observation{}, err
	}
	return observation, nil
}

func applyRecordMetadata(observation *auditext.Observation, recordID string, data *recordData, availability auditext.Availability) (string, string) {
	if data.recordIDValid {
		recordID = data.recordID
	}
	recordVersion := ""
	if data.hasVersion {
		version := data.recordVersion
		observation.RecordVersion = &version
		recordVersion = data.recordVersion
	}
	if data.hasUpdatedAt {
		updatedAt := data.updatedAt
		observation.SourceUpdatedAt = &updatedAt
	}
	// Only a usable (available or stale) snapshot may carry the source's
	// validity interval; a rejected record must not appear to cover any
	// business instant.
	if data.hasInterval && (availability == auditext.AvailabilityAvailable || availability == auditext.AvailabilityStale) {
		observation.EffectiveFrom = data.effectiveFrom
		observation.EffectiveUntil = data.effectiveUntil
	}
	if data.hasField && availability != auditext.AvailabilityRejected {
		observation.Fields = []auditext.Field{data.field}
	}
	return recordID, recordVersion
}

// snapshotID is deterministic over the connector, object, record identity,
// observation instant and exact raw payload bytes.
func (c *Connector) snapshotID(externalObjectID, recordID, recordVersion string, observedAt time.Time, payload []byte) string {
	hash := sha256.New()
	for _, part := range []string{
		c.id,
		externalObjectID,
		recordID,
		recordVersion,
		observedAt.UTC().Format(time.RFC3339Nano),
	} {
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, snapshotSeparator)
	}
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil))
}

// endpoint builds the only request URL: base path + /records/{escaped id}.
// RawPath keeps the escaped identifier so an object id containing "/" stays a
// single path segment instead of becoming an extra path element.
func (c *Connector) endpoint(objectID string) string {
	endpoint := *c.baseURL
	basePath := strings.TrimRight(endpoint.Path, "/")
	baseEscapedPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	endpoint.Path = basePath + "/" + recordsSegment + "/" + objectID
	endpoint.RawPath = baseEscapedPath + "/" + recordsSegment + "/" + url.PathEscape(objectID)
	endpoint.RawQuery = ""
	endpoint.ForceQuery = false
	endpoint.Fragment = ""
	endpoint.RawFragment = ""
	endpoint.User = nil
	return endpoint.String()
}

func reasonForError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return reasonSourceTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return reasonSourceTimeout
	}
	return reasonSourceUnavailable
}

// validObjectID enforces the frozen identifier grammar and additionally
// rejects absolute URLs and traversal segments. The id is still escaped when
// building the path; this check keeps obviously hostile values out early.
func validObjectID(objectID string) bool {
	if auditext.ValidateID(objectID) != nil {
		return false
	}
	if strings.Contains(objectID, urlSchemeSeparator) {
		return false
	}
	for _, segment := range strings.Split(objectID, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func parseBaseURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: base url", auditext.ErrInvalidRequest)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: base url", auditext.ErrInvalidRequest)
	}
	if !supportedBaseURLScheme(parsed.Scheme) {
		return nil, fmt.Errorf("%w: base url scheme", auditext.ErrInvalidRequest)
	}
	if err := validateBaseURLAuthority(parsed); err != nil {
		return nil, err
	}
	if err := validateBaseURLSuffix(parsed, raw); err != nil {
		return nil, err
	}
	return parsed, nil
}

func supportedBaseURLScheme(scheme string) bool {
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}

func validateBaseURLAuthority(parsed *url.URL) error {
	if parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" {
		return fmt.Errorf("%w: base url host", auditext.ErrInvalidRequest)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: base url userinfo", auditext.ErrInvalidRequest)
	}
	return nil
}

func validateBaseURLSuffix(parsed *url.URL, raw string) error {
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("%w: base url query", auditext.ErrInvalidRequest)
	}
	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: base url fragment", auditext.ErrInvalidRequest)
	}
	return nil
}
