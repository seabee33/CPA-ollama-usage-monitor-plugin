package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const balanceURL = "https://ollama.com/api/balance"
const maxBody = 128 * 1024
const cacheTTL = time.Minute
const maxCacheEntries = 256

type cacheEntry struct {
	value quotaResponse
	err   error
	until time.Time
}
type monitor struct {
	mu     sync.Mutex
	cache  map[[32]byte]cacheEntry
	client *http.Client
	now    func() time.Time
}

func newMonitor() *monitor {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 2
	transport.MaxIdleConnsPerHost = 2
	transport.ResponseHeaderTimeout = 10 * time.Second
	return &monitor{
		cache: make(map[[32]byte]cacheEntry), now: time.Now,
		client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (m *monitor) fetch(key string) (quotaResponse, error) {
	// Serialize upstream requests, including refresh-all, to bound work inside
	// the CPA process. Concurrent requests for the same key reuse the result.
	m.mu.Lock()
	defer m.mu.Unlock()
	id := sha256.Sum256([]byte(key))
	now := m.now()
	if e, ok := m.cache[id]; ok && now.Before(e.until) {
		return e.value, e.err
	}
	for k, e := range m.cache {
		if !now.Before(e.until) {
			delete(m.cache, k)
		}
	}
	// Do not evict live entries: eviction could bypass the upstream cooldown.
	if len(m.cache) >= maxCacheEntries {
		return quotaResponse{}, errors.New("monitor is at capacity; retry after one minute")
	}
	value, retry, err := m.request(key)
	if retry < cacheTTL {
		retry = cacheTTL
	}
	m.cache[id] = cacheEntry{value: value, err: err, until: m.now().Add(retry)}
	return value, err
}

func (m *monitor) request(key string) (quotaResponse, time.Duration, error) {
	req, _ := http.NewRequest(http.MethodGet, balanceURL, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", pluginID+"/"+version)
	resp, err := m.client.Do(req)
	if err != nil {
		return quotaResponse{}, 0, errors.New("Ollama balance request failed; check network connectivity and TLS")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message := fmt.Sprintf("Ollama balance API returned HTTP %d", resp.StatusCode)
		switch resp.StatusCode {
		case 401:
			message = "Ollama rejected the API key (401); update the key in AI Providers"
		case 403:
			message = "Ollama denied balance access (403); check account access"
		case 404:
			message = "Ollama balance API is unavailable (404); no cookie-scraping fallback is used"
		case 429:
			message = "Ollama rate limit reached (429); wait before refreshing"
		}
		// Never echo upstream bodies, which could contain credentials or HTML.
		return quotaResponse{}, retryAfter(resp.Header.Get("Retry-After"), m.now()), errors.New(message)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return quotaResponse{}, 0, errors.New("could not read Ollama balance response")
	}
	if len(raw) > maxBody {
		return quotaResponse{}, 0, errors.New("Ollama balance response exceeded size limit")
	}
	value, err := normalizeBalance(raw)
	return value, 0, err
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		// Saturate before duration conversion to prevent overflow.
		if seconds > 86400 {
			return 24 * time.Hour
		}
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil && t.After(now) {
		if t.Sub(now) > 24*time.Hour {
			return 24 * time.Hour
		}
		return t.Sub(now)
	}
	return cacheTTL
}
