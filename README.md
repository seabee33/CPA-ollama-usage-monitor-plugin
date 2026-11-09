# CPA Ollama Usage Monitor

A small, read-only native **CLIProxyAPI quota plugin**. Shows **5-hour/session and
weekly quota remaining, with reset times, in CPA's existing Quota Management
page**, using the Ollama Cloud API key already configured in AI Providers →
OpenAI-compatible providers.

No browser cookies, separate dashboard, management-key storage, provider edits,
credential duplication, or HTML scraping. The plugin only declares the native
`quota_provider` capability. CPA authenticates quota requests and passes the
selected credential to the plugin server-side.

## Compatibility — read before installing

- Requires CPA native plugin ABI 1 with `quota_provider` support, plus a management
  UI that renders plugin quota cards. Reference backend: CPA v8 source commit
  `681ce41b6e44c45b15b79378b003909d6ba29e68`.
- **The reference backend hides config-backed OpenAI-compatible keys from the
  credential list used by Quota Management.** To make your existing provider show
  up, apply the small [CPA compatibility patch](integration/README.md) and rebuild
  CPA, unless your version already supports this. Installing the plugin alone on
  that unpatched version will not create a quota card.
- The current upstream Management Center supports `5h` and `weekly` plugin
  buckets. Look under **All / Plugin** in Quota Management; the exact tab label
  depends on your UI version. A dedicated Ollama tab is not injected.
- Ollama's `/api/balance` returns session/weekly limits for **legacy plans**.
  Credit-based plans show their actual included/purchased credits instead. The
  plugin never fabricates a 5-hour window from token counts.

## Install

1. Keep your existing OpenAI-compatible provider:

   ```yaml
   openai-compatibility:
     - name: "Ollama"
       base-url: "https://ollama.com/v1"
       api-key-entries:
         - api-key: "YOUR_EXISTING_OLLAMA_KEY"
       # Keep your existing models and other provider settings.
   ```

2. Build on the target OS/architecture with a current patched Go (1.26+) and C
   compiler. For Linux:

   ```sh
   mkdir -p build
   go build -trimpath -buildmode=c-shared -o build/ollama-usage-monitor.so .
   ```

   Copy the library to CPA's `plugins/linux/amd64/ollama-usage-monitor.so` (use
   `arm64` for ARM servers). macOS uses `.dylib`; Windows uses `.dll` with a C
   compiler. For containers, build against a compatible libc (glibc vs musl).

3. Merge this into CPA's existing configuration:

   ```yaml
   plugins:
     enabled: true
     dir: "plugins"
     configs:
       ollama-usage-monitor:
         enabled: true
         priority: 10
         provider_names: ["Ollama"]
   ```

   `provider_names` must match the **name** of your existing provider, not its model
   names. Matching is case-insensitive. Defaults are `Ollama` and `Ollama Cloud`.
   Custom names work, e.g. `["Personal Ollama", "Work Ollama"]`. Both the v8
   `openai-compatible-...` provider IDs and older unprefixed IDs are advertised.

4. Apply the compatibility patch if required, restart CPA, reload the management
   UI, open Quota Management, and refresh your Ollama card.

   A response with 75% remaining means **25% used**. The native UI controls whether
   the bar is labelled used or remaining. Both reset timestamps are supplied.

## Authentication and security

- CPA supplies `attributes.api_key` and `attributes.base_url` for the selected
  credential. No API key needs to be copied into plugin configuration.
- Only exact Ollama Cloud base URLs are accepted: `https://ollama.com/v1`,
  `https://ollama.com/api`, or `https://ollama.com`, with optional trailing slashes.
  Keys configured for local Ollama or another service are rejected.
- The only upstream request is `GET https://ollama.com/api/balance`, authenticated
  with `Authorization: Bearer <key>`. Redirects are never followed; TLS validation
  stays enabled. Standard Go HTTPS proxy environment settings are respected.
- No public resource routes, file writes, host credential-save callbacks, shell
  execution, or management API loopback requests are implemented.
- Responses are limited to 128 KiB, requests to 15 seconds, and cache entries to
  256. Fetches are serialized and cached for at least 60 seconds, including
  failures. `Retry-After` is honored up to 24 hours. Cached keys are SHA-256 hashes;
  plaintext keys are not stored in the cache. Upstream errors are sanitized.
- Limits are account-level, not per key or per CPA instance. Multiple keys for
  the same account show the same quota; caches are per key because Ollama supplies
  no account ID here. Avoid refreshing many keys for the same account: Ollama's
  limit is 10 requests/minute/user, shared across keys and devices.
- CPA management authentication must remain enabled. A native library runs with
  the CPA process's permissions; run CPA unprivileged and protect config/backups.
  An inference API key is not a read-only scoped credential merely because this
  plugin uses it read-only.

## Troubleshooting

**No card:** Confirm the plugin is loaded, `provider_names` matches, the provider
is enabled, the UI supports plugin quotas, and the compatibility patch is applied.
The authenticated `/v8/management/credentials` response should contain your
credential with `supports_quota: true` and
`quota_plugin_id: "ollama-usage-monitor"`.

**Check the backend directly:** Using your normal authenticated management client,
POST `/v8/management/plugins/ollama-usage-monitor/quota` with
`{"auth_index":"<the credential's auth_index>"}`. Do not put the Ollama key in
this request; CPA resolves it. This is also useful to distinguish UI issues from
plugin/API issues.

**401:** Update the existing key in AI Providers. Key rotation bypasses the old
key's cache automatically. **403:** Check Ollama account access. **404:** The API
is unavailable; the monitor fails visibly rather than falling back to cookies.
**429:** Wait for the cooldown. Repeated refreshes do not bypass it.

## Development and verification

```sh
go test -race ./...
go vet ./...
go build -trimpath -buildmode=c-shared -o build/ollama-usage-monitor.so .
python3 scripts/abi_smoke.py build/ollama-usage-monitor.so
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Tests use synthetic keys and mocked Ollama responses, covering quota conversion,
invalid schemas, credential boundaries, concurrency/cache behavior, rate limiting,
redirect rejection, secret redaction, ABI loading, and read-only capabilities.
No real Ollama credentials are required. A real-account/live-dashboard test is
still needed in your deployment; source compatibility does not establish your
installed CPA/UI versions or account plan.

The patched reference CPA was also built and tested with synthetic credentials:

```sh
python3 scripts/cpa_smoke.py /path/to/patched/cli-proxy-api build/ollama-usage-monitor.so
```

This Linux amd64 integration check starts an isolated loopback CPA process and
checks plugin discovery, configured credential visibility, secret redaction,
authenticated quota dispatch, and rejection of non-Ollama credentials. It never
sends the synthetic keys to Ollama.

GitHub Actions tests and builds Linux amd64/arm64 libraries. Tagging `v*` publishes
archives and SHA-256 checksums. Build locally for other platforms.

## Scope and API reference

This first version deliberately focuses on the requested quota windows. It uses
the official [Balance API](https://docs.ollama.com/api/balance), not the separate
[Usage history API](https://docs.ollama.com/api/cloud-usage). Token-history charts
are not required to report session/weekly quota correctly.

This is a fresh implementation using CPA's public ABI contract and Ollama's JSON
API. It does not incorporate the original monitor's HTML parser or provider-edit
code. Licensed under MIT.
