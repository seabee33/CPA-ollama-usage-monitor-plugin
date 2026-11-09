package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

//go:embed web/*
var dashboardFiles embed.FS

const resourceBase = "/v0/resource/plugins/" + pluginID
const dashboardTokenHeader = "X-Ollama-Monitor-Token"

type managementRequest struct {
	Method  string              `json:"Method"`
	Path    string              `json:"Path"`
	Headers map[string][]string `json:"Headers"`
	Query   map[string][]string `json:"Query"`
}
type managementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

func managementRegistration() any {
	return map[string]any{"resources": []map[string]string{
		{"Path": "/dashboard", "Menu": "Ollama Usage", "Description": "Ollama session and weekly quota"},
		{"Path": "/app.js"}, {"Path": "/style.css"}, {"Path": "/data"},
	}}
}
func response(status int, contentType string, body []byte) managementResponse {
	return managementResponse{StatusCode: status, Body: body, Headers: map[string][]string{
		"Content-Type": {contentType}, "Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"},
		"Referrer-Policy": {"no-referrer"}, "X-Frame-Options": {"SAMEORIGIN"},
		"Content-Security-Policy": {"default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"},
	}}
}
func jsonResult(status int, value any) (managementResponse, error) {
	raw, err := json.Marshal(value)
	return response(status, "application/json; charset=utf-8", raw), err
}
func (p *plugin) management(raw []byte) (managementResponse, error) {
	var req managementRequest
	if json.Unmarshal(raw, &req) != nil {
		return jsonResult(400, map[string]string{"error": "Invalid request"})
	}
	if req.Method != http.MethodGet {
		return jsonResult(405, map[string]string{"error": "GET required"})
	}
	path := strings.TrimPrefix(req.Path, resourceBase)
	if req.Path != resourceBase+path {
		return jsonResult(404, map[string]string{"error": "Not found"})
	}
	static := map[string]struct{ file, kind string }{
		"/dashboard": {"web/index.html", "text/html; charset=utf-8"},
		"/app.js":    {"web/app.js", "application/javascript; charset=utf-8"},
		"/style.css": {"web/style.css", "text/css; charset=utf-8"},
	}
	if asset, ok := static[path]; ok {
		body, err := dashboardFiles.ReadFile(asset.file)
		return response(200, asset.kind, body), err
	}
	if path != "/data" {
		return jsonResult(404, map[string]string{"error": "Not found"})
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.configured {
		return jsonResult(503, map[string]string{"error": "Dashboard access is not configured. Set dashboard_token_env or dashboard_token in the plugin configuration."})
	}
	var tokens []string
	for name, values := range req.Headers {
		if strings.EqualFold(name, dashboardTokenHeader) {
			tokens = append(tokens, values...)
		}
	}
	if len(tokens) != 1 || len(tokens[0]) > 256 {
		return jsonResult(401, map[string]string{"error": "Dashboard access token required"})
	}
	hash := sha256.Sum256([]byte(tokens[0]))
	if subtle.ConstantTimeCompare(hash[:], p.tokenHash[:]) != 1 {
		return jsonResult(401, map[string]string{"error": "Invalid dashboard access token"})
	}
	if len(req.Query) == 0 {
		names := make([]string, 0, len(p.accounts))
		for _, a := range p.accounts {
			names = append(names, a.Name)
		}
		return jsonResult(200, map[string]any{"accounts": names})
	}
	names := req.Query["account"]
	if len(req.Query) != 1 || len(names) != 1 {
		return jsonResult(400, map[string]string{"error": "Expected one account parameter"})
	}
	for _, a := range p.accounts {
		if a.Name == names[0] {
			quota, err := p.monitor.fetch(a.APIKey)
			if err != nil {
				return jsonResult(502, map[string]string{"error": err.Error()})
			}
			return jsonResult(200, map[string]any{"name": a.Name, "quota": quota, "served_at": time.Now().UTC().Format(time.RFC3339), "cache_seconds": 60})
		}
	}
	return jsonResult(404, map[string]string{"error": "Account not found"})
}
