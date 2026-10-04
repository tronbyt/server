package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHandleHealth(t *testing.T) {
	s := newTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	expected := "OK"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expected)
	}
}

func TestWithSystemAppsRefreshSerializesRefreshes(t *testing.T) {
	s := newTestServer(t)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	results := make(chan error, 2)
	var calls atomic.Int32

	go func() {
		results <- s.withSystemAppsRefresh(func() error {
			calls.Add(1)
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()

	<-firstEntered
	go func() {
		results <- s.withSystemAppsRefresh(func() error {
			calls.Add(1)
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second refresh started before the first refresh completed")
	case <-time.After(50 * time.Millisecond):
	}

	require.Equal(t, int32(1), calls.Load())
	close(releaseFirst)

	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second refresh did not start after the first refresh completed")
	}

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, int32(2), calls.Load())
}

func TestWithSystemAppsRefreshReturnsRefreshError(t *testing.T) {
	s := newTestServer(t)
	want := errors.New("refresh failed")

	err := s.withSystemAppsRefresh(func() error { return want })

	require.ErrorIs(t, err, want)
}
