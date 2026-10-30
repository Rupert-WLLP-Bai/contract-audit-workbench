// Package objectstore provides private, path-style S3-compatible object storage.
package objectstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxObjectBytes is the largest original object or downloaded object accepted.
const MaxObjectBytes = 32 << 20

var (
	// ErrInvalidConfig indicates that the S3 endpoint or credentials are invalid.
	ErrInvalidConfig = errors.New("object store configuration is invalid")
	// ErrInvalidKey indicates that an object key is unsafe or malformed.
	ErrInvalidKey = errors.New("object key is invalid")
	// ErrObjectTooLarge indicates that an object exceeds MaxObjectBytes.
	ErrObjectTooLarge = errors.New("object exceeds size limit")
	// ErrObjectNotFound indicates that the requested object does not exist.
	ErrObjectNotFound = errors.New("object not found")
	// ErrIntegrity indicates that downloaded bytes do not match their S3 metadata.
	ErrIntegrity = errors.New("object integrity check failed")
	// ErrRequestFailed is a redacted S3 transport or service failure.
	ErrRequestFailed = errors.New("object store request failed")
)

// Config contains the endpoint and credentials for one private S3 bucket.
type Config struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
}

// S3 stores objects in a private S3-compatible bucket using path-style requests.
type S3 struct {
	endpoint  url.URL
	bucket    string
	region    string
	accessKey string
	secretKey string
	client    *http.Client
}

// New validates configuration and creates an S3-compatible object store.
func New(config Config) (*S3, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil || !validBucket(config.Bucket) || !validRegion(config.Region) ||
		!validAccessKey(config.AccessKey) || !validSecretKey(config.SecretKey) {
		return nil, ErrInvalidConfig
	}

	var transport *http.Transport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &S3{
		endpoint:  endpoint,
		bucket:    config.Bucket,
		region:    config.Region,
		accessKey: config.AccessKey,
		secretKey: config.SecretKey,
		client:    client,
	}, nil
}

// Put writes one object and stores its SHA-256 digest as signed S3 user metadata.
func (s *S3) Put(ctx context.Context, key string, content []byte) error {
	if err := validateOperation(s, ctx, key); err != nil {
		return err
	}
	if len(content) > MaxObjectBytes {
		return ErrObjectTooLarge
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	digest := sha256.Sum256(content)
	digestHex := hex.EncodeToString(digest[:])
	request, err := s.newRequest(ctx, http.MethodPut, key, content, digestHex)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return requestError(ctx)
	}
	defer response.Body.Close()
	return validatePutResponse(response)
}

// Get downloads one object and verifies its bytes against the stored SHA-256 metadata.
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	if err := validateOperation(s, ctx, key); err != nil {
		return nil, err
	}
	emptyDigest := sha256.Sum256(nil)
	emptyDigestHex := hex.EncodeToString(emptyDigest[:])
	request, err := s.newRequest(ctx, http.MethodGet, key, nil, emptyDigestHex)
	if err != nil {
		return nil, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, requestError(ctx)
	}
	defer response.Body.Close()
	return readGetResponse(ctx, response)
}

func validateOperation(s *S3, ctx context.Context, key string) error {
	if s == nil || s.client == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if err := validateKey(key); err != nil {
		return err
	}
	return ctx.Err()
}

func requestError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrRequestFailed
}

func validatePutResponse(response *http.Response) error {
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusNoContent {
		return nil
	}
	return ErrRequestFailed
}

func readGetResponse(ctx context.Context, response *http.Response) ([]byte, error) {
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrObjectNotFound
	}
	if response.StatusCode != http.StatusOK {
		return nil, ErrRequestFailed
	}
	content, err := readLimitedObject(ctx, response)
	if err != nil {
		return nil, err
	}
	if err := verifyObjectDigest(content, response.Header.Values("X-Amz-Meta-Sha256")); err != nil {
		return nil, err
	}
	return content, nil
}

func readLimitedObject(ctx context.Context, response *http.Response) ([]byte, error) {
	if response.ContentLength > MaxObjectBytes {
		return nil, ErrObjectTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, MaxObjectBytes+1))
	if err != nil {
		return nil, requestError(ctx)
	}
	if len(content) > MaxObjectBytes {
		return nil, ErrObjectTooLarge
	}
	return content, nil
}

func verifyObjectDigest(content []byte, metadataValues []string) error {
	if len(metadataValues) != 1 {
		return ErrIntegrity
	}
	storedDigest, err := hex.DecodeString(metadataValues[0])
	if err != nil || len(storedDigest) != sha256.Size {
		return ErrIntegrity
	}
	actualDigest := sha256.Sum256(content)
	if subtle.ConstantTimeCompare(storedDigest, actualDigest[:]) != 1 {
		return ErrIntegrity
	}
	return nil
}

func (s *S3) newRequest(ctx context.Context, method, key string, body []byte, digest string) (*http.Request, error) {
	objectURL := s.endpoint
	basePath := strings.TrimRight(objectURL.Path, "/")
	objectPath := basePath + "/" + s.bucket + "/" + key
	objectURL.Path = objectPath
	objectURL.RawPath = encodePath(objectPath)
	objectURL.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, method, objectURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, ErrRequestFailed
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("X-Amz-Content-Sha256", digest)
	request.Header.Set("X-Amz-Date", time.Now().UTC().Format("20060102T150405Z"))
	if method == http.MethodPut {
		request.Header.Set("X-Amz-Meta-Sha256", digest)
	}
	if err := s.sign(request, digest); err != nil {
		return nil, ErrRequestFailed
	}
	return request, nil
}

func (s *S3) sign(request *http.Request, payloadDigest string) error {
	canonicalQuery, err := canonicalQuery(request.URL.RawQuery)
	if err != nil {
		return err
	}

	signedHeaders := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if request.Method == http.MethodPut {
		signedHeaders = append(signedHeaders, "x-amz-meta-sha256")
	}
	canonicalHeaders := strings.Builder{}
	for _, name := range signedHeaders {
		value := request.URL.Host
		if name != "host" {
			value = request.Header.Get(name)
		}
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.Join(strings.Fields(value), " "))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaderNames := strings.Join(signedHeaders, ";")
	canonicalRequest := request.Method + "\n" + request.URL.EscapedPath() + "\n" + canonicalQuery + "\n" +
		canonicalHeaders.String() + "\n" + signedHeaderNames + "\n" + payloadDigest
	canonicalRequestDigest := sha256.Sum256([]byte(canonicalRequest))

	date := request.Header.Get("X-Amz-Date")
	shortDate := date[:8]
	scope := shortDate + "/" + s.region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + date + "\n" + scope + "\n" + hex.EncodeToString(canonicalRequestDigest[:])

	dateKey := hmacSHA256([]byte("AWS4"+s.secretKey), shortDate)
	regionKey := hmacSHA256(dateKey, s.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.accessKey+"/"+scope+
		", SignedHeaders="+signedHeaderNames+", Signature="+signature)
	return nil
}

func parseEndpoint(value string) (url.URL, error) {
	if !validEndpointInput(value) {
		return url.URL{}, ErrInvalidConfig
	}
	endpoint, err := url.Parse(value)
	if err != nil || !validEndpointURL(endpoint) || !validEndpointPort(endpoint) {
		return url.URL{}, ErrInvalidConfig
	}
	basePath, ok := endpointBasePath(endpoint)
	if !ok {
		return url.URL{}, ErrInvalidConfig
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Path = basePath
	endpoint.RawPath = ""
	return *endpoint, nil
}

func validEndpointInput(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "?#")
}

func validEndpointURL(endpoint *url.URL) bool {
	return validEndpointAddress(endpoint) && validEndpointPathOptions(endpoint)
}

func validEndpointAddress(endpoint *url.URL) bool {
	return endpoint.IsAbs() && endpoint.Opaque == "" && endpoint.User == nil &&
		(endpoint.Scheme == "http" || endpoint.Scheme == "https") && endpoint.Hostname() != ""
}

func validEndpointPathOptions(endpoint *url.URL) bool {
	return endpoint.RawQuery == "" && !endpoint.ForceQuery && endpoint.Fragment == "" && endpoint.RawFragment == ""
}

func validEndpointPort(endpoint *url.URL) bool {
	port := endpoint.Port()
	if port == "" {
		return true
	}
	portNumber, err := strconv.Atoi(port)
	return err == nil && portNumber > 0 && portNumber <= 65535
}

func endpointBasePath(endpoint *url.URL) (string, bool) {
	escapedPath := strings.ToLower(endpoint.EscapedPath())
	if strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") || strings.Contains(endpoint.Path, `\`) {
		return "", false
	}
	basePath := strings.TrimRight(endpoint.Path, "/")
	for _, segment := range endpointSegments(basePath) {
		if !validEndpointSegment(segment) {
			return "", false
		}
	}
	return basePath, true
}

func endpointSegments(basePath string) []string {
	if basePath == "" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(basePath, "/"), "/")
}

func validEndpointSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for _, r := range segment {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validBucket(bucket string) bool {
	if !validBucketLength(bucket) || !validBucketName(bucket) {
		return false
	}
	if !isBucketAlphaNumeric(bucket[0]) || !isBucketAlphaNumeric(bucket[len(bucket)-1]) {
		return false
	}
	for i := range bucket {
		if !validBucketCharacter(bucket[i]) {
			return false
		}
	}
	return true
}

func validBucketLength(bucket string) bool { return len(bucket) >= 3 && len(bucket) <= 63 }

func validBucketName(bucket string) bool {
	return !strings.Contains(bucket, "..") && !strings.HasPrefix(bucket, "xn--") &&
		!strings.HasPrefix(bucket, "sthree-") && !strings.HasPrefix(bucket, "amzn-s3-demo-") && net.ParseIP(bucket) == nil
}

func isBucketAlphaNumeric(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }

func validBucketCharacter(b byte) bool { return isBucketAlphaNumeric(b) || b == '.' || b == '-' }

func validRegion(region string) bool {
	if region == "" || len(region) > 64 {
		return false
	}
	for i := range region {
		if !validRegionCharacter(region[i]) {
			return false
		}
	}
	return true
}

func validRegionCharacter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-'
}

func validAccessKey(accessKey string) bool {
	if accessKey == "" || len(accessKey) > 128 {
		return false
	}
	for i := range accessKey {
		if !validAccessKeyCharacter(accessKey[i]) {
			return false
		}
	}
	return true
}

func validAccessKeyCharacter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("+=._-", rune(b))
}

func validSecretKey(secretKey string) bool {
	if secretKey == "" || len(secretKey) > 1024 || strings.TrimSpace(secretKey) != secretKey || !utf8.ValidString(secretKey) {
		return false
	}
	for _, r := range secretKey {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateKey(key string) error {
	if !validKeyText(key) || !validKeyPrefix(key) || !validKeySegments(key) {
		return ErrInvalidKey
	}
	return nil
}

func validKeyText(key string) bool {
	return key != "" && len(key) <= 1024 && utf8.ValidString(key)
}

func validKeyPrefix(key string) bool {
	return key[0] != '/' && key[0] != '\\' && !strings.ContainsAny(key, `\?#`) && !hasSchemePrefix(key)
}

func validKeySegments(key string) bool {
	for _, r := range key {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func isASCIIAlpha(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

func hasSchemePrefix(key string) bool {
	colon := strings.IndexByte(key, ':')
	slash := strings.IndexByte(key, '/')
	return validSchemePosition(key, colon, slash) && validSchemeName(key[:colon])
}

func validSchemePosition(key string, colon, slash int) bool {
	return colon > 0 && (slash < 0 || slash >= colon) && isASCIIAlpha(key[0])
}

func validSchemeName(name string) bool {
	for i := 1; i < len(name); i++ {
		if !validSchemeCharacter(name[i]) {
			return false
		}
	}
	return true
}

func validSchemeCharacter(b byte) bool {
	return isASCIIAlpha(b) || b >= '0' && b <= '9' || b == '+' || b == '.' || b == '-'
}

func canonicalQuery(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	pairs, err := parseCanonicalQuery(raw)
	if err != nil {
		return "", err
	}
	slices.SortFunc(pairs, func(a, b canonicalQueryPair) int {
		if order := strings.Compare(a.key, b.key); order != 0 {
			return order
		}
		return strings.Compare(a.value, b.value)
	})
	return formatCanonicalQuery(pairs), nil
}

type canonicalQueryPair struct{ key, value string }

func parseCanonicalQuery(raw string) ([]canonicalQueryPair, error) {
	pairs := make([]canonicalQueryPair, 0, strings.Count(raw, "&")+1)
	for _, item := range strings.Split(raw, "&") {
		if item == "" {
			continue
		}
		name, value, hasValue := strings.Cut(item, "=")
		decodedName, err := url.PathUnescape(name)
		if err != nil {
			return nil, err
		}
		if !hasValue {
			value = ""
		}
		decodedValue, err := url.PathUnescape(value)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, canonicalQueryPair{key: awsEncode(decodedName), value: awsEncode(decodedValue)})
	}
	return pairs, nil
}

func formatCanonicalQuery(pairs []canonicalQueryPair) string {
	var query strings.Builder
	for i, item := range pairs {
		if i > 0 {
			query.WriteByte('&')
		}
		query.WriteString(item.key)
		query.WriteByte('=')
		query.WriteString(item.value)
	}
	return query.String()
}

func encodePath(path string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(path), "+", "%20"), "%2F", "/")
}

func awsEncode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, value)
	return mac.Sum(nil)
}
