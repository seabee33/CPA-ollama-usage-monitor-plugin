// Package main implements a read-only CPA native quota provider.
package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

const pluginID = "ollama-usage-monitor"

var version = "0.1.0"

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
	ProviderNames []string `yaml:"provider_names"`
}
type plugin struct {
	mu        sync.RWMutex
	providers []string
	monitor   *monitor
}

func newPlugin() *plugin {
	return &plugin{providers: providerIDs([]string{"Ollama", "Ollama Cloud"}), monitor: newMonitor()}
}

var app = newPlugin()

func providerIDs(names []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		for _, id := range []string{name, "openai-compatible-" + strings.TrimPrefix(name, "openai-compatible-")} {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}
	return result
}

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
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid lifecycle request")
		}
		cfg := pluginConfig{ProviderNames: []string{"Ollama", "Ollama Cloud"}}
		if err := yaml.Unmarshal(req.ConfigYAML, &cfg); err != nil {
			return nil, errors.New("invalid plugin YAML configuration")
		}
		if len(cfg.ProviderNames) > 64 {
			return nil, errors.New("at most 64 provider names are supported")
		}
		ids := providerIDs(cfg.ProviderNames)
		if len(ids) == 0 {
			return nil, errors.New("provider_names must contain an Ollama provider name")
		}
		p.mu.Lock()
		p.providers = ids
		p.mu.Unlock()
		return map[string]any{
			"schema_version": 1,
			"metadata": map[string]any{
				"Name": pluginID, "Version": version, "Author": "seabee33",
				"GitHubRepository": "https://github.com/seabee33/CPA-ollama-usage-monitor-plugin",
				"ConfigFields":     []map[string]string{{"Name": "provider_names", "Type": "array", "Description": "Names of your existing Ollama OpenAI-compatible providers. Defaults: Ollama, Ollama Cloud."}},
			},
			"capabilities": map[string]bool{"quota_provider": true},
		}, nil
	case "quota.identifier":
		return map[string]string{"identifier": "openai-compatible-ollama"}, nil
	case "quota.describe":
		p.mu.RLock()
		ids := append([]string(nil), p.providers...)
		p.mu.RUnlock()
		return map[string]any{"supported_providers": ids, "display_name": "Ollama Cloud", "supports_reset": false}, nil
	case "quota.fetch":
		var req quotaRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid quota request")
		}
		key, err := credential(req)
		if err != nil {
			return nil, err
		}
		return p.monitor.fetch(key)
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
