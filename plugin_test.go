package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const legacyFixture = `{"included":{"session":{"remaining_percent":75,"resets_at":"2026-11-09T17:00:00Z"},"weekly":{"remaining_percent":40,"resets_at":"2026-11-15T00:00:00Z"}},"purchased":{"balance_usd":25}}`

func TestLegacyQuotaContract(t *testing.T) {
	q, err := normalizeBalance([]byte(legacyFixture))
	if err != nil {
		t.Fatal(err)
	}
	bs := q.Groups[0].Buckets
	if len(bs) != 2 || bs[0].Window != "5h" || bs[0].RemainingFraction != .75 || bs[1].Window != "weekly" || bs[1].RemainingFraction != .4 {
		t.Fatalf("wrong quota: %+v", bs)
	}
	if bs[0].ResetTime != "2026-11-09T17:00:00Z" {
		t.Fatal(bs[0])
	}
	raw, _ := json.Marshal(q)
	if !strings.Contains(string(raw), `"remainingFraction":0.75`) {
		t.Fatal(string(raw))
	}
}
func TestCreditBalanceAndInvalidSchemas(t *testing.T) {
	q, err := normalizeBalance([]byte(`{"included":{"balance_usd":72.5,"allowance_usd":100,"period":{"until":"2026-12-01T00:00:00Z"}}}`))
	if err != nil || q.Groups[0].Buckets[0].RemainingFraction != .725 || len(q.Summary) != 2 {
		t.Fatalf("%+v %v", q, err)
	}
	for _, raw := range []string{`{}`, `null`, `{"included":{}}`, `{"included":{"session":{}}}`, strings.Replace(legacyFixture, `"remaining_percent":75`, `"remaining_percent":101`, 1), strings.Replace(legacyFixture, `"remaining_percent":75`, `"remaining_percent":null`, 1), strings.Replace(legacyFixture, "2026-11-09T17:00:00Z", "bad timestamp", 1), legacyFixture + `{}`} {
		if _, err := normalizeBalance([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid schema: %s", raw)
		}
	}
}
func TestCredentialBoundary(t *testing.T) {
	for _, base := range []string{"http://ollama.com/v1", "https://ollama.com.attacker.test/v1", "https://attacker.test/v1", "https://ollama.com@attacker.test/v1", "http://127.0.0.1:11434/v1", ""} {
		if _, err := credential(quotaRequest{Attributes: map[string]string{"base_url": base, "api_key": "secret"}}); err == nil {
			t.Fatalf("accepted %q", base)
		}
	}
	for _, key := range []string{"", "secret\r\nX-Test: injected", strings.Repeat("x", 4097)} {
		if _, err := credential(quotaRequest{Attributes: map[string]string{"base_url": "https://ollama.com/v1", "api_key": key}}); err == nil {
			t.Fatal("accepted invalid key")
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestConcurrentRefreshCacheAndRotation(t *testing.T) {
	m := newMonitor()
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	var count atomic.Int32
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		if r.URL.String() != balanceURL || r.Method != "GET" || r.Header.Get("Authorization") == "" {
			t.Error("wrong upstream request")
		}
		return mockResponse(200, legacyFixture), nil
	})
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.fetch("secret"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("refresh stampede", count.Load())
	}
	if _, err := m.fetch("rotated"); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 2 {
		t.Fatal("key rotation reused old cache")
	}
	now = now.Add(time.Minute)
	if _, err := m.fetch("secret"); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 3 {
		t.Fatal("cache did not expire")
	}
}
func TestRateLimitBackoffAndSecretRedaction(t *testing.T) {
	m := newMonitor()
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	count := 0
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		count++
		r := mockResponse(429, "PRIVATE-KEY")
		r.Header.Set("Retry-After", "120")
		return r, nil
	})
	_, err := m.fetch("PRIVATE-KEY")
	if err == nil || strings.Contains(err.Error(), "PRIVATE-KEY") {
		t.Fatal(err)
	}
	now = now.Add(90 * time.Second)
	_, _ = m.fetch("PRIVATE-KEY")
	if count != 1 {
		t.Fatal("ignored Retry-After")
	}
	now = now.Add(31 * time.Second)
	_, _ = m.fetch("PRIVATE-KEY")
	if count != 2 {
		t.Fatal("backoff did not expire")
	}
	m = newMonitor()
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("PRIVATE-KEY") })
	_, err = m.fetch("PRIVATE-KEY")
	if err == nil || strings.Contains(err.Error(), "PRIVATE-KEY") {
		t.Fatal(err)
	}
}
func TestRedirectsNeverForwardCredentials(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	m := newMonitor()
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := mockResponse(302, "")
		r.Header.Set("Location", target.URL)
		return r, nil
	})
	_, err := m.fetch("secret")
	if err == nil || hits.Load() != 0 {
		t.Fatal("redirect followed", err, hits.Load())
	}
}
func TestOversizedAndTruncatedBodies(t *testing.T) {
	for _, body := range []string{strings.Repeat(" ", maxBody+1), legacyFixture[:30]} {
		m := newMonitor()
		m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(200, body), nil })
		if _, err := m.fetch("secret"); err == nil {
			t.Fatal("accepted bad body")
		}
	}
}
func TestLifecycleAndReadOnlySurface(t *testing.T) {
	p := newPlugin()
	raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("enabled: true\n")})
	if _, err := p.handle("plugin.register", raw); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"quota.fetch", "quota.reset", "host.auth.save"} {
		out, err := p.dispatch(method, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var e envelope
		if err = json.Unmarshal(out, &e); err != nil || e.OK || e.Error == nil {
			t.Fatalf("unsafe method %s: %s", method, out)
		}
	}
	// YAML errors must not echo secrets in parser diagnostics.
	raw, _ = json.Marshal(map[string]any{"config_yaml": []byte("provider_names: [PRIVATE-KEY")})
	out, _ := p.dispatch("plugin.reconfigure", raw)
	if strings.Contains(string(out), "PRIVATE-KEY") {
		t.Fatal("secret leaked")
	}
}
func TestRetryAfterFormats(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if got := retryAfter(now.Add(2*time.Minute).Format(http.TimeFormat), now); got != 2*time.Minute {
		t.Fatal(got)
	}
	if got := retryAfter("9223372036854775807", now); got != 24*time.Hour {
		t.Fatal(got)
	}
}
