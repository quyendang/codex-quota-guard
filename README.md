# Codex Quota Guard

`codex-quota-guard` is a CLIProxyAPI plugin for Codex account pools. It watches completed Codex requests, learns quota-window state from upstream `x-codex-*` headers, and soft-blocks credentials before they are picked again when a 5-hour or weekly quota window is exhausted or near exhaustion.

## Capabilities

- `usage_plugin`: records Codex quota headers and 429 `usage_limit_reached` failures.
- `scheduler`: skips soft-blocked Codex credentials until their reset time has passed.
- `management_api`: exposes an operations console at `/v0/resource/plugins/codex-quota-guard/status`.

The plugin does not write `disabled: true` to auth files. Automatic blocks are kept in plugin memory.

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
./scripts/package.sh 0.1.3
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

- `remaining-threshold-percent`: soft-block when a quota window has this percent or less remaining. Default: `5`.
- `fallback-429-ban`: soft-block duration when a Codex 429 usage limit has no reset headers. Default: `5h`.
- `manual-block-duration`: default control-panel manual block duration. Default: `1h`.

## Control Panel

Open:

```text
http://localhost:8080/v0/resource/plugins/codex-quota-guard/status
```

The panel shows usable, cooling, near-limit, and manual-block credentials, plus the latest quota windows and event trail. It refreshes read-only state through its resource URL with `?format=json`, so leaving it open does not create repeated failed Management API authentication attempts.

Manual actions call authenticated Management API endpoints and only mutate plugin state:

- `POST /v0/management/codex-quota-guard/block`
- `POST /v0/management/codex-quota-guard/unblock`
- `POST /v0/management/codex-quota-guard/clear`

## Plugin Store Release

Create a tag such as `v0.1.3`. The GitHub Actions release workflow builds these assets:

```text
codex-quota-guard_0.1.3_darwin_amd64.zip
codex-quota-guard_0.1.3_darwin_arm64.zip
codex-quota-guard_0.1.3_linux_amd64.zip
codex-quota-guard_0.1.3_windows_amd64.zip
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
  "version": "0.1.3",
  "repository": "https://github.com/quyendang/codex-quota-guard",
  "homepage": "https://github.com/quyendang/codex-quota-guard",
  "license": "MIT",
  "tags": ["codex", "quota", "scheduler", "usage", "management"]
}
```

## Notes

- The plugin is header-first and does not run background quota checks.
- On 429 `usage_limit_reached`, reset hints from the error body win first; known Codex quota-window reset headers are used next; `fallback-429-ban` is only used when no reset hint is available.
- Credentials auto-unblock lazily on future scheduler calls after `blocked_until`.
- If all Codex candidates are blocked, the plugin declines the pick so CPA core can return its normal unavailable or cooldown response.
