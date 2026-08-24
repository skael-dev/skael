package api

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{"no header, first attempt", 1, 0, 2 * time.Second},
		{"no header, second attempt", 2, 0, 4 * time.Second},
		{"header wins over the ladder", 1, 7 * time.Second, 7 * time.Second},
		{"header over the clamp", 1, 10 * time.Minute, maxRetryAfter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := backoff(c.attempt, c.retryAfter); got != c.want {
				t.Errorf("backoff(%d, %s) = %s, want %s", c.attempt, c.retryAfter, got, c.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{
		"":                              0,
		"3":                             3 * time.Second,
		" 12 ":                          12 * time.Second,
		"-1":                            0,
		"Wed, 21 Oct 2015 07:28:00 GMT": 0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestJitterStaysInBand(t *testing.T) {
	base := 10 * time.Second
	for i := 0; i < 200; i++ {
		got := jitter(base)
		if got < 8*time.Second || got > 12*time.Second {
			t.Fatalf("jitter(%s) = %s, outside plus or minus 20%%", base, got)
		}
	}
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %s, want 0", got)
	}
}
