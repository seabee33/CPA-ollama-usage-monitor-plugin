package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testToken = "synthetic-dashboard-token-32-characters"

func configuredPlugin(t *testing.T) *plugin {
	t.Helper()
	p := newPlugin()
	raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("dashboard_token: " + testToken + "\naccounts:\n  - name: personal\n    api_key: PRIVATE-OLLAMA-KEY\n")})
	if _, err := p.handle("plugin.register", raw); err != nil {
		t.Fatal(err)
	}
	return p
}
func callPage(t *testing.T, p *plugin, path, token string, query map[string][]string) managementResponse {
	t.Helper()
	req := managementRequest{Method: "GET", Path: resourceBase + path, Headers: map[string][]string{}, Query: query}
	if token != "" {
		req.Headers[dashboardTokenHeader] = []string{token}
	}
	raw, _ := json.Marshal(req)
	resp, err := p.management(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
func TestDashboardAuthenticationAndIsolation(t *testing.T) {
	p := configuredPlugin(t)
	count := 0
	p.monitor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { count++; return mockResponse(200, legacyFixture), nil })
	for _, token := range []string{"", "incorrect"} {
		resp := callPage(t, p, "/data", token, map[string][]string{"account": {"personal"}})
		if resp.StatusCode != 401 || strings.Contains(string(resp.Body), "personal") {
			t.Fatal(resp)
		}
	}
	if count != 0 {
		t.Fatal("unauthorized request contacted upstream")
	}
	resp := callPage(t, p, "/dashboard", "", nil)
	if resp.StatusCode != 200 || strings.Contains(string(resp.Body), "PRIVATE-OLLAMA-KEY") || strings.Contains(string(resp.Body), testToken) {
		t.Fatal("unsafe public shell")
	}
	resp = callPage(t, p, "/data", testToken, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(resp.Body), "personal") || strings.Contains(string(resp.Body), "PRIVATE-OLLAMA-KEY") {
		t.Fatal(string(resp.Body))
	}
	resp = callPage(t, p, "/data", testToken, map[string][]string{"account": {"personal"}})
	if resp.StatusCode != 200 || !strings.Contains(string(resp.Body), `"remainingFraction":0.75`) || count != 1 {
		t.Fatal(string(resp.Body))
	}
	if resp.Headers["Cache-Control"][0] != "no-store" {
		t.Fatal("cacheable private data")
	}
	// A query parameter cannot substitute for authentication.
	resp = callPage(t, p, "/data", "", map[string][]string{"token": {testToken}})
	if resp.StatusCode != 401 {
		t.Fatal(resp)
	}
	req := managementRequest{Method: "GET", Path: resourceBase + "/data", Headers: map[string][]string{dashboardTokenHeader: {testToken, testToken}}}
	raw, _ := json.Marshal(req)
	resp, _ = p.management(raw)
	if resp.StatusCode != 401 {
		t.Fatal("duplicate token accepted")
	}
	raw, _ = json.Marshal(map[string]any{"config_yaml": []byte("accounts: [invalid")})
	_, _ = p.handle("plugin.reconfigure", raw)
	if resp = callPage(t, p, "/data", testToken, nil); resp.StatusCode != 503 {
		t.Fatal("invalid config retained old access")
	}
}
func TestConfigEnvironmentAndValidation(t *testing.T) {
	t.Setenv("TEST_DASH_TOKEN", testToken)
	t.Setenv("TEST_OLLAMA_KEY", "secret")
	token, accounts, err := validateConfig(pluginConfig{DashboardTokenEnv: "TEST_DASH_TOKEN", Accounts: []accountConfig{{Name: "work", APIKeyEnv: "TEST_OLLAMA_KEY"}}})
	if err != nil || token != testToken || accounts[0].APIKey != "secret" {
		t.Fatal(err)
	}
	for _, cfg := range []pluginConfig{
		{DashboardToken: "short"},
		{DashboardTokenEnv: "MISSING_SECRET_ENV"},
		{DashboardToken: testToken, DashboardTokenEnv: "TEST_DASH_TOKEN"},
		{Accounts: []accountConfig{{Name: "same", APIKey: "a"}, {Name: "same", APIKey: "b"}}},
		{DashboardToken: testToken, Accounts: []accountConfig{{Name: "work", APIKey: testToken}}},
	} {
		if _, _, err := validateConfig(cfg); err == nil {
			t.Fatal("accepted bad configuration")
		}
	}
}
