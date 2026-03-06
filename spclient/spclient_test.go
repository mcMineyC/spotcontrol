package spclient

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterDuration_DeltaSeconds(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"5"},
		},
	}

	d := retryAfterDuration(resp, 10*time.Second)
	if d != 5*time.Second {
		t.Errorf("expected 5s, got %s", d)
	}
}

func TestRetryAfterDuration_LargeDeltaSeconds(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"120"},
		},
	}

	d := retryAfterDuration(resp, 10*time.Second)
	if d != 120*time.Second {
		t.Errorf("expected 120s, got %s", d)
	}
}

func TestRetryAfterDuration_ZeroSeconds(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"0"},
		},
	}

	fallback := 7 * time.Second
	d := retryAfterDuration(resp, fallback)
	// 0 is not > 0, so it should fall through to HTTP-date parsing, fail, and return fallback.
	if d != fallback {
		t.Errorf("expected fallback %s for zero seconds, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_NegativeSeconds(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"-3"},
		},
	}

	fallback := 8 * time.Second
	d := retryAfterDuration(resp, fallback)
	if d != fallback {
		t.Errorf("expected fallback %s for negative seconds, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_HTTPDate(t *testing.T) {
	future := time.Now().Add(30 * time.Second).UTC().Format(time.RFC1123)
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{future},
		},
	}

	d := retryAfterDuration(resp, 10*time.Second)
	// Should be roughly 30 seconds, but allow some tolerance for test execution time.
	if d < 28*time.Second || d > 32*time.Second {
		t.Errorf("expected ~30s for HTTP-date, got %s", d)
	}
}

func TestRetryAfterDuration_HTTPDateInPast(t *testing.T) {
	past := time.Now().Add(-10 * time.Second).UTC().Format(time.RFC1123)
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{past},
		},
	}

	fallback := 5 * time.Second
	d := retryAfterDuration(resp, fallback)
	// Date is in the past, duration would be <= 0, so fallback should be returned.
	if d != fallback {
		t.Errorf("expected fallback %s for past HTTP-date, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_MissingHeader(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}

	fallback := 3 * time.Second
	d := retryAfterDuration(resp, fallback)
	if d != fallback {
		t.Errorf("expected fallback %s for missing header, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_EmptyHeader(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{""},
		},
	}

	fallback := 4 * time.Second
	d := retryAfterDuration(resp, fallback)
	if d != fallback {
		t.Errorf("expected fallback %s for empty header, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_GarbageValue(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"not-a-number-or-date"},
		},
	}

	fallback := 6 * time.Second
	d := retryAfterDuration(resp, fallback)
	if d != fallback {
		t.Errorf("expected fallback %s for garbage value, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_FloatValue(t *testing.T) {
	// Retry-After with a float should not parse as int, and is not a valid HTTP-date.
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"2.5"},
		},
	}

	fallback := 9 * time.Second
	d := retryAfterDuration(resp, fallback)
	if d != fallback {
		t.Errorf("expected fallback %s for float value, got %s", fallback, d)
	}
}

func TestRetryAfterDuration_OneSecond(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"1"},
		},
	}

	d := retryAfterDuration(resp, 10*time.Second)
	if d != 1*time.Second {
		t.Errorf("expected 1s, got %s", d)
	}
}
