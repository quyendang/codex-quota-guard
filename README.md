# Codex Quota Guard

`codex-quota-guard` is a CLIProxyAPI plugin for Codex account pools. It watches completed Codex requests, learns quota-window state from upstream `x-codex-*` headers, and disables credentials before they are picked again when a 5-hour or weekly quota window is exhausted or near exhaustion.

## Capabilities

- `usage_plugin`: records Codex quota headers and 429 `usage_limit_reached` failures.
- `scheduler`: keeps an in-memory guardrail for blocked Codex credentials.
- `management_api`: exposes an operations console at `/v0/resource/plugins/codex-quota-guard/status`.
- Host auth callbacks: uses `host.auth.list`, `host.auth.get`, and `host.auth.save` to toggle backing auth files.

When a quota threshold is reached, the plugin writes `disabled: true` to the backing auth JSON and adds a `codex_quota_guard` ownership marker. CLIProxyAPI core then excludes that auth the same way as Auth Files Management. After the reset time passes, the plugin only re-enables files that still carry its marker; auth files disabled manually outside the plugin are left disabled.

## Build

```bash
go test ./...
go build -buildmode=c-shared -o codex-quota-guard.dylib .
rm -f codex-quota-guard.h
```

Use the dynamic library extension for your platform:

- `.dylib` on macOS
- `.so` on Linux
- `.dll` on Windows

To create a Plugin Store asset for the current platform:

```bash
./scripts/package.sh 0.1.14
```

Artifacts are written to `dist/`.

## Configuration

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    codex-quota-guard:
      enabled: true
      priority: 100
      remaining-threshold-percent: 5
      fallback-429-ban: "5h"
      manual-block-duration: "1h"
```

Fields:

- `remaining-threshold-percent`: disable an auth file when a quota window has this percent or less remaining. Default: `5`.
- `fallback-429-ban`: disable duration when a Codex 429 usage limit has no reset headers. Default: `5h`.
- `manual-block-duration`: default control-panel manual block duration. Default: `1h`.

## Control Panel

Open:

```text
http://localhost:8080/v0/resource/plugins/codex-quota-guard/status
```

The panel shows usable, cooling, near-limit, manual-block, and auth-disabled credentials, plus backing auth file state, latest quota windows, host callback errors, and event trail. It refreshes read-only state through its resource URL with `?format=json`, so leaving it open does not create repeated failed Management API authentication attempts.

Manual actions call authenticated Management API endpoints and use the same auth-file disable marker:

- `POST /v0/management/codex-quota-guard/block`
- `POST /v0/management/codex-quota-guard/unblock`
- `POST /v0/management/codex-quota-guard/clear`

## Plugin Store Release

Create a tag such as `v0.1.14`. The GitHub Actions release workflow builds these assets:

```text
codex-quota-guard_0.1.14_darwin_amd64.zip
codex-quota-guard_0.1.14_darwin_arm64.zip
codex-quota-guard_0.1.14_linux_amd64.zip
codex-quota-guard_0.1.14_windows_amd64.zip
checksums.txt
```

Each zip contains one dynamic library at the archive root:

```text
codex-quota-guard.dylib
codex-quota-guard.so
codex-quota-guard.dll
```

Registry entry:

```json
{
  "id": "codex-quota-guard",
  "name": "Codex Quota Guard",
  "description": "Soft-blocks Codex credentials near 5-hour or weekly quota limits and returns them after reset.",
  "author": "quyen.eth",
  "version": "0.1.14",
  "repository": "https://github.com/quyendang/codex-quota-guard",
  "homepage": "https://github.com/quyendang/codex-quota-guard",
  "license": "MIT",
  "tags": ["codex", "quota", "scheduler", "usage", "management"]
}
```

## Notes

- The plugin is header-first and reconciles auth-file markers when usage, scheduler, or status requests reach the plugin.
- On 429 `usage_limit_reached`, reset hints from the error body win first; known Codex quota-window reset headers are used next; `fallback-429-ban` is only used when no reset hint is available.
- Credentials auto-enable lazily after `blocked_until` when the plugin observes activity or the status page refreshes.
- The scheduler guard remains as defense in depth, but real enforcement is the auth file `disabled` flag because CLIProxyAPI may fall back to built-in scheduling when a plugin declines a pick.
