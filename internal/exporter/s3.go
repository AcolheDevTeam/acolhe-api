package exporter

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
	"path"
	"strconv"
	"strings"
	"time"
)

type S3Config struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKeyID  string
	SecretKey    string
	SessionToken string
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type S3ObjectStore struct {
	config S3Config
	base   *url.URL
	client HTTPDoer
}

func NewS3ObjectStore(config S3Config, client HTTPDoer) (*S3ObjectStore, error) {
	if config.Endpoint == "" ||
		config.Region == "" ||
		config.Bucket == "" ||
		config.AccessKeyID == "" ||
		config.SecretKey == "" {
		return nil, errors.New("configuração S3 incompleta")
	}
	base, err := url.Parse(config.Endpoint)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("endpoint S3 inválido")
	}
	if base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("endpoint S3 não pode conter query ou fragment")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &S3ObjectStore{config: config, base: base, client: client}, nil
}

func (store *S3ObjectStore) Put(
	ctx context.Context,
	key string,
	contentType string,
	content []byte,
) error {
	now := time.Now().UTC()
	target, err := store.objectURL(key)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		target.String(),
		bytes.NewReader(content),
	)
	if err != nil {
		return err
	}
	payloadDigest := sha256.Sum256(content)
	payloadHash := hex.EncodeToString(payloadDigest[:])
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	if store.config.SessionToken != "" {
		request.Header.Set("X-Amz-Security-Token", store.config.SessionToken)
	}
	request.Header.Set(
		"Authorization",
		store.authorizationHeader(request, payloadHash, now),
	)

	response, err := store.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("S3 PUT retornou %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (store *S3ObjectStore) PresignGet(
	key string,
	expiresIn time.Duration,
	now time.Time,
) (string, error) {
	if expiresIn <= 0 || expiresIn > 7*24*time.Hour {
		return "", errors.New("validade do link S3 deve ficar entre 1s e 7 dias")
	}
	target, err := store.objectURL(key)
	if err != nil {
		return "", err
	}
	now = now.UTC()
	date := now.Format("20060102")
	scope := date + "/" + store.config.Region + "/s3/aws4_request"
	query := target.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", store.config.AccessKeyID+"/"+scope)
	query.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", strconv.FormatInt(int64(expiresIn/time.Second), 10))
	query.Set("X-Amz-SignedHeaders", "host")
	if store.config.SessionToken != "" {
		query.Set("X-Amz-Security-Token", store.config.SessionToken)
	}
	target.RawQuery = query.Encode()

	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		target.EscapedPath(),
		target.RawQuery,
		"host:" + target.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		scope,
		hex.EncodeToString(requestHash[:]),
	}, "\n")
	signature := hex.EncodeToString(
		hmacSHA256(store.signingKey(date), []byte(stringToSign)),
	)
	query.Set("X-Amz-Signature", signature)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (store *S3ObjectStore) authorizationHeader(
	request *http.Request,
	payloadHash string,
	now time.Time,
) string {
	date := now.Format("20060102")
	scope := date + "/" + store.config.Region + "/s3/aws4_request"
	headerNames := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	canonicalHeaders := strings.Join([]string{
		"content-type:" + strings.TrimSpace(request.Header.Get("Content-Type")),
		"host:" + request.URL.Host,
		"x-amz-content-sha256:" + payloadHash,
		"x-amz-date:" + request.Header.Get("X-Amz-Date"),
	}, "\n") + "\n"
	if store.config.SessionToken != "" {
		headerNames = append(headerNames, "x-amz-security-token")
		canonicalHeaders += "x-amz-security-token:" + store.config.SessionToken + "\n"
	}
	signedHeaders := strings.Join(headerNames, ";")
	canonicalRequest := strings.Join([]string{
		request.Method,
		request.URL.EscapedPath(),
		request.URL.Query().Encode(),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		request.Header.Get("X-Amz-Date"),
		scope,
		hex.EncodeToString(requestHash[:]),
	}, "\n")
	signature := hex.EncodeToString(
		hmacSHA256(store.signingKey(date), []byte(stringToSign)),
	)
	return fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		store.config.AccessKeyID,
		scope,
		signedHeaders,
		signature,
	)
}

func (store *S3ObjectStore) signingKey(date string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+store.config.SecretKey), []byte(date))
	regionKey := hmacSHA256(dateKey, []byte(store.config.Region))
	serviceKey := hmacSHA256(regionKey, []byte("s3"))
	return hmacSHA256(serviceKey, []byte("aws4_request"))
}

func (store *S3ObjectStore) objectURL(key string) (*url.URL, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return nil, errors.New("chave S3 inválida")
	}
	target := *store.base
	target.Path = path.Join(store.base.Path, store.config.Bucket, key)
	target.RawPath = ""
	return &target, nil
}

func hmacSHA256(key, value []byte) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write(value)
	return hash.Sum(nil)
}
