package exporter_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/exporter"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (run roundTripFunc) Do(request *http.Request) (*http.Response, error) {
	return run(request)
}

func TestS3ObjectStoreSignsPrivateUploadAndDownload(t *testing.T) {
	var uploaded *http.Request
	var uploadedBody []byte
	client := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		uploaded = request.Clone(request.Context())
		var err error
		uploadedBody, err = io.ReadAll(request.Body)
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})
	store, err := exporter.NewS3ObjectStore(exporter.S3Config{
		Endpoint: "https://s3.example.test", Region: "sa-east-1",
		Bucket: "private-exports", AccessKeyID: "access", SecretKey: "secret",
	}, client)
	require.NoError(t, err)

	require.NoError(t, store.Put(
		context.Background(),
		"lgpd/org/patient/request.zip",
		"application/zip",
		[]byte("archive"),
	))
	require.NotNil(t, uploaded)
	assert.Equal(t, http.MethodPut, uploaded.Method)
	assert.Equal(t, "/private-exports/lgpd/org/patient/request.zip", uploaded.URL.Path)
	assert.Equal(t, []byte("archive"), uploadedBody)
	assert.Contains(t, uploaded.Header.Get("Authorization"), "AWS4-HMAC-SHA256")
	assert.Len(t, uploaded.Header.Get("X-Amz-Content-Sha256"), 64)

	now := time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	signed, err := store.PresignGet(
		"lgpd/org/patient/request.zip",
		24*time.Hour,
		now,
	)
	require.NoError(t, err)
	link, err := url.Parse(signed)
	require.NoError(t, err)
	assert.Equal(t, "https", link.Scheme)
	assert.Equal(t, "86400", link.Query().Get("X-Amz-Expires"))
	assert.Equal(t, "AWS4-HMAC-SHA256", link.Query().Get("X-Amz-Algorithm"))
	assert.NotEmpty(t, link.Query().Get("X-Amz-Signature"))
	assert.NotContains(t, signed, "secret")
}

func TestS3ObjectStoreRejectsUnsafeKeyAndLongLink(t *testing.T) {
	store, err := exporter.NewS3ObjectStore(exporter.S3Config{
		Endpoint: "https://s3.example.test", Region: "sa-east-1",
		Bucket: "private-exports", AccessKeyID: "access", SecretKey: "secret",
	}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		panic("request should not be sent")
	}))
	require.NoError(t, err)
	assert.Error(t, store.Put(context.Background(), "../escape", "text/plain", nil))
	_, err = store.PresignGet("safe", 8*24*time.Hour, time.Now())
	assert.Error(t, err)
}
