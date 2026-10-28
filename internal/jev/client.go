// Package jev calls TypeSafe's hosted Jev System One endpoint.
//
// Jev returns typed, calibrated answers to bounded questions about state the
// caller supplies. This package exposes one choice question and batched Noul
// questions, and treats every transport or validation failure as an error,
// never as an answer. It does not resolve case authorization and never turns an
// answer into an audit conclusion: callers must authorize the request and keep
// the answer suggestion-only.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultBaseURL is the official TypeSafe API origin. The client appends the
	// /v1/systemone path.
	DefaultBaseURL = "https://api.typesafe.ai"
	// DefaultModel is the stable Jev alias. The response reports the resolved
	// version, which callers should record instead of assuming this alias.
	DefaultModel = "jev-latest"

	DefaultTimeout = 10 * time.Second
	MaxTimeout     = 30 * time.Second

	MaxStateBytes               = 32 * 1024
	MaxResponseBytes            = 128 * 1024
	MaxInstructionsBytes        = 2048
	MaxCriteria                 = 64
	MaxQuestions                = 16
	MaxCandidateIDBytes         = 80
	MaxCandidateDescriptionSize = 512
)

var (
	ErrInvalidInput         = errors.New("invalid jev input")
	ErrRequest              = errors.New("jev request failed")
	ErrHTTPStatus           = errors.New("jev upstream status")
	ErrRedirect             = errors.New("jev redirect refused")
	ErrOversizeResponse     = errors.New("jev response too large")
	ErrInvalidJSON          = errors.New("jev response is not valid json")
	ErrInvalidAnswer        = errors.New("jev answer is not a usable choice")
	ErrInvalidProbabilities = errors.New("jev probabilities are invalid")
)

var (
	modelPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	questionPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	candidatePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
)

// Config identifies one TypeSafe account and model. APIKey is sent only in the
// Authorization header and is never returned in an error or an answer.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// Usage contains only counters reported by the provider. A nil counter means
// the provider did not report it; zero is preserved as zero.
type Usage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
}

// Answer is one typed choice. Probabilities sum to one over every criterion the
// caller supplied, and Confidence is either the provider's reported value or the
// probability of the selected option.
type Answer struct {
	Choice           string             `json:"choice"`
	Probabilities    map[string]float64 `json:"probabilities"`
	Confidence       float64            `json:"confidence"`
	ConfidenceSource string             `json:"confidence_source"`
	Model            string             `json:"model"`
	Usage            Usage              `json:"usage"`
}

// NoulCriteria is the optional true/false boundary supplied with a Noul. Both
// descriptions are concrete situations, not negations of each other.
type NoulCriteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

// NoulQuestion asks for the probability that one bounded statement is true.
// Questions in one AskNouls request share state but are answered independently.
type NoulQuestion struct {
	Instructions string
	Criteria     NoulCriteria
}

// NoulAnswer is one probability. Model and Usage are copied from the response
// envelope so a caller cannot accidentally use the requested model as the
// observed one.
type NoulAnswer struct {
	Noul  float64
	Model string
	Usage Usage
}

// ResponseMeta is the provider-reported identity and usage for one request.
type ResponseMeta struct {
	Model string
	Usage Usage
}

type Client struct {
	endpoint string
	apiKey   string
	model    string
	http     *http.Client
}

// Validate checks the endpoint, key, model and timeout without making a
// network request. Health and configuration surfaces use it to distinguish an
// enabled provider from an invalid one without creating a client.
func (c Config) Validate() error {
	_, err := c.normalized()
	return err
}

func (c Config) normalized() (Config, error) {
	endpoint, err := validateEndpoint(c.BaseURL)
	if err != nil {
		return Config{}, err
	}
	key := strings.TrimSpace(c.APIKey)
	if err := validateAPIKey(key); err != nil {
		return Config{}, err
	}
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = DefaultModel
	}
	if !modelPattern.MatchString(model) {
		return Config{}, errors.New("jev model name is invalid")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 || timeout > MaxTimeout {
		return Config{}, errors.New("jev timeout is outside the supported range")
	}
	return Config{BaseURL: endpoint, APIKey: key, Model: model, Timeout: timeout}, nil
}

// New validates the endpoint, key, model and timeout. A clear-text endpoint is
// accepted only on loopback, so a key cannot be sent over plain HTTP to a remote
// host. The official endpoint is HTTPS.
func New(config Config) (*Client, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxConnsPerHost:       4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: normalized.Timeout,
		TLSHandshakeTimeout:   5 * time.Second,
	}
	return &Client{
		endpoint: normalized.BaseURL,
		apiKey:   normalized.APIKey,
		model:    normalized.Model,
		http: &http.Client{
			Transport: transport,
			Timeout:   normalized.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Model returns the configured request alias, not the observed response model.
func (c *Client) Model() string { return c.model }

// ValidateInput applies the same bounds Choose enforces, so callers can reject
// an oversized or malformed request before a network call.
func ValidateInput(state, questionID, instructions string, criteria map[string]string) error {
	if !utf8.ValidString(state) || len(state) == 0 || len(state) > MaxStateBytes {
		return fmt.Errorf("%w: state must be valid utf-8 within the size limit", ErrInvalidInput)
	}
	if !questionPattern.MatchString(questionID) {
		return fmt.Errorf("%w: question id is invalid", ErrInvalidInput)
	}
	if !utf8.ValidString(instructions) || len(instructions) == 0 || len(instructions) > MaxInstructionsBytes {
		return fmt.Errorf("%w: instructions must be valid utf-8 within the size limit", ErrInvalidInput)
	}
	if len(criteria) < 2 || len(criteria) > MaxCriteria {
		return fmt.Errorf("%w: criteria count is outside the supported range", ErrInvalidInput)
	}
	for id, description := range criteria {
		if !candidatePattern.MatchString(id) || len(id) > MaxCandidateIDBytes {
			return fmt.Errorf("%w: criterion id is invalid", ErrInvalidInput)
		}
		if !utf8.ValidString(description) || strings.TrimSpace(description) == "" || len(description) > MaxCandidateDescriptionSize {
			return fmt.Errorf("%w: criterion description is invalid", ErrInvalidInput)
		}
	}
	return nil
}

// Choose answers one choice question. The caller's criteria are the only
// permitted answers; an upstream answer outside them is an error.
func (c *Client) Choose(ctx context.Context, state, questionID, instructions string, criteria map[string]string) (Answer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateInput(state, questionID, instructions, criteria); err != nil {
		return Answer{}, err
	}
	payload, err := json.Marshal(request{
		Model: c.model,
		State: state,
		Questions: map[string]question{
			questionID: {Type: "choice", Instructions: instructions, Criteria: criteria},
		},
	})
	if err != nil {
		return Answer{}, fmt.Errorf("%w: request cannot be encoded", ErrInvalidInput)
	}
	body, err := c.post(ctx, payload)
	if err != nil {
		return Answer{}, err
	}
	return decodeAnswer(body, questionID, criteria)
}

// ValidateNoulInput applies the same bounds AskNouls enforces before a network
// call. It is exported so the advisory layer can reject or omit candidates
// without spending a provider request.
func ValidateNoulInput(state string, questions map[string]NoulQuestion) error {
	if !utf8.ValidString(state) || len(state) == 0 || len(state) > MaxStateBytes {
		return fmt.Errorf("%w: state must be valid utf-8 within the size limit", ErrInvalidInput)
	}
	if len(questions) == 0 || len(questions) > MaxQuestions {
		return fmt.Errorf("%w: question count is outside the supported range", ErrInvalidInput)
	}
	for id, question := range questions {
		if !questionPattern.MatchString(id) {
			return fmt.Errorf("%w: question id is invalid", ErrInvalidInput)
		}
		if !utf8.ValidString(question.Instructions) || len(question.Instructions) == 0 || len(question.Instructions) > MaxInstructionsBytes {
			return fmt.Errorf("%w: instructions must be valid utf-8 within the size limit", ErrInvalidInput)
		}
		for _, description := range []string{question.Criteria.True, question.Criteria.False} {
			if !utf8.ValidString(description) || strings.TrimSpace(description) == "" || len(description) > MaxCandidateDescriptionSize {
				return fmt.Errorf("%w: noul criterion description is invalid", ErrInvalidInput)
			}
		}
	}
	return nil
}

// AskNouls answers independent Noul questions over one shared state. Every
// answer is validated against the question id and the [0,1] range; a missing,
// duplicate, wrong-type, or out-of-range answer invalidates the whole response.
// Callers must treat the returned model as authoritative and compare it to
// their pinned configuration before using any answer.
func (c *Client) AskNouls(ctx context.Context, state string, questions map[string]NoulQuestion) (map[string]NoulAnswer, ResponseMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateNoulInput(state, questions); err != nil {
		return nil, ResponseMeta{}, err
	}
	wireQuestions := make(map[string]question, len(questions))
	for id, item := range questions {
		wireQuestions[id] = question{
			Type:         "noul",
			Instructions: item.Instructions,
			Criteria:     map[string]string{"true": item.Criteria.True, "false": item.Criteria.False},
		}
	}
	payload, err := json.Marshal(request{Model: c.model, State: state, Questions: wireQuestions})
	if err != nil {
		return nil, ResponseMeta{}, fmt.Errorf("%w: request cannot be encoded", ErrInvalidInput)
	}
	body, err := c.post(ctx, payload)
	if err != nil {
		return nil, ResponseMeta{}, err
	}
	return ParseSystemOne(body, questions)
}

func (c *Client) post(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: request cannot be built", ErrRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := c.http.Do(req)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrRequest, contextErr)
		}
		return nil, fmt.Errorf("%w: transport", ErrRequest)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrRequest, contextErr)
		}
		return nil, fmt.Errorf("%w: response cannot be read", ErrRequest)
	}
	if len(body) > MaxResponseBytes {
		return nil, ErrOversizeResponse
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return nil, ErrRedirect
	}
	if res.StatusCode != http.StatusOK {
		return nil, ErrHTTPStatus
	}
	return body, nil
}

type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type wireResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
	Usage   struct {
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
	} `json:"usage"`
}

type wireAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Noul          *float64           `json:"noul"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// ParseSystemOne validates one complete System One response against the exact
// questions that were sent. Any missing, extra, wrong-type, non-finite, or
// out-of-range answer invalidates the whole response; callers must not turn a
// decoder failure into a probability.
func ParseSystemOne(body []byte, questions map[string]NoulQuestion) (map[string]NoulAnswer, ResponseMeta, error) {
	if err := rejectDuplicateKeys(body); err != nil {
		return nil, ResponseMeta{}, ErrInvalidJSON
	}
	var response wireResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return nil, ResponseMeta{}, ErrInvalidJSON
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ResponseMeta{}, ErrInvalidJSON
	}
	if !modelPattern.MatchString(response.Model) {
		return nil, ResponseMeta{}, ErrInvalidAnswer
	}
	if len(response.Answers) != len(questions) {
		return nil, ResponseMeta{}, ErrInvalidAnswer
	}
	inputTokens, err := nonNegative(response.Usage.InputTokens)
	if err != nil {
		return nil, ResponseMeta{}, ErrInvalidAnswer
	}
	outputTokens, err := nonNegative(response.Usage.OutputTokens)
	if err != nil {
		return nil, ResponseMeta{}, ErrInvalidAnswer
	}
	usage := Usage{InputTokens: inputTokens, OutputTokens: outputTokens}
	answers := make(map[string]NoulAnswer, len(questions))
	for id := range questions {
		answer, ok := response.Answers[id]
		if !ok || answer.Type != "noul" || answer.Noul == nil {
			return nil, ResponseMeta{}, ErrInvalidAnswer
		}
		value := *answer.Noul
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return nil, ResponseMeta{}, ErrInvalidProbabilities
		}
		answers[id] = NoulAnswer{Noul: value, Model: response.Model, Usage: usage}
	}
	return answers, ResponseMeta{Model: response.Model, Usage: usage}, nil
}

func decodeAnswer(body []byte, questionID string, criteria map[string]string) (Answer, error) {
	if err := rejectDuplicateKeys(body); err != nil {
		return Answer{}, ErrInvalidJSON
	}
	var response wireResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return Answer{}, ErrInvalidJSON
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Answer{}, ErrInvalidJSON
	}
	if !modelPattern.MatchString(response.Model) {
		return Answer{}, ErrInvalidAnswer
	}
	if len(response.Answers) != 1 {
		return Answer{}, ErrInvalidAnswer
	}
	answer, ok := response.Answers[questionID]
	if !ok || answer.Type != "choice" || answer.Choice == "" {
		return Answer{}, ErrInvalidAnswer
	}
	if _, allowed := criteria[answer.Choice]; !allowed {
		return Answer{}, ErrInvalidAnswer
	}
	probabilities, err := validateProbabilities(answer.Probabilities, criteria, answer.Choice)
	if err != nil {
		return Answer{}, ErrInvalidProbabilities
	}
	confidence := probabilities[answer.Choice]
	source := "probability_max"
	if answer.Confidence != nil {
		if math.IsNaN(*answer.Confidence) || math.IsInf(*answer.Confidence, 0) ||
			*answer.Confidence < 0 || *answer.Confidence > 1 {
			return Answer{}, ErrInvalidAnswer
		}
		confidence = *answer.Confidence
		source = "answer_confidence"
	}
	inputTokens, err := nonNegative(response.Usage.InputTokens)
	if err != nil {
		return Answer{}, ErrInvalidAnswer
	}
	outputTokens, err := nonNegative(response.Usage.OutputTokens)
	if err != nil {
		return Answer{}, ErrInvalidAnswer
	}
	return Answer{
		Choice:           answer.Choice,
		Probabilities:    probabilities,
		Confidence:       confidence,
		ConfidenceSource: source,
		Model:            response.Model,
		Usage:            Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
	}, nil
}

func validateProbabilities(values map[string]float64, criteria map[string]string, choice string) (map[string]float64, error) {
	if len(values) != len(criteria) {
		return nil, errors.New("probability count does not match criteria")
	}
	probabilities := make(map[string]float64, len(values))
	var sum, maximum float64
	for id := range criteria {
		value, ok := values[id]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return nil, errors.New("probability value is invalid")
		}
		probabilities[id] = value
		sum += value
		if value > maximum {
			maximum = value
		}
	}
	tolerance := math.Max(1e-3, float64(len(criteria))*1e-4)
	if math.Abs(sum-1) > tolerance {
		return nil, errors.New("probabilities do not sum to one")
	}
	if probabilities[choice] < maximum-1e-9 {
		return nil, errors.New("selected choice is not the probability maximum")
	}
	return probabilities, nil
}

func nonNegative(value *int64) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 {
		return nil, errors.New("usage counter is negative")
	}
	copied := *value
	return &copied, nil
}

func validateEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Opaque != "" || u.RawPath != "" {
		return "", errors.New("jev endpoint must be an http(s) base URL without credentials, query or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", errors.New("jev endpoint must use https unless it is loopback")
		}
	default:
		return "", errors.New("jev endpoint must use http or https")
	}
	if u.Path != "" && u.Path != "/" && u.Path != "/v1" && u.Path != "/v1/" {
		return "", errors.New("jev endpoint path must be empty or /v1")
	}
	if port := u.Port(); port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 {
			return "", errors.New("jev endpoint has an invalid port")
		}
	}
	u.Path = "/v1/systemone"
	u.RawPath = ""
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAPIKey(key string) error {
	if key == "" {
		return errors.New("TYPESAFE_API_KEY is required")
	}
	if len(key) < 20 || len(key) > 4096 {
		return errors.New("TYPESAFE_API_KEY length is outside the supported range")
	}
	for _, character := range key {
		if character < 0x21 || character > 0x7e {
			return errors.New("TYPESAFE_API_KEY contains invalid characters")
		}
	}
	return nil
}

// rejectDuplicateKeys refuses ambiguous JSON before it reaches the typed
// decoder. The API is expected to emit unique keys; a duplicate is not an answer.
func rejectDuplicateKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid json object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate json object key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid json object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid json array")
		}
	default:
		return errors.New("invalid json delimiter")
	}
	return nil
}
