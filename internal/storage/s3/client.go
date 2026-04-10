package s3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("s3 object not found")

type Client struct {
	endpoint     *url.URL
	region       string
	accessKey    string
	secretKey    string
	usePathStyle bool
	httpClient   *http.Client
}

type ClientConfig struct {
	Endpoint     string
	Region       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
	DisableSSL   bool
	MaxRetries   int
}

type GetObjectResponse struct {
	Body          io.ReadCloser
	ContentLength int64
	LastModified  time.Time
}

type HeadObjectResponse struct {
	ContentLength int64
	LastModified  time.Time
}

type ListedObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type ListObjectsResponse struct {
	CommonPrefixes        []string
	Contents              []ListedObject
	IsTruncated           bool
	NextContinuationToken string
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("s3 endpoint is required")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if !strings.Contains(endpoint, "://") {
		scheme := "https"
		if cfg.DisableSSL {
			scheme = "http"
		}
		endpoint = scheme + "://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if parsed.Host == "" {
		return nil, errors.New("s3 endpoint must include a host")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-1"
	}
	return &Client{
		endpoint:     parsed,
		region:       region,
		accessKey:    strings.TrimSpace(cfg.AccessKey),
		secretKey:    strings.TrimSpace(cfg.SecretKey),
		usePathStyle: cfg.UsePathStyle,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

func (c *Client) GetObject(ctx context.Context, bucket, key string) (*GetObjectResponse, error) {
	req, err := c.newObjectRequest(ctx, http.MethodGet, bucket, key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req, nil)
	if err != nil {
		return nil, err
	}
	return &GetObjectResponse{
		Body:          resp.Body,
		ContentLength: headerInt64(resp.Header, "Content-Length"),
		LastModified:  headerTime(resp.Header, "Last-Modified"),
	}, nil
}

func (c *Client) HeadObject(ctx context.Context, bucket, key string) (*HeadObjectResponse, error) {
	req, err := c.newObjectRequest(ctx, http.MethodHead, bucket, key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return &HeadObjectResponse{
		ContentLength: headerInt64(resp.Header, "Content-Length"),
		LastModified:  headerTime(resp.Header, "Last-Modified"),
	}, nil
}

func (c *Client) PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	req, err := c.newObjectRequest(ctx, http.MethodPut, bucket, key, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.do(req, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	req, err := c.newObjectRequest(ctx, http.MethodDelete, bucket, key, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	req, err := c.newObjectRequest(ctx, http.MethodPut, dstBucket, dstKey, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-amz-copy-source", "/"+srcBucket+"/"+escapeCopySource(srcKey))
	resp, err := c.do(req, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) ListObjectsV2(ctx context.Context, bucket, prefix, delimiter string, maxKeys int) (*ListObjectsResponse, error) {
	return c.ListObjectsV2WithToken(ctx, bucket, prefix, delimiter, maxKeys, "")
}

func (c *Client) ListObjectsV2WithToken(ctx context.Context, bucket, prefix, delimiter string, maxKeys int, token string) (*ListObjectsResponse, error) {
	values := url.Values{}
	values.Set("list-type", "2")
	if prefix != "" {
		values.Set("prefix", prefix)
	}
	if delimiter != "" {
		values.Set("delimiter", delimiter)
	}
	if maxKeys > 0 {
		values.Set("max-keys", strconv.Itoa(maxKeys))
	}
	if token != "" {
		values.Set("continuation-token", token)
	}
	req, err := c.newBucketRequest(ctx, bucket, values)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &ListObjectsResponse{}, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed listBucketResult
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := &ListObjectsResponse{
		CommonPrefixes:        make([]string, 0, len(parsed.CommonPrefixes)),
		Contents:              make([]ListedObject, 0, len(parsed.Contents)),
		IsTruncated:           parsed.IsTruncated,
		NextContinuationToken: parsed.NextContinuationToken,
	}
	for _, cp := range parsed.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, cp.Prefix)
	}
	for _, obj := range parsed.Contents {
		out.Contents = append(out.Contents, ListedObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		})
	}
	return out, nil
}

func (c *Client) newObjectRequest(ctx context.Context, method, bucket, key string, body io.Reader) (*http.Request, error) {
	endpoint := *c.endpoint
	if c.usePathStyle {
		endpoint.Path = joinObjectURLPath(endpoint.Path, bucket, key)
	} else {
		endpoint.Host = bucket + "." + endpoint.Host
		endpoint.Path = joinObjectURLPath(endpoint.Path, key)
	}
	return http.NewRequestWithContext(ctx, method, endpoint.String(), body)
}

func (c *Client) newBucketRequest(ctx context.Context, bucket string, values url.Values) (*http.Request, error) {
	endpoint := *c.endpoint
	if c.usePathStyle {
		endpoint.Path = joinURLPath(endpoint.Path, bucket)
	} else {
		endpoint.Host = bucket + "." + endpoint.Host
	}
	endpoint.RawQuery = values.Encode()
	return http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
}

func (c *Client) do(req *http.Request, payload []byte) (*http.Response, error) {
	if payload == nil {
		payload = []byte{}
	}
	if c.accessKey != "" && c.secretKey != "" {
		c.signRequest(req, payload)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		if len(body) == 0 {
			return nil, fmt.Errorf("s3 request failed: %s", resp.Status)
		}
		return nil, fmt.Errorf("s3 request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
}

func (c *Client) signRequest(req *http.Request, payload []byte) {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzdate := now.Format("20060102T150405Z")

	req.Header.Set("x-amz-date", amzdate)
	req.Header.Set("x-amz-content-sha256", sha256Hex(payload))

	canonicalHeaders, signedHeaders := buildCanonicalHeaders(req)
	canonicalQuery := canonicalQueryString(req.URL.Query())
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		sha256Hex(payload),
	}, "\n")

	credentialScope := datestamp + "/" + c.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzdate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(c.secretKey, datestamp, c.region)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set(
		"Authorization",
		fmt.Sprintf(
			"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
			c.accessKey,
			credentialScope,
			signedHeaders,
			signature,
		),
	)
}

func buildCanonicalHeaders(req *http.Request) (string, string) {
	headers := map[string]string{
		"host": req.Host,
	}
	keys := []string{"host"}
	for name := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" {
			headers[lower] = strings.TrimSpace(req.Header.Get(name))
			keys = append(keys, lower)
		}
	}
	sort.Strings(keys)
	keys = unique(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+":"+headers[key])
	}
	return strings.Join(lines, "\n") + "\n", strings.Join(keys, ";")
}

func canonicalQueryString(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		items := append([]string(nil), values[key]...)
		sort.Strings(items)
		escapedKey := awsEscape(key)
		for _, item := range items {
			parts = append(parts, escapedKey+"="+awsEscape(item))
		}
	}
	return strings.Join(parts, "&")
}

func deriveSigningKey(secretKey, datestamp, region string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func headerInt64(header http.Header, key string) int64 {
	value := strings.TrimSpace(header.Get(key))
	if value == "" {
		return 0
	}
	out, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return out
}

func headerTime(header http.Header, key string) time.Time {
	value := strings.TrimSpace(header.Get(key))
	if value == "" {
		return time.Time{}
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func joinURLPath(base string, parts ...string) string {
	all := make([]string, 0, len(parts)+1)
	if base != "" && base != "/" {
		all = append(all, strings.Trim(base, "/"))
	}
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			all = append(all, part)
		}
	}
	return "/" + strings.Join(all, "/")
}

func joinObjectURLPath(base string, parts ...string) string {
	all := make([]string, 0, len(parts)+1)
	if base != "" && base != "/" {
		all = append(all, strings.Trim(base, "/"))
	}
	for i, part := range parts {
		part = normalizeObjectKey(part)
		if part == "" {
			continue
		}
		if i < len(parts)-1 {
			part = strings.Trim(part, "/")
		}
		if part != "" {
			all = append(all, part)
		}
	}
	return "/" + strings.Join(all, "/")
}

func normalizeObjectKey(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	hasTrailingSlash := strings.HasSuffix(key, "/")
	cleanKey := strings.TrimPrefix(path.Clean("/"+key), "/")
	if cleanKey == "." {
		cleanKey = ""
	}
	if hasTrailingSlash && cleanKey != "" && !strings.HasSuffix(cleanKey, "/") {
		cleanKey += "/"
	}
	return cleanKey
}

func escapeCopySource(key string) string {
	segments := strings.Split(key, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	return strings.Join(segments, "/")
}

func awsEscape(value string) string {
	escaped := url.QueryEscape(value)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "*", "%2A")
	return strings.ReplaceAll(escaped, "%7E", "~")
}

func unique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	prev := ""
	for i, item := range in {
		if i == 0 || item != prev {
			out = append(out, item)
		}
		prev = item
	}
	return out
}

type listBucketResult struct {
	XMLName               xml.Name               `xml:"ListBucketResult"`
	IsTruncated           bool                   `xml:"IsTruncated"`
	NextContinuationToken string                 `xml:"NextContinuationToken"`
	CommonPrefixes        []listBucketPrefix     `xml:"CommonPrefixes"`
	Contents              []listBucketResultItem `xml:"Contents"`
}

type listBucketPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listBucketResultItem struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	Size         int64     `xml:"Size"`
}
