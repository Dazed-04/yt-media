// Package netutil holds what the LRCLIB and MusicBrainz clients share: a
// request limiter, and retries that respect "slow down" answers.
package netutil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrRateLimited means the service kept answering "too many requests" or
// "unavailable" even after waiting and retrying.
var ErrRateLimited = errors.New("rate limited or service busy")

// Limiter spaces requests at least Interval apart, across all goroutines.
type Limiter struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func NewLimiter(interval time.Duration) *Limiter { return &Limiter{interval: interval} }

// SetInterval changes the spacing (used by tests).
func (l *Limiter) SetInterval(d time.Duration) {
	l.mu.Lock()
	l.interval = d
	l.mu.Unlock()
}

// Wait blocks until this caller may send a request.
func (l *Limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()

	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// Pause holds back every caller for d, for when the service said to slow down.
func (l *Limiter) Pause(d time.Duration) {
	l.mu.Lock()
	if until := time.Now().Add(d); until.After(l.next) {
		l.next = until
	}
	l.mu.Unlock()
}

// Retry says how patient to be.
type Retry struct {
	Max     int           // retries after the first attempt
	Base    time.Duration // first wait; doubles each retry
	MaxWait time.Duration // cap for any single wait (including Retry-After)
}

// Do sends the request built by build, waiting its turn on lim (may be nil).
// On 429/502/503/504 it waits - for as long as Retry-After says, otherwise
// with a doubling delay - and tries again, pausing the limiter so other
// goroutines back off too. Any other response is returned for the caller to
// judge. When retries run out the error wraps ErrRateLimited.
func Do(ctx context.Context, c *http.Client, lim *Limiter, rt Retry, build func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if lim != nil {
			if err := lim.Wait(ctx); err != nil {
				return nil, err
			}
		}
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		default:
			return resp, nil
		}

		code := resp.StatusCode
		wait := retryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		if attempt >= rt.Max {
			return nil, fmt.Errorf("%w (HTTP %d)", ErrRateLimited, code)
		}
		if wait <= 0 {
			wait = rt.Base << attempt
		}
		if rt.MaxWait > 0 && wait > rt.MaxWait {
			wait = rt.MaxWait
		}
		if lim != nil {
			lim.Pause(wait)
		} else {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
}

// retryAfter reads a Retry-After header given in seconds (the HTTP-date form
// is rare for APIs and treated as "not given").
func retryAfter(v string) time.Duration {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}
