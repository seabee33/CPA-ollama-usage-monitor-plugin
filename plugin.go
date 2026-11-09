// Package main implements a read-only CPA Ollama sidebar monitor.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

const pluginID = "ollama-usage-monitor"

var version = "0.2.0"

// These wire types mirror CPA's quota-provider RPC contract. Keeping the small
// contract local avoids linking an entire second copy of the proxy into CPA.
type quotaRequest struct {
	Attributes map[string]string `json:"attributes"`
}
type quotaResponse struct {
	Subscription *subscription `json:"subscription,omitempty"`
	Groups       []group       `json:"groups,omitempty"`
	Summary      []metric      `json:"summary,omitempty"`
}
type subscription struct {
	Plan string `json:"plan"`
}
type group struct {
	DisplayName string   `json:"displayName"`
	Buckets     []bucket `json:"buckets"`
}
type bucket struct {
	Window            string  `json:"window"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description"`
}
type metric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Format   string  `json:"format"`
	Currency string  `json:"currency,omitempty"`
}
type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type envelope struct {
	OK     bool      `json:"ok"`
	Result any       `json:"result,omitempty"`
	Error  *rpcError `json:"error,omitempty"`
}

type pluginConfig struct {
	DashboardToken    string          `yaml:"dashboard_token"`
	DashboardTokenEnv string          `yaml:"dashboard_token_env"`
	Accounts          []accountConfig `yaml:"accounts"`
}
type accountConfig struct {
	Name      string `yaml:"name"`
	APIKey    string `yaml:"api_key"`
	APIKeyEnv string `yaml:"api_key_env"`
}
type plugin struct {
	mu         sync.RWMutex
	tokenHash  [32]byte
	configured bool
	accounts   []accountConfig
	monitor    *monitor
}

func newPlugin() *plugin {
	return &plugin{monitor: newMonitor()}
}

var app = newPlugin()

func (p *plugin) dispatch(method string, raw []byte) ([]byte, error) {
	result, err := p.handle(method, raw)
	if err != nil {
		return json.Marshal(envelope{Error: &rpcError{Code: "quota_error", Message: err.Error()}})
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func (p *plugin) handle(method string, raw []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		// Invalidate access before parsing, so malformed reloads cannot retain an old token.
		p.mu.Lock()
		p.configured = false
		p.accounts = nil
		p.mu.Unlock()
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid lifecycle request")
		}
		cfg := pluginConfig{}
		if err := yaml.Unmarshal(req.ConfigYAML, &cfg); err != nil {
			return nil, errors.New("invalid plugin YAML configuration")
		}
		token, accounts, err := validateConfig(cfg)
		p.mu.Lock()
		// Fail closed on invalid replacement configuration, including token removal.
		p.configured = err == nil && token != ""
		p.tokenHash = sha256.Sum256([]byte(token))
		p.accounts = accounts
		p.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"schema_version": 1,
			"metadata": map[string]any{
				"Name": pluginID, "Version": version, "Author": "seabee33",
				"GitHubRepository": "https://github.com/seabee33/CPA-ollama-usage-monitor-plugin",
				"ConfigFields": []map[string]string{
					{"Name": "dashboard_token_env", "Type": "string", "Description": "Environment variable containing a separate random dashboard access token (at least 32 characters)."},
					{"Name": "dashboard_token", "Type": "string", "Description": "Dashboard access token; prefer dashboard_token_env. Not your CPA management key."},
					{"Name": "accounts", "Type": "array", "Description": "Accounts with name and api_key_env (recommended) or api_key."},
				},
			},
			"capabilities": map[string]bool{"management_api": true},
		}, nil
	case "management.register":
		return managementRegistration(), nil
	case "management.handle":
		return p.management(raw)
	case "quota.reset":
		return nil, errors.New("read-only monitor: Ollama quota cannot be reset")
	case "plugin.shutdown":
		return struct{}{}, nil
	default:
		return nil, errors.New("unsupported plugin method")
	}
}

// Do not accept arbitrary upstream URLs or credentials belonging to another
// provider, even when an administrator explicitly selects this quota plugin.
func credential(req quotaRequest) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(req.Attributes["base_url"]), "/")
	if base != "https://ollama.com/v1" && base != "https://ollama.com/api" && base != "https://ollama.com" {
		return "", errors.New("select an Ollama Cloud credential with base URL https://ollama.com/v1")
	}
	key := strings.TrimSpace(req.Attributes["api_key"])
	if len(key) == 0 || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
		return "", errors.New("selected credential has no valid Ollama API key")
	}
	return key, nil
}

func main() {}

func validateConfig(cfg pluginConfig) (string, []accountConfig, error) {
	resolve := func(value, env string) (string, error) {
		if value != "" && env != "" {
			return "", errors.New("choose a literal secret or an environment variable, not both")
		}
		if env != "" {
			value = os.Getenv(env)
			if value == "" {
				return "", errors.New("configured secret environment variable is empty or missing")
			}
		}
		return strings.TrimSpace(value), nil
	}
	token, err := resolve(cfg.DashboardToken, cfg.DashboardTokenEnv)
	if err != nil {
		return "", nil, err
	}
	if token != "" && (len(token) < 32 || len(token) > 256 || strings.ContainsAny(token, "\r\n\x00")) {
		return "", nil, errors.New("dashboard token must be 32–256 characters without control characters")
	}
	if len(cfg.Accounts) > 32 {
		return "", nil, errors.New("at most 32 accounts are supported")
	}
	seen := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" || len(a.Name) > 100 || seen[a.Name] {
			return "", nil, errors.New("account names must be unique and 1–100 bytes long")
		}
		seen[a.Name] = true
		a.APIKey, err = resolve(a.APIKey, a.APIKeyEnv)
		if err != nil {
			return "", nil, err
		}
		if _, err = credential(quotaRequest{Attributes: map[string]string{"base_url": "https://ollama.com/v1", "api_key": a.APIKey}}); err != nil {
			return "", nil, err
		}
		if a.APIKey == token {
			return "", nil, errors.New("dashboard token must differ from the Ollama API key")
		}
		a.APIKeyEnv = ""
	}
	return token, cfg.Accounts, nil
}
