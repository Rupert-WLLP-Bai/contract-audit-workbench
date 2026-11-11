// File metadata (manifest mirror):
// Purpose: provides private, path-style S3-compatible object storage. Related `objectstore` operations include New, Put, Get, validateOperation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package objectstore provides private, path-style S3-compatible object storage.
package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
)

// MaxObjectBytes is the largest original object or downloaded object accepted.
const MaxObjectBytes = 32 << 20

const maxServiceErrorBodyBytes = 64 << 10

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
	bucket string
	client *awss3.Client
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
		bucket: config.Bucket,
		client: newSDKClient(config, endpoint, responseMetadataHTTPClient{next: client}),
	}, nil
}

func newSDKClient(config Config, endpoint url.URL, httpClient awss3.HTTPClient) *awss3.Client {
	credentials := aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{
			AccessKeyID:     config.AccessKey,
			SecretAccessKey: config.SecretKey,
			Source:          "objectstore configuration",
		}, nil
	}))
	return awss3.New(awss3.Options{
		Region:                       config.Region,
		Credentials:                  credentials,
		BaseEndpoint:                 aws.String(endpoint.String()),
		UsePathStyle:                 true,
		HTTPClient:                   httpClient,
		RetryMaxAttempts:             1,
		RequestChecksumCalculation:   aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation:   aws.ResponseChecksumValidationWhenRequired,
		ContinueHeaderThresholdBytes: -1,
		APIOptions:                   []func(*middleware.Stack) error{forceSignedPayloadHash},
	})
}

func forceSignedPayloadHash(stack *middleware.Stack) error {
	computedHash := &awsv4.ComputePayloadSHA256{}
	_, err := stack.Finalize.Swap(computedHash.ID(), computedHash)
	return err
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
	requestCtx, responseState := withResponseState(ctx)
	_, err := s.client.PutObject(requestCtx, &awss3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(content),
		ContentLength: aws.Int64(int64(len(content))),
		Metadata:      map[string]string{"sha256": hex.EncodeToString(digest[:])},
	})
	if err != nil {
		return requestError(ctx)
	}
	if !validPutStatus(responseState.statusCode) {
		return ErrRequestFailed
	}
	return nil
}

// Get downloads one object and verifies its bytes against the stored SHA-256 metadata.
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	if err := validateOperation(s, ctx, key); err != nil {
		return nil, err
	}
	requestCtx, responseState := withResponseState(ctx)
	response, err := s.client.GetObject(requestCtx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if responseState.statusCode == http.StatusNotFound {
			return nil, ErrObjectNotFound
		}
		return nil, requestError(ctx)
	}
	if response == nil || response.Body == nil {
		return nil, ErrRequestFailed
	}
	defer response.Body.Close()
	if responseState.statusCode != http.StatusOK {
		return nil, ErrRequestFailed
	}
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

func validPutStatus(statusCode int) bool {
	return statusCode == http.StatusOK || statusCode == http.StatusCreated || statusCode == http.StatusNoContent
}

func readGetResponse(ctx context.Context, response *awss3.GetObjectOutput) ([]byte, error) {
	if response.ContentLength != nil && *response.ContentLength > MaxObjectBytes {
		return nil, ErrObjectTooLarge
	}
	content, err := readLimitedObject(ctx, response)
	if err != nil {
		return nil, err
	}
	digest, ok := response.Metadata["sha256"]
	if !ok {
		return nil, ErrIntegrity
	}
	if err := verifyObjectDigest(content, digest); err != nil {
		return nil, err
	}
	return content, nil
}

func readLimitedObject(ctx context.Context, response *awss3.GetObjectOutput) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(response.Body, MaxObjectBytes+1))
	if err != nil {
		return nil, requestError(ctx)
	}
	if len(content) > MaxObjectBytes {
		return nil, ErrObjectTooLarge
	}
	return content, nil
}

func verifyObjectDigest(content []byte, metadataValue string) error {
	storedDigest, err := hex.DecodeString(metadataValue)
	if err != nil || len(storedDigest) != sha256.Size {
		return ErrIntegrity
	}
	actualDigest := sha256.Sum256(content)
	if subtle.ConstantTimeCompare(storedDigest, actualDigest[:]) != 1 {
		return ErrIntegrity
	}
	return nil
}

type responseMetadataHTTPClient struct {
	next awss3.HTTPClient
}

type responseStateContextKey struct{}

type responseState struct {
	statusCode int
}

func withResponseState(ctx context.Context) (context.Context, *responseState) {
	state := &responseState{}
	return context.WithValue(ctx, responseStateContextKey{}, state), state
}

func (client responseMetadataHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := client.next.Do(request)
	if response == nil {
		return response, err
	}
	if state, ok := request.Context().Value(responseStateContextKey{}).(*responseState); ok {
		state.statusCode = response.StatusCode
	}
	const digestHeader = "X-Amz-Meta-Sha256"
	values := response.Header.Values(digestHeader)
	if len(values) > 1 {
		// The pinned SDK maps repeated metadata headers to their first value. Keep
		// duplicates invalid so the package does not accept ambiguous evidence.
		response.Header.Set(digestHeader, strings.Join(values, ","))
	}
	if response.Body != nil && (response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices) {
		response.Body = &limitedErrorResponseBody{
			ReadCloser: response.Body,
			reader:     io.LimitReader(response.Body, maxServiceErrorBodyBytes),
		}
	}
	return response, err
}

type limitedErrorResponseBody struct {
	io.ReadCloser
	reader io.Reader
}

func (body *limitedErrorResponseBody) Read(p []byte) (int, error) {
	return body.reader.Read(p)
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
