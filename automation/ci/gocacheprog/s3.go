// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// envelopeMagic prefixes every remote cache object so OutputID travels with the
	// body in a single GET without relying on proxy/CDN preservation of x-amz-meta headers.
	envelopeMagic    = "PGC1"
	outputIDLen      = 32
	envelopeHeaderSz = len(envelopeMagic) + outputIDLen
	emptySHA256Hex   = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// S3Config configures the zero-external-dependency S3/R2-compatible remote backend.
type S3Config struct {
	Bucket               string
	Endpoint             string // e.g. https://<account>.r2.cloudflarestorage.com or http://127.0.0.1:9000
	Region               string // e.g. auto (R2) or us-east-1 (AWS S3)
	Prefix               string // optional key namespace, e.g. v1/linux-amd64
	AccessKeyID          string
	SecretAccessKey      string
	SessionToken         string
	ReadOnly             bool
	Anonymous            bool // allow unsigned GET requests for public-read buckets (implies ReadOnly)
	PathStyle            bool // force path-style URLs even when Endpoint is empty
	Timeout              time.Duration
	MaxConsecutiveErrors int
	Client               *http.Client
	Now                  func() time.Time
}

// S3Backend stores Go cache entries in an S3/R2-compatible object store using
// AWS Signature Version 4. All errors are non-fatal to the caller and trip a
// fail-fast circuit breaker so an unreachable remote never stalls a build.
type S3Backend struct {
	cfg    S3Config
	client *http.Client
	now    func() time.Time

	mu           sync.Mutex
	consecErrors int
	circuitOpen  bool
}

// NewS3Backend validates cfg and constructs an S3Backend.
func NewS3Backend(cfg S3Config) (*S3Backend, error) {
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("s3: invalid endpoint %q", cfg.Endpoint)
		}
		// Custom endpoints (R2, MinIO, Tigris, test servers) use path-style addressing.
		cfg.PathStyle = true
	}
	if strings.TrimSpace(cfg.Region) == "" {
		if cfg.Endpoint != "" {
			cfg.Region = "auto"
		} else {
			cfg.Region = "us-east-1"
		}
	}
	cfg.Prefix = strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	hasCreds := cfg.AccessKeyID != "" && cfg.SecretAccessKey != ""
	if !hasCreds {
		if !cfg.Anonymous {
			return nil, errors.New("s3: credentials missing (set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or enable anonymous read-only mode)")
		}
		cfg.ReadOnly = true
	}
	if cfg.Anonymous {
		cfg.ReadOnly = true
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxConsecutiveErrors <= 0 {
		cfg.MaxConsecutiveErrors = 5
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 32,
				IdleConnTimeout:     60 * time.Second,
			},
		}
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &S3Backend{
		cfg:    cfg,
		client: client,
		now:    now,
	}, nil
}

// ReadOnly reports whether remote writes are disabled.
func (b *S3Backend) ReadOnly() bool {
	return b.cfg.ReadOnly
}

// CircuitOpen reports whether the fail-fast breaker has disabled remote I/O.
func (b *S3Backend) CircuitOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.circuitOpen
}

func (b *S3Backend) recordSuccess() {
	b.mu.Lock()
	b.consecErrors = 0
	b.mu.Unlock()
}

func (b *S3Backend) recordFailure() {
	b.mu.Lock()
	b.consecErrors++
	if b.consecErrors >= b.cfg.MaxConsecutiveErrors {
		b.circuitOpen = true
	}
	b.mu.Unlock()
}

func (b *S3Backend) objectKey(actionHex string) string {
	shard := actionHex
	if len(shard) >= 2 {
		shard = actionHex[:2]
	}
	parts := []string{}
	if b.cfg.Prefix != "" {
		parts = append(parts, b.cfg.Prefix)
	}
	parts = append(parts, shard, actionHex)
	return path.Join(parts...)
}

func (b *S3Backend) objectURL(key string) string {
	escapedKey := encodeS3Path(key)
	if b.cfg.Endpoint != "" {
		return b.cfg.Endpoint + "/" + encodeS3Segment(b.cfg.Bucket) + "/" + escapedKey
	}
	if b.cfg.PathStyle {
		return fmt.Sprintf("https://s3.%s.amazonaws.com/%s/%s", b.cfg.Region, encodeS3Segment(b.cfg.Bucket), escapedKey)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", b.cfg.Bucket, b.cfg.Region, escapedKey)
}

// Get retrieves an artifact from the remote bucket and validates its envelope.
func (b *S3Backend) Get(ctx context.Context, actionHex string) ([]byte, []byte, time.Time, bool, error) {
	if b.CircuitOpen() {
		return nil, nil, time.Time{}, false, nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()

	targetURL := b.objectURL(b.objectKey(actionHex))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, nil, time.Time{}, false, err
	}
	if !b.cfg.Anonymous {
		signS3Request(req, emptySHA256Hex, b.cfg.AccessKeyID, b.cfg.SecretAccessKey, b.cfg.SessionToken, b.cfg.Region, b.now())
	}

	resp, err := b.client.Do(req)
	if err != nil {
		b.recordFailure()
		return nil, nil, time.Time{}, false, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		b.recordSuccess()
		return nil, nil, time.Time{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		b.recordFailure()
		return nil, nil, time.Time{}, false, fmt.Errorf("s3 get %s: HTTP %d", actionHex[:min(len(actionHex), 12)], resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		b.recordFailure()
		return nil, nil, time.Time{}, false, err
	}
	outputID, payload, err := decodeEnvelope(raw)
	if err != nil {
		return nil, nil, time.Time{}, false, err
	}
	b.recordSuccess()

	var modTime time.Time
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if parsed, err := http.ParseTime(lm); err == nil {
			modTime = parsed.UTC()
		}
	}
	return outputID, payload, modTime, true, nil
}

// Put uploads a framed (OutputID + body) artifact to the remote bucket.
// Refuses all writes when ReadOnly is true.
func (b *S3Backend) Put(ctx context.Context, actionHex string, outputID []byte, bodyPath string, _ int64) error {
	if b.cfg.ReadOnly {
		return errors.New("s3: remote backend is read-only")
	}
	if b.CircuitOpen() {
		return nil
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return err
	}
	framed, err := encodeEnvelope(outputID, body)
	if err != nil {
		return err
	}
	payloadSum := sha256.Sum256(framed)
	payloadHash := hex.EncodeToString(payloadSum[:])

	putTimeout := max(b.cfg.Timeout, 15*time.Second)
	reqCtx, cancel := context.WithTimeout(ctx, putTimeout)
	defer cancel()

	targetURL := b.objectURL(b.objectKey(actionHex))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, targetURL, bytes.NewReader(framed))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(framed))
	req.Header.Set("Content-Type", "application/octet-stream")
	signS3Request(req, payloadHash, b.cfg.AccessKeyID, b.cfg.SecretAccessKey, b.cfg.SessionToken, b.cfg.Region, b.now())

	resp, err := b.client.Do(req)
	if err != nil {
		b.recordFailure()
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b.recordFailure()
		return fmt.Errorf("s3 put %s: HTTP %d", actionHex[:min(len(actionHex), 12)], resp.StatusCode)
	}
	b.recordSuccess()
	return nil
}

func encodeEnvelope(outputID, body []byte) ([]byte, error) {
	if len(outputID) != outputIDLen {
		return nil, fmt.Errorf("s3: unexpected OutputID length %d (want %d)", len(outputID), outputIDLen)
	}
	buf := make([]byte, envelopeHeaderSz+len(body))
	copy(buf[:len(envelopeMagic)], envelopeMagic)
	copy(buf[len(envelopeMagic):envelopeHeaderSz], outputID)
	copy(buf[envelopeHeaderSz:], body)
	return buf, nil
}

func decodeEnvelope(raw []byte) (outputID, body []byte, err error) {
	if len(raw) < envelopeHeaderSz {
		return nil, nil, fmt.Errorf("s3: truncated cache envelope (%d bytes)", len(raw))
	}
	if string(raw[:len(envelopeMagic)]) != envelopeMagic {
		return nil, nil, errors.New("s3: unrecognized cache envelope magic")
	}
	outputID = make([]byte, outputIDLen)
	copy(outputID, raw[len(envelopeMagic):envelopeHeaderSz])
	body = raw[envelopeHeaderSz:]
	return outputID, body, nil
}

// signS3Request signs req in-place using AWS Signature Version 4 (service "s3").
func signS3Request(req *http.Request, payloadHash, accessKey, secretKey, sessionToken, region string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	// Build canonical headers (sorted lowercase keys).
	hdrMap := map[string]string{
		"host":                 strings.TrimSpace(host),
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if sessionToken != "" {
		hdrMap["x-amz-security-token"] = strings.TrimSpace(sessionToken)
	}
	keys := make([]string, 0, len(hdrMap))
	for k := range hdrMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(hdrMap[k])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(keys, ";")

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalizeQuery(req.URL.Query())

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	crSum := sha256.Sum256([]byte(canonicalRequest))
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(crSum[:]),
	}, "\n")

	signingKey := deriveSigV4Key(secretKey, dateStamp, region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func deriveSigV4Key(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func canonicalizeQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		ek := encodeS3Segment(k)
		for _, v := range vals {
			parts = append(parts, ek+"="+encodeS3Segment(v))
		}
	}
	return strings.Join(parts, "&")
}

func encodeS3Path(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = encodeS3Segment(seg)
	}
	return strings.Join(segs, "/")
}

// encodeS3Segment percent-encodes per RFC 3986 unreserved characters (required by AWS SigV4).
func encodeS3Segment(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			_, _ = fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
