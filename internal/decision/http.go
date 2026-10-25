package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/access"
)

const (
	defaultHTTPTimeout    = 5 * time.Second
	maxHTTPTimeout        = 30 * time.Second
	probabilityQuantum    = 1e-4
	probabilityFloatSlack = 1e-12
)

type AdapterResult struct {
	Suggestion    *Suggestion
	Latency       time.Duration
	FailureReason string
}

type systemOneRequest struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type wireResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
	Routing *wireRouting          `json:"routing"`
}

type wireRouting struct {
	Model string `json:"model"`
}

type wireAnswer struct {
	Type             string          `json:"type"`
	Choice           string          `json:"choice"`
	Probabilities    json.RawMessage `json:"probabilities"`
	AnswerConfidence *float64        `json:"answer_confidence"`
	LowConfidence    *bool           `json:"low_confidence"`
	Abstention       string          `json:"abstention"`
}

// Query performs one authorized suggestion request. It returns only fixed failure
// reasons; upstream response bodies, transport errors and bearer tokens are never
// returned to the caller.
func (a *SystemOneAdapter) Query(ctx context.Context, input Input) AdapterResult {
	if ctx == nil {
		ctx = context.Background()
	}
	decision := access.Check(access.Review, input.Principal, input.Resource, time.Now())
	if !decision.Allowed {
		return AdapterResult{FailureReason: FailureAuthorization}
	}
	if !validInput(input) {
		return AdapterResult{FailureReason: FailureInvalidInput}
	}
	suggestion, latency, failure := a.query(ctx, input)
	return AdapterResult{Suggestion: suggestion, Latency: latency, FailureReason: failure}
}

func newSystemOneAdapter(endpoint string, timeout time.Duration, bearerToken string) (*SystemOneAdapter, error) {
	validatedEndpoint, err := validateEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if timeout == 0 {
		timeout = defaultHTTPTimeout
	}
	if timeout < 0 || timeout > maxHTTPTimeout {
		return nil, errors.New("invalid Laya HTTP timeout")
	}
	if len(bearerToken) > 4096 {
		return nil, errors.New("invalid Laya bearer token")
	}
	for _, character := range bearerToken {
		if character < 0x21 || character > 0x7e {
			return nil, errors.New("invalid Laya bearer token")
		}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           loopbackDialer(),
		MaxConnsPerHost:       4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &SystemOneAdapter{endpoint: validatedEndpoint, timeout: timeout,
		bearerToken: bearerToken, client: client}, nil
}

func validateEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return "", errors.New("Laya endpoint must be a loopback HTTP base URL")
	}
	host := strings.ToLower(u.Hostname())
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("Laya endpoint must use a loopback host")
		}
	}
	if port := u.Port(); port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 {
			return "", errors.New("Laya endpoint has an invalid port")
		}
	}
	if u.Path != "" && u.Path != "/" && u.Path != "/v1" && u.Path != "/v1/" {
		return "", errors.New("Laya endpoint path must be /v1")
	}
	u.Path = "/v1/systemone"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func loopbackDialer() func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid Laya dial target")
		}
		if ip := net.ParseIP(host); ip != nil {
			if !ip.IsLoopback() {
				return nil, errors.New("non-loopback Laya dial target")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		if !strings.EqualFold(host, "localhost") {
			return nil, errors.New("non-loopback Laya dial target")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("Laya loopback name could not be resolved")
		}
		for _, candidate := range addresses {
			if !candidate.IP.IsLoopback() {
				return nil, errors.New("Laya name resolved outside loopback")
			}
		}
		var lastErr error
		for _, candidate := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network,
				net.JoinHostPort(candidate.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr != nil {
			return nil, errors.New("Laya loopback connection failed")
		}
		return nil, errors.New("Laya loopback connection failed")
	}
}

func (a *SystemOneAdapter) query(ctx context.Context, input Input) (*Suggestion, time.Duration, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestBody, err := json.Marshal(buildRequest(input))
	if err != nil {
		return nil, 0, FailureInvalidInput
	}
	requestCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.endpoint,
		bytes.NewReader(requestBody))
	if err != nil {
		return nil, 0, FailureInvalidInput
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if a.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}

	started := time.Now()
	response, err := a.client.Do(request)
	if err != nil {
		latency := time.Since(started)
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, latency, FailureTimeout
		}
		if errors.Is(requestCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return nil, latency, FailureCanceled
		}
		return nil, latency, FailureTransport
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	latency := time.Since(started)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, latency, FailureTimeout
		}
		if errors.Is(requestCtx.Err(), context.Canceled) {
			return nil, latency, FailureCanceled
		}
		return nil, latency, FailureTransport
	}
	if len(body) > MaxResponseBytes {
		return nil, latency, FailureOversizeResponse
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, latency, FailureRedirect
	}
	if response.StatusCode != http.StatusOK {
		return nil, latency, FailureHTTPStatus
	}
	suggestion, failure := decodeResponse(body, input)
	return suggestion, latency, failure
}

func buildRequest(input Input) systemOneRequest {
	questionID, instruction := questionFor(input.Capability)
	criteria := make(map[string]string, len(input.AllowedCandidates))
	for _, candidate := range input.AllowedCandidates {
		criteria[candidate.ID] = candidate.Description
	}
	return systemOneRequest{
		Model: ModelAlias,
		State: input.State,
		Questions: map[string]question{
			questionID: {Type: "choice", Instructions: instruction, Criteria: criteria},
		},
	}
}

func questionFor(capability Capability) (string, string) {
	switch capability {
	case CapabilityChoice:
		return "review_focus", ChoiceInstructions
	case CapabilityRuleRecommendation:
		return "rule_recommendation", "选择最适合优先执行的一个非强制审核规则。只建议规则执行优先级，不判断合同结论；不得跳过其他规则。"
	default:
		return "", ""
	}
}

func decodeResponse(body []byte, input Input) (*Suggestion, string) {
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return nil, FailureInvalidJSON
	}
	var response wireResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return nil, FailureInvalidJSON
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, FailureInvalidJSON
	}
	questionID, _ := questionFor(input.Capability)
	if response.Model != "laya-rl-agent" || len(response.Answers) != 1 {
		return nil, FailureInvalidAnswer
	}
	answer, ok := response.Answers[questionID]
	if !ok || answer.Type != "choice" || answer.Choice == "" {
		return nil, FailureInvalidAnswer
	}
	allowed := make(map[string]struct{}, len(input.AllowedCandidates))
	for _, candidate := range input.AllowedCandidates {
		allowed[candidate.ID] = struct{}{}
	}
	if _, ok := allowed[answer.Choice]; !ok {
		return nil, FailureInvalidAnswer
	}
	probabilities, confidence, source, err := decodeProbabilities(answer.Probabilities,
		input.AllowedCandidates, answer.Choice, answer.AnswerConfidence)
	if err != nil {
		return nil, FailureInvalidProbabilities
	}
	if answer.Abstention != "" && answer.Abstention != "passed" &&
		answer.Abstention != "abstained" && answer.Abstention != "unevaluated" {
		return nil, FailureInvalidAnswer
	}
	upstreamAbstained := answer.Abstention == "abstained" ||
		(answer.LowConfidence != nil && *answer.LowConfidence)
	if response.Routing != nil && response.Routing.Model != "" && response.Routing.Model != ModelAlias {
		return nil, FailureInvalidAnswer
	}
	routedCheckpoint := ""
	if response.Routing != nil {
		routedCheckpoint = safeResponseLabel(response.Routing.Model)
	}
	return &Suggestion{
		CandidateID: answer.Choice, Probabilities: probabilities, Confidence: &confidence,
		ConfidenceSource: source, ResponseModel: safeResponseLabel(response.Model),
		RoutedCheckpoint: routedCheckpoint, UpstreamAbstained: upstreamAbstained,
	}, ""
}

func decodeProbabilities(raw json.RawMessage, candidates []Candidate, choice string,
	answerConfidence *float64) (map[string]float64, float64, string, error) {
	if len(raw) == 0 {
		return nil, 0, "", errors.New("missing probabilities")
	}
	probabilities, err := parseProbabilityObject(raw)
	if err != nil || len(probabilities) != len(candidates) {
		return nil, 0, "", errors.New("invalid probability map")
	}
	var sum, maxProbability float64
	for _, candidate := range candidates {
		probability, ok := probabilities[candidate.ID]
		if !ok || math.IsNaN(probability) || math.IsInf(probability, 0) || probability < 0 || probability > 1 {
			return nil, 0, "", errors.New("invalid probability value")
		}
		sum += probability
		if probability > maxProbability {
			maxProbability = probability
		}
	}
	sumTolerance := float64(len(candidates))*probabilityQuantum/2 + probabilityFloatSlack
	if math.Abs(sum-1) > sumTolerance {
		return nil, 0, "", errors.New("probabilities do not sum to one")
	}
	if probabilities[choice] != maxProbability {
		return nil, 0, "", errors.New("choice is not the probability maximum")
	}
	confidence, source := maxProbability, "probability_max"
	if answerConfidence != nil {
		if math.IsNaN(*answerConfidence) || math.IsInf(*answerConfidence, 0) ||
			*answerConfidence < 0 || *answerConfidence > 1 ||
			math.Abs(*answerConfidence-maxProbability) > probabilityQuantum+probabilityFloatSlack {
			return nil, 0, "", errors.New("answer confidence does not match probabilities")
		}
		confidence, source = *answerConfidence, "answer_confidence"
	}
	return probabilities, confidence, source, nil
}

func parseProbabilityObject(raw []byte) (map[string]float64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("probabilities must be an object")
	}
	probabilities := make(map[string]float64)
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		key, ok := keyToken.(string)
		if tokenErr != nil || !ok || key == "" {
			return nil, errors.New("invalid probability key")
		}
		if _, duplicate := probabilities[key]; duplicate {
			return nil, errors.New("duplicate probability key")
		}
		valueToken, tokenErr := decoder.Token()
		number, numeric := valueToken.(json.Number)
		if tokenErr != nil || !numeric {
			return nil, errors.New("invalid probability value")
		}
		value, err := number.Float64()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, errors.New("invalid probability value")
		}
		probabilities[key] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, errors.New("invalid probability object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("trailing probability data")
	}
	return probabilities, nil
}

func rejectDuplicateJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON")
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
				return errors.New("invalid JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func safeResponseLabel(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == ':' || r == '/' || r == '@') {
			return ""
		}
	}
	return value
}
