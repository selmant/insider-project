package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGzip_CompressesWhenAccepted(t *testing.T) {
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))

	// Decompress and verify
	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	defer func() { _ = gr.Close() }()
	body, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Equal(t, `{"status":"ok"}`, string(body))
}

func TestGzip_NoCompressionWithoutAcceptHeader(t *testing.T) {
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain response"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	// No Accept-Encoding header
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Equal(t, "plain response", w.Body.String())
}

func TestGzip_HandlesDeflateOnly(t *testing.T) {
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("response"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "deflate")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should not compress — only gzip requested handling
	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Equal(t, "response", w.Body.String())
}

func TestGzip_SetsContentEncodingHeader(t *testing.T) {
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))

	// Verify the body is valid gzip
	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	defer func() { _ = gr.Close() }()
	body, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Equal(t, "test", string(body))
}
