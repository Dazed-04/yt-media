package netutil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func get(url string) func() (*http.Request, error) {
	return func() (*http.Request, error) { return http.NewRequest("GET", url, nil) }
}

func TestRetriesThenSucceeds(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	lim := NewLimiter(0)
	resp, err := Do(context.Background(), srv.Client(), lim, Retry{Max: 3, Base: 5 * time.Millisecond}, get(srv.URL))
	if err != nil || resp.StatusCode != 200 || n.Load() != 3 {
		t.Fatalf("resp=%v err=%v hits=%d", resp, err, n.Load())
	}
	resp.Body.Close()
}

func TestGivesUpWithErrRateLimited(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(429)
	}))
	defer srv.Close()
	_, err := Do(context.Background(), srv.Client(), NewLimiter(0), Retry{Max: 2, Base: time.Millisecond}, get(srv.URL))
	if !errors.Is(err, ErrRateLimited) || n.Load() != 3 {
		t.Fatalf("err=%v hits=%d", err, n.Load())
	}
}

func TestRetryAfterIsHonouredAndCapped(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "100")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	start := time.Now()
	resp, err := Do(context.Background(), srv.Client(), NewLimiter(0), Retry{Max: 1, Base: time.Millisecond, MaxWait: 80 * time.Millisecond}, get(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if d := time.Since(start); d < 70*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v, want ~80ms (the cap)", d)
	}
}

func TestOtherStatusesPassThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	resp, err := Do(context.Background(), srv.Client(), nil, Retry{Max: 3}, get(srv.URL))
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
}

func TestLimiterSpacesCallersAndPauseHoldsEveryone(t *testing.T) {
	l := NewLimiter(40 * time.Millisecond)
	start := time.Now()
	for range 3 {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 75*time.Millisecond {
		t.Errorf("3 waits took %v", d)
	}
	l.Pause(100 * time.Millisecond)
	start = time.Now()
	l.Wait(context.Background())
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Errorf("after Pause the wait was only %v", d)
	}
}

func TestContextCancelStopsWaiting(t *testing.T) {
	l := NewLimiter(time.Hour)
	l.Wait(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("expected context error")
	}
}
