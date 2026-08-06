package webhookcore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultCaller_Call_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(200)
		w.Write([]byte(`{"jobId":"j1"}`))
	}))
	defer srv.Close()

	c := &DefaultCaller{}
	result, err := c.Call(context.Background(), CallConfig{
		Method: "POST",
		URL:    srv.URL,
		Body:   `{"model":"test"}`,
		Header: "Bearer test-token",
	})
	require.NoError(t, err)
	require.Equal(t, 200, result.StatusCode)
	require.Equal(t, `{"jobId":"j1"}`, string(result.Body))
}

func TestDefaultCaller_Call_NoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "", r.Header.Get("Content-Type"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := &DefaultCaller{}
	_, err := c.Call(context.Background(), CallConfig{
		Method: "GET",
		URL:    srv.URL,
	})
	require.NoError(t, err)
}

func TestDefaultCaller_Call_NoAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "", r.Header.Get("Authorization"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := &DefaultCaller{}
	_, err := c.Call(context.Background(), CallConfig{
		Method: "GET",
		URL:    srv.URL,
	})
	require.NoError(t, err)
}

func TestDefaultCaller_Call_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	c := &DefaultCaller{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Call(ctx, CallConfig{
		Method: "GET",
		URL:    srv.URL,
	})
	require.Error(t, err)
}
