# CLAUDE.md: Nycto Wings

## Project context

- **Nycto Wings** is the node daemon for **Nycto**, a new open-source game/app
  server hosting panel.
- This repo is a fork of [pterodactyl/wings](https://github.com/pterodactyl/wings)
  (MIT). The repo owner maintains it. A partner builds the Nycto panel from
  scratch in a separate repo (Go backend, embedded frontend, SQLite/Postgres,
  AGPL-3.0).
- This daemon stays **MIT**. The original copyright, `LICENSE` and upstream
  attribution must stay intact.
- **For now, the daemon must stay fully compatible with the existing Wings
  protocol**, so it works with the stock Pterodactyl panel *and* the new Nycto
  panel. Any divergence comes later, on purpose, and only with explicit approval.
- `protocol/WINGS_API.md` is the reference for the panel ⇄ daemon contract.
  Update it in the same change whenever protocol behaviour changes.

## Repo layout

| Path | What lives there |
|---|---|
| `wings.go` | `main`. Calls `cmd.Execute()` |
| `cmd/` | Cobra CLI: `root.go` (daemon boot: `rootCmdRun`), `configure.go` (`wings configure`, fetches node config from the panel), `diagnostics.go` |
| `config/` | YAML config singleton (`config.Get/Set/Update`), defaults, token resolution, JWT key, Docker config (`config_docker.go`) |
| `remote/` | HTTP client for the panel's `/api/remote/*` API (`remote.Client`) |
| `router/` | Gin HTTP API that Wings serves. `router.go:Configure` registers every route |
| `router/middleware/` | Node-token auth, CORS, request IDs, error formatting |
| `router/tokens/` | JWT payload types, scope checks, one-time-use store, denylist |
| `router/websocket/` | Console websocket protocol, permissions, rate limits |
| `router/downloader/` | Remote URL → server file downloads (SSRF-guarded) |
| `server/` | Per-server state machine: `Manager`, `Server`, power, install, backup, crash detection, events, websocket/SFTP session bags |
| `server/filesystem/` | Per-server sandboxed filesystem: path resolution, denylist, quota, archive/compress/decompress, transfer extraction |
| `server/backup/` | Local (`wings`) and S3 backup adapters |
| `server/transfer/` | Node-to-node server transfer (source side, archive streaming) |
| `server/installer/` | Create-server bootstrap (fetches config from the panel) |
| `environment/` | Environment abstraction plus Docker network setup (`docker.go`) |
| `environment/docker/` | Docker implementation: container create/attach/power/stats/cgroup burst |
| `sftp/` | Built-in SSH/SFTP server, panel-backed auth, per-op permissions |
| `internal/ufs/` | Low-level "unix filesystem" with openat/openat2-based path containment (has its own BSD `LICENSE`/`go.LICENSE`) |
| `internal/network/` | Internal-IP detection, allowlists, connection-limiting listener |
| `internal/database/`, `internal/models/` | SQLite (gorm) activity store, `<root>/wings.db` |
| `internal/cron/` | Activity / SFTP-event delivery to the panel |
| `parser/` | Egg config-file parsers (yaml/json/ini/xml/properties/file) |
| `events/`, `system/` | Event bus, sinks, locks, atomics, version (`system.Version`) |
| `loggers/cli/` | apex/log CLI handler |
| `protocol/` | Protocol documentation for the fork |

### Startup flow (`cmd/root.go`)

1. `initConfig`: `config.FromFile`, which runs `ResolveToken(false)` and `Set` (builds the HS256 JWT key).
2. `initLogging`.
3. Timezone, directories, system user, passwd files, logrotate.
4. `remote.New(...)`.
5. `database.Initialize`.
6. `server.NewManager`: `GET /api/remote/servers` → `InitServer` for each server.
7. `environment.ConfigureDocker`: creates the network if missing.
8. Write the config to disk.
9. Restore server states (4 workers).
10. Cron.
11. SFTP server.
12. `POST /servers/reset`.
13. HTTP/TLS listener (`router.Configure`).
14. `SIGINT`/`SIGTERM`: persist states and shut down. Containers keep running.

## Build

- **Go 1.26** is required (`go.mod`: `go 1.26.0`, `toolchain go1.26.9`).
  Go is **not installed** on the Windows dev machine yet. Install it first.
- **Wings is Linux-only.** It uses `golang.org/x/sys/unix` (openat2),
  `syscall.Getrlimit` and `//go:build unix` files, so a native Windows build
  **fails**. Always set `GOOS=linux`.

PowerShell (Windows dev machine):

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -trimpath -ldflags="-s -w -X github.com/pterodactyl/wings/system.Version=dev" -o build/wings_linux_amd64 .
$env:GOARCH="arm64"; go build -trimpath -ldflags="-s -w" -o build/wings_linux_arm64 .
go vet ./...      # with GOOS=linux still set; type-checks Linux-only files and tests
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

Bash / Linux / WSL: `make build` (amd64 + arm64 into `build/`), or

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/pterodactyl/wings/system.Version=dev" -o build/wings_linux_amd64 .
```

Lint (matches CI): `GOOS=linux golangci-lint run ./...` (config in `.golangci.yml`; `gosec` enabled).

Docker image: `Dockerfile` (multi-stage, distroless; `--build-arg VERSION=`).

## Tests

- Tests need **no Docker and no root**. Docker is faked with `httptest`
  (e.g. `server/install_container_test.go`). One test skips itself without root
  (`server/mounts_test.go`).
- They must **run on Linux**. On Windows, `GOOS=linux go test` builds Linux test
  binaries it can't execute. Locally, `GOOS=linux go vet ./...` is the
  compile check. Run the real tests in WSL (it's installed) or on the VPS.
- Commands (from CI, `.github/workflows/push.yaml`):

  ```sh
  go test ./...
  go test -race ./...
  go test ./router/...                # one package tree
  go test ./sftp -run TestName -v     # one test
  ```

- Frameworks in use: the standard `testing` package with `testify` (`require`/`assert`), plus
  some older `franela/goblin` suites. Match the file you're editing.
- Never assume Docker or root are available locally. Only build and run unit tests locally.

## Dev workflow

1. Edit locally (Windows). Run the `GOOS=linux` build and `go vet`, and run unit tests in WSL if possible.
2. Commit and push to GitHub (`origin` = `github.com/nycto-sh/wings`, main branch `develop`).
   Only commit or push when the owner asks.
3. On the Linux VPS (Docker + root available): pull, build, run tests, then run
   the daemon against a panel (`wings --debug --config <path>`) for integration testing.
4. Report what was verified locally versus what still needs VPS verification.

## Coding conventions observed

- `gofmt` formatting with tabs (`.editorconfig`). Keep `.go` lines to about 100 columns.
- Errors: `emperror.dev/errors` (`errors.WithStack`, `WrapIf`, `WithStackIf`).
  Some files use the stdlib `errors` package. Follow the file you're in.
- Logging: `github.com/apex/log` with structured fields
  (`log.WithField(...)`, `s.Log()`, `middleware.ExtractLogger(c)`). Never log tokens.
  `remote/http.go:debugLogRequest` redacts `Authorization`, and the router
  log line masks `token=`.
- HTTP handlers: `gin`. Read context values with the
  `middleware.Extract*` helpers. Report errors with `middleware.CaptureAndAbort(c, err)`.
  Send expected client errors as `c.AbortWithStatusJSON(code, gin.H{"error": "..."})`.
  Long-running work goes in a goroutine and returns `202 Accepted`.
- Config: read through `config.Get()`, which returns a copy. Mutate only through `config.Update(fn)` / `config.Set`.
- Concurrency: guard state with mutexes/atomics. The `system.Atomic*` types and `system.Locker` exist for this.
- Comments explain *why*. Keep the existing comment density and references (`@see` issue links).
- `nolint` directives must state a reason (`//nolint:gosec // reason`).
- Every file operation on server data goes through `server/filesystem` / `internal/ufs`,
  never raw `os.*` on user-controlled paths. (Backups use `os` because they
  live outside server-accessible directories.)

## Rules (must follow)

1. **Never weaken** path sanitization, symlink handling, archive extraction
   checks or token validation. That covers `server/filesystem/*`, `internal/ufs/*`,
   `router/tokens/*`, `router/middleware/RequireAuthorization`,
   `config.ResolveToken`, the SFTP `can()` checks, the SSRF guards in
   `router/downloader` and `router_server_backup.go`, and the websocket JWT re-validation.
2. **Flag for extra review** any change that touches file operations, SFTP, auth
   (node token, JWTs, websocket auth, denylist) or container configuration
   (`environment/docker/*`, mounts, network, users, limits). Say so clearly in
   the summary and the commit/PR description.
3. **Keep protocol compatibility.** Routes, methods, JSON field names, headers
   (including `Accept: application/vnd.pterodactyl.v1+json` and the
   `User-Agent`), websocket event names, JWT claims/scopes and `/api/remote/*`
   calls all stay as they are unless the owner explicitly approves a breaking
   change. Update `protocol/WINGS_API.md` with any protocol-visible change.
4. **Keep the upstream `LICENSE` and attribution intact.** That includes the
   "Dane Everitt & Contributors" copyright notices and `internal/ufs/LICENSE` / `go.LICENSE`.
   Add Nycto attribution alongside, never in place of, the upstream notices.
5. Don't rename upstream-specific identifiers (module path, binary, paths,
   Docker names) until the owner approves the rename plan.
