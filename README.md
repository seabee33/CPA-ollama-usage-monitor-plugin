# CPA Ollama Usage Monitor

A read-only **Ollama Usage sidebar page for stock CLIProxyAPI**. Displays
**5-hour/session and weekly usage**, remaining quota, and reset times using
Ollama's official API. No CPA fork or backend patch is required.

Credit-based Ollama plans show included/purchased balances instead. Session and
weekly windows are returned by Ollama for legacy plans; the monitor never
estimates these limits from tokens or manufactures unsupported windows.

## Install and configure

Requires CPA's native plugin ABI 1 and Management API resource capability, plus a
management UI with plugin sidebar support. Tested against unmodified CPA source
commit `681ce41b6e44c45b15b79378b003909d6ba29e68` (v8 module).

1. Build using a current patched Go (1.26+) and C compiler:

   ```sh
   mkdir -p build
   go build -trimpath -buildmode=c-shared -o build/ollama-usage-monitor.so .
   ```

   Copy to CPA's `plugins/linux/amd64/ollama-usage-monitor.so` (use `arm64` for
   ARM servers). Build against a compatible libc for your container/server.
   macOS uses `.dylib`; Windows uses `.dll` and needs a C compiler.

2. Generate a separate dashboard access token:

   ```sh
   openssl rand -hex 32
   ```

3. Set these environment variables **in the CPA service/container environment**:

   ```text
   OLLAMA_MONITOR_TOKEN=<the generated dashboard token>
   OLLAMA_MONITOR_API_KEY=<your Ollama Cloud API key>
   ```

   The Ollama key can be the same key you already use in AI Providers. This plugin
   configures its own server-side reference; it does not automatically retrieve
   keys from OpenAI-compatible providers. You can use the same environment
   variable wherever your deployment supports secret references.

4. Merge this into CPA's existing config:

   ```yaml
   plugins:
     enabled: true
     dir: "plugins"
     configs:
       ollama-usage-monitor:
         enabled: true
         priority: 10
         dashboard_token_env: "OLLAMA_MONITOR_TOKEN"
         accounts:
           - name: "Personal"
             api_key_env: "OLLAMA_MONITOR_API_KEY"
   ```

   Alternatively, use `dashboard_token` and `api_key` with literal values in the
   private config. Do not specify both the literal and environment form for a
   secret. Protect config files, environment files, and backups (e.g. mode 0600).
   Each account needs a unique name; up to 32 accounts are supported.

5. Restart CPA and open **Ollama Usage** in its sidebar. Enter your **dashboard
   access token**, not your Ollama key or CPA management key. Select Refresh all
   to update quota. Use **Lock dashboard** when finished.

The page can also be opened directly at:
`/v0/resource/plugins/ollama-usage-monitor/dashboard` on your CPA server.
Use HTTPS, a VPN with appropriate transport protection, or an SSH tunnel when
sending the dashboard token. The page intentionally uses a separate access token
because CPA's plugin resource routes are not management-authenticated.

## What the dashboard shows

- **5-hour usage** and **weekly usage** with percentage-used bars and local reset
  dates. A provider response of 75% remaining is displayed as 25% used.
- Included and purchased credits where returned by Ollama.
- Independent errors per account, so one expired key does not hide other cards.
- Explicit cached-result messaging. Refresh is manual; successful and failed
  fetches are cached for at least 60 seconds.

Quota is account-wide, including requests made outside CPA. Multiple API keys
for the same Ollama account will show the same limits. Configure one monitoring
entry per Ollama account where possible.

## Security model

- The public dashboard shell, JavaScript, and CSS contain no account data or
  credentials. The `/data` route validates the dashboard token **on every
  request**, before listing accounts or fetching usage. Missing token configuration
  disables data access. Invalid reloads invalidate existing dashboard access.
- The token is sent only in `X-Ollama-Monitor-Token`, never in a URL or cookie.
  It lives only in page memory; it is not saved to local/session storage. Locking
  or navigating away clears it. Dashboard data responses use `Cache-Control:
  no-store`. Configure proxies not to log the custom token header.
- All holders of the dashboard token can view all configured accounts. It is a
  shared read-only access token, not per-user authentication. Use a strong random
  value (32–256 characters), distinct from the API keys and CPA management key.
- Account names and API responses are rendered as text, not injected as HTML.
  A restrictive CSP blocks inline scripts, third-party content, and foreign
  framing while permitting same-origin CPA sidebar embedding.
- Only `GET https://ollama.com/api/balance` is called. TLS verification is enabled,
  redirects are rejected, bodies are capped at 128 KiB, and calls have a 15-second
  timeout. Standard Go HTTPS proxy environment settings are respected.
- Fetches are serialized, cached per hashed API key, and limited to 256 live cache
  entries. `Retry-After` is honored up to 24 hours. Ollama's per-user limit is 10
  requests/minute shared across keys/devices; avoid redundant account entries.
- The plugin performs no provider edits, key cleanup, config writes, shell
  execution, or loopback management calls. It stores the configured API keys in
  process memory to make requests; it never sends them to the browser.
- A native plugin has the permissions of the CPA process. Run CPA unprivileged.
  The Ollama API key itself is not read-only scoped just because this monitor is.

## Troubleshooting

**Sidebar missing:** Check plugin loading, the exact library basename
`ollama-usage-monitor`, enabled configuration, and management UI plugin-sidebar
support. Reload the UI. Try the direct dashboard URL above.

**Access not configured:** Set `dashboard_token_env` or `dashboard_token` and
reload/restart CPA. Environment variables must belong to the CPA process; setting
them in an unrelated terminal will not update a running service.

**401 from dashboard:** Enter the separate dashboard access token. **Ollama 401:**
Update the account API key in server configuration. **429:** Wait for the cached
cooldown. **404:** The official balance endpoint is unavailable; the monitor does
not fall back to browser cookies. Credit-based plans do not have legacy 5-hour
or weekly buckets.

## Tests and builds

```sh
go test -race ./...
go vet ./...
go build -trimpath -buildmode=c-shared -o build/ollama-usage-monitor.so .
python3 scripts/abi_smoke.py build/ollama-usage-monitor.so
python3 scripts/cpa_smoke.py /path/to/stock/cli-proxy-api build/ollama-usage-monitor.so
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Tests use synthetic credentials. Unit tests cover balance conversion, auth,
configuration, cache concurrency, rate-limit handling, redirects, and redaction.
The native ABI and stock-CPA integration tests verify real library loading and
protected resource dispatch without making requests to Ollama. A real-account
test is still needed for your deployment and account plan.

`scripts/dashboard_smoke.cjs` additionally exercises the page in headless Chromium
with fixture responses: unlocking, percentage conversion, safe text rendering,
mobile layout, memory-only token handling, and locking. Run it with Playwright
installed and available via `NODE_PATH`; its browser must be installed separately.

GitHub Actions builds Linux amd64 and arm64. Tagged releases use the official
store format, e.g. `ollama-usage-monitor_0.2.0_linux_amd64.zip`, containing the
library at the ZIP root, plus `checksums.txt`. Store listing requires a separate
registry submission; this repository is not automatically listed.

## Migration from the initial prototype

Version 0.2 replaces the native quota-card prototype with this self-contained
sidebar plugin. It does **not** require the earlier CPA visibility patch, and
does not place a card in the built-in Quota Management page. Remove obsolete
`provider_names`, configure `accounts` and a dashboard token, and use stock CPA.
The old patch is no longer distributed or maintained.

## References and license

- [Ollama balance API](https://docs.ollama.com/api/balance)
- [Ollama authentication](https://docs.ollama.com/api/authentication)
- [CPA plugin store requirements](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)

Fresh implementation under MIT; no HTML-scraping or provider-editing code from
the original monitor is incorporated.
