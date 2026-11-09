# CPA configured-credential quota visibility

The reference CPA source is commit `681ce41b6e44c45b15b79378b003909d6ba29e68`
(v8 module, native ABI 1). Its quota RPC passes configured OpenAI-compatible
credentials' `attributes.api_key` and `attributes.base_url` to plugins, but its
credential listing excludes config-backed entries without an auth-file path.
The Quota Management UI loads that list, so a plugin alone cannot make these
credentials appear there.

`cpa-config-quota.patch` adds config-backed credentials to the existing list
**only when their provider has a registered quota plugin**. It does not expose
their API keys, change routing, or create duplicate auth files. It also handles
paginated listing. The change is limited to v8 listings; deprecated v0 listing
behavior remains unchanged. CPA's `AccountInfo()` can return an API key as an
account label, so the patch also suppresses that field for these entries.
Apply it to the reference CPA source before building:

```sh
git apply --check /path/to/CPA-ollama-usage-monitor-plugin/integration/cpa-config-quota.patch
git apply /path/to/CPA-ollama-usage-monitor-plugin/integration/cpa-config-quota.patch
go test ./internal/api/handlers/management -run 'TestConfiguredQuotaCredentialVisibility'
go build -o cli-proxy-api ./cmd/server
```

If your CPA already lists configured provider credentials with `supports_quota`
and `quota_plugin_id` in `/v8/management/credentials`, you do not need this patch.
Do not force-apply it to an incompatible version.

The current upstream management UI supports generic plugin quota cards and
`5h`/`weekly` buckets. Older management UIs without plugin quota support need an
update. This repository does not replace or inject code into the management UI.
