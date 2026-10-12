# Wings ⇄ Panel protocol map

This file maps the contract between the daemon in this repo and a panel, as the
**current source tree** implements it (branch `develop`, at commit `5d9e2c3`).
It covers what Wings calls on the panel, what Wings serves, the console
websocket, the JWTs, node authentication and SFTP authentication.

Conventions:

- Paths are relative to the repo root. `file.go:func` names the function to read.
- **UNVERIFIED** marks anything this repo can't prove. That's usually panel-side
  behaviour (what the panel sends, how it signs things), or library behaviour
  that wasn't checked in the vendored source.
- "Stock panel" means the upstream Pterodactyl Panel. This tree includes
  post-fork hardening commits. Where a check could reject an older stock panel,
  this file says so.

---

## 0. Transport basics

| Item | Value | Source |
|---|---|---|
| Wings HTTP listen | `api.host:api.port` (defaults `0.0.0.0:8080`) | `config/config.go:ApiConfiguration`, `cmd/root.go:rootCmdRun` |
| TLS | `api.ssl.enabled` + `cert`/`key`, or `--auto-tls --tls-hostname` (Let's Encrypt, cache in `<root>/.tls-cache`) | `cmd/root.go:rootCmdRun` |
| TLS settings | TLS 1.2–1.3, ECDHE AEAD suites, h2 + http/1.1 | `config/config.go:DefaultTLSConfig` |
| SFTP listen | `system.sftp.bind_address:bind_port` (defaults `0.0.0.0:2022`) | `config/config.go:SftpConfiguration`, `sftp/server.go:New` |
| Unauthenticated request deadline | 30 s to read the request and write the response, cleared once authenticated | `router/router.go:limitUnauthenticated`, `router/middleware/middleware.go:ClearDeadlines` |
| Header timeout / idle | 10 s / 2 min | `router/router.go:NewServer` |
| Connection caps | HTTP and SFTP caps come from `RLIMIT_NOFILE` (3/4 HTTP, 1/4 SFTP of the available descriptors) | `cmd/root.go:connectionLimits`, `internal/network/limit_listener.go` |
| CORS | `Access-Control-Allow-Origin` = panel URL, or a matching entry of `allowed_origins` (`*` allowed). `OPTIONS` → 204 | `router/middleware/middleware.go:SetAccessControlHeaders` |
| Request ID | Every response has `X-Request-Id: <uuid4>` | `router/middleware/middleware.go:AttachRequestID` |
| Error body shape | `{"error": "<message>", "request_id": "<uuid>"}` (some handlers send only `{"error": ...}`) | `router/middleware/request_error.go:RequestError.Abort`, `asFilesystemError` |

---

## 1. Node authentication (token ID / token)

### 1.1 Config fields

`config/config.go:Configuration`:

| YAML key | Go field | Meaning |
|---|---|---|
| `uuid` | `Uuid` | Node UUID on the panel |
| `token_id` | `AuthenticationTokenId` | Public identifier of the node's daemon token |
| `token` | `AuthenticationToken` | Secret daemon token |
| `remote` | `PanelLocation` | Panel base URL |

`Configuration.Token` (`ID`, `Token`) is a **derived** field. It's never
serialized (`json:"-" yaml:"-"`). `Configuration.ResolveToken(remote bool)`
fills it in:

- **Local load** (`config.FromFile` → `ResolveToken(false)`). Env vars
  `WINGS_TOKEN_ID` / `WINGS_TOKEN` take priority over the YAML values. Values
  are passed through `config.Expand`, so `$VAR` is expanded, then a `file://path`
  value is read from disk (meant for systemd `LoadCredential`).
- **Panel-supplied values** (`POST /api/update`, `wings configure`) →
  `ResolveToken(true)`. A token containing `$` or starting with `file://` is
  rejected. If an env override is set, the panel's value must equal it or the
  update is rejected.

### 1.2 Panel → Wings requests

`router/middleware/middleware.go:RequireAuthorization`:

- Header: `Authorization: Bearer <token>`. Only the **secret token** is sent,
  with no token ID.
- Compared in constant time with `config.Get().Token.Token`.
- Missing or malformed header → `401` + `WWW-Authenticate: Bearer`. Wrong
  token → `403`.
- The token is re-read on every request, so rotation through `/api/update`
  takes effect immediately.

### 1.3 Wings → Panel requests

`remote/http.go:client.requestOnce`:

```
Authorization: Bearer <token_id>.<token>
Accept:        application/vnd.pterodactyl.v1+json
Content-Type:  application/json
User-Agent:    Pterodactyl Wings/v<system.Version> (id:<token_id>)
```

- Base URL = `<remote>/api/remote` (`remote/http.go:New`).
- HTTP client timeout = `remote_query.timeout` seconds, default 30
  (`cmd/root.go:rootCmdRun`). `remote/http.go:New` has a 15 s default, but
  the boot code overrides it.
- Retries use exponential backoff (max interval 12 s, max elapsed 30 s).
  `4xx` responses and context cancellation are not retried; `5xx` and network
  errors are (`remote/http.go:client.request`, `client.backoff`).
- Error responses are parsed as `{"errors":[{"code","status","detail"}]}`
  (`remote/errors.go:RequestErrors`).
- Credentials rotate at runtime through `Client.SetCredentials`, which
  `router/router_system.go:postUpdateConfiguration` calls.

### 1.4 JWT signing key

The **same secret token** is the HMAC key for every JWT Wings accepts:
`config/config.go:Set` → `jwt.NewHS256([]byte(token))`, read through
`config.GetJwtAlgorithm()`. See §5.

### 1.5 Bootstrap: `wings configure`

`cmd/configure.go`. This is not part of the runtime protocol, but it does call
the panel:

- `GET <panel-url>/api/application/nodes/<node-id>/configuration`
- Headers: `Authorization: Bearer <application API key>`,
  `Accept: application/vnd.pterodactyl.v1+json`
- Response: a JSON node configuration, written to the config path through
  `cmd/configure.go:writePanelConfiguration`. `PanelLocation` is set from
  `--panel-url`, and the token passes `ResolveToken(true)`.
- `401`/`403` → "invalid token" message. Any status other than 200 → failure.

---

## 2. Endpoints Wings CALLS on the panel (`/api/remote/*`)

Everything below goes through `remote.Client` (`remote/http.go`,
`remote/servers.go`). Paths are relative to `<remote>/api/remote`. Request and
response types are in `remote/types.go`.

| # | Method & path | Client function | Request | Response used by Wings | Called when (caller) |
|---|---|---|---|---|---|
| 1 | `GET /servers?page=<n>&per_page=<limit>` | `GetServers` / `getServersPaged` | query only. The first call uses `page=0`; any further pages (`current_page+1 … last_page`) are fetched in parallel | `{"data":[RawServerData], "meta": Pagination}` | Boot, loading every server on the node: `server/manager.go:Manager.init`. `per_page` = `remote_query.boot_servers_per_page` (default 50) |
| 2 | `POST /servers/reset` | `ResetServersState` | body `null` | ignored | Once after boot, in the background, so servers stuck in installing/restoring get reset: `cmd/root.go:rootCmdRun` |
| 3 | `GET /servers/{uuid}` | `GetServerConfiguration` | – | `ServerConfigurationResponse` `{"settings": {...}, "process_configuration": {...}}` | `server/server.go:Server.Sync` (every start via `server/power.go:onBeforeStart`, reinstall, `POST /api/servers/:server/sync`, install, boot re-sync of running servers); also `server/installer/installer.go:New` (server create, and the incoming transfer on the destination node). A `404` makes `Sync` return `serverDoesNotExist` |
| 4 | `GET /servers/{uuid}/install` | `GetInstallationScript` | – | `{"container_image","entrypoint","script"}` | `server/install.go:Server.internalInstall` (install and reinstall, unless `skip_egg_scripts`) |
| 5 | `POST /servers/{uuid}/install` | `SetInstallationStatus` | `{"successful": bool, "reinstall": bool}` | ignored | After install/reinstall finishes, including the skipped-script case: `server/install.go:Server.SyncInstallState` |
| 6 | `POST /servers/{uuid}/transfer/success` or `/transfer/failure` | `SetTransferStatus` | body `null` | ignored | Source node on failure: `router/router_server_transfer.go:postServerTransfer` (`notifyPanelOfFailure`). Destination node on completion or failure: `router/router_transfer.go:completeIncomingTransfer`, and on installer failure in `postTransfers`. Only the **destination** reports success |
| 7 | `POST /servers/{uuid}/archive` | `SetArchiveStatus` | `{"successful": bool}` | ignored | **Nothing calls it** in the current tree. It's still in the `remote.Client` interface |
| 8 | `POST /sftp/auth` | `ValidateSftpCredentials` | `SftpAuthRequest`: `{"type":"password"\|"public_key","username","password","ip","session_id","client_version"}`. `session_id` and `client_version` are `[]byte`, so they're JSON **base64 strings**. With public key auth, `password` holds the key in `authorized_keys` format | `{"server": "<uuid>", "user": "<user uuid>", "permissions": ["..."]}` | On every SSH password or public-key attempt: `sftp/server.go:SFTPServer.makeCredentialsRequest`. Any `4xx` → invalid credentials, not retried |
| 9 | `GET /backups/{backup}?size=<bytes>` | `GetBackupRemoteUploadURLs` | query `size` | `{"parts": ["<presigned PUT url>", ...], "part_size": int}` | S3 backup upload: `server/backup/backup_s3.go:S3Backup.generateRemoteRequest` |
| 10 | `POST /backups/{backup}` | `SetBackupStatus` | `BackupRequest`: `{"checksum","checksum_type":"sha1","size","successful","parts":[{"etag","part_number"}]}` | ignored | After a backup succeeds or fails: `server/backup.go:Server.notifyPanelOfBackup`. If reporting a success fails, the local archive is deleted |
| 11 | `POST /backups/{backup}/restore` | `SendRestorationStatus` | `{"successful": bool}` | ignored | Deferred at the end of `server/backup.go:Server.RestoreBackup` |
| 12 | `POST /activity` | `SendActivityLogs` | `{"data": [Activity]}`. `Activity` = `{"user": "<uuid>"\|null, "server": "<uuid>", "event": "<string>", "metadata": {...}, "ip": "<ip>", "timestamp": RFC3339}` (`internal/models/activity.go`) | ignored | Cron every `system.activity_send_interval` seconds (default 60), in batches of `activity_send_count` (default 100): `internal/cron/cron.go:Scheduler` → `activity_cron.go`, `sftp_cron.go` → `delivery.go:deliver`. A `400`/`413`/`422` batch is bisected and the refused items dropped |

### 2.1 `settings` payload (server configuration)

Unmarshalled into `server/configuration.go:configurationData` by
`server/server.go:SyncWithConfiguration`:

```jsonc
{
  "uuid": "<uuid>",                 // must be a valid UUID and can never change for a server
  "meta": {"name": "", "description": ""},
  "suspended": false,
  "invocation": "<startup command>",
  "skip_egg_scripts": false,
  "environment": {"KEY": "value"},  // environment.Variables, values may be non-strings
  "labels": {"k": "v"},             // extra container labels
  "allocations": {                  // environment/allocations.go:Allocations
    "force_outgoing_ip": false,
    "default": {"ip": "", "port": 0},
    "mappings": {"<ip>": [port, ...]}
  },
  "build": {                        // environment/settings.go:Limits
    "memory_limit": 0, "swap": 0, "io_weight": 0, "cpu_limit": 0,
    "disk_space": 0, "threads": "", "oom_disabled": false
  },
  "crash_detection_enabled": true,  // defaults to the node's system.crash_detection.enabled
  "mounts": [{"target": "", "source": "", "read_only": false}],
  "egg": {"id": "", "file_denylist": ["..."]},
  "container": {"image": "..."}
}
```

`process_configuration` (`remote/types.go:ProcessConfiguration`):

```jsonc
{
  "startup": {"done": ["text" | "regex:<expr>"], "user_interaction": [], "strip_ansi": false},
  "stop": {"type": "command" | "signal" | "stop", "value": "..."},
  "configs": [ /* parser.ConfigurationFile, see parser/ */ ]
}
```

Wings rejects a configuration whose `uuid` doesn't match the requested
server (`server/manager.go:InitServer`, `server/server.go:SyncWithConfiguration`).

### 2.2 Non-panel outbound calls (FYI)

- S3 multipart part upload: `PUT <presigned url>` with `Content-Type: application/x-gzip`. The ETag is taken from the response (`server/backup/backup_s3.go:uploadPart`).
- S3 restore download: `GET download_url`. Internal/private destinations are blocked unless `system.backups.restore_host_allowlist` allows them (`router/router_server_backup.go:backupRestoreHttpClient`).
- Remote file pull: user-supplied URL (`router/downloader/`). Its `User-Agent` is `Pterodactyl Panel (https://pterodactyl.io)`.
- Node-to-node transfer: `POST <url>` (§3.6).
- `wings diagnostics`: uploads the report to a hastebin (default `https://ptero.co`) when the user agrees (`cmd/diagnostics.go:uploadToHastebin`).

---

## 3. Endpoints Wings SERVES

All routes are registered in `router/router.go:Configure`. Auth legend:

- **NODE**: `Authorization: Bearer <node token>` (`middleware.RequireAuthorization`, §1.2)
- **JWT-q**: JWT in the `?token=` query parameter, see §5
- **JWT-h**: JWT in `Authorization: Bearer <jwt>`
- **WS**: no HTTP auth; the JWT is sent inside the websocket (§4)
- `+SE`: `middleware.ServerExists()`. Unknown `:server` → `404 {"error":"The requested resource does not exist on this instance."}`

### 3.1 Signed-URL routes (no node token)

| Method & path | Auth | Handler | Purpose |
|---|---|---|---|
| `GET /download/backup?token=` | JWT-q, `BackupPayload`, scope `backup-download` | `router/router_download.go:getDownloadBackup` | Stream a local backup `<backup_dir>/<backup_uuid>.tar.gz` |
| `GET /download/file?token=` | JWT-q, `FilePayload`, scope `file-download` | `router/router_download.go:getDownloadFile` | Stream one server file (`file_path` claim) as an attachment |
| `POST /upload/file?token=&directory=` | JWT-q, `UploadPayload`, scope `file-upload` | `router/router_server_files.go:postServerUploadFiles` | Multipart upload. Each part named `files` with a filename is written to `directory/<filename>`. Each file is limited to `api.upload_limit` MB (default 100). Logs `server:file.uploaded` activity |

For all three: invalid signature or expiry → error through `CaptureAndAbort`.
Unknown server, denylisted token, reused `unique_id`, or wrong scope →
`404 {"error":"The requested resource was not found on this server."}`.

### 3.2 Websocket and inter-node routes

| Method & path | Auth | Handler | Purpose |
|---|---|---|---|
| `GET /api/servers/:server/ws` | WS (+SE) | `router/router_server_ws.go:getServerWebsocket` → `handleServerWebsocket` | Console websocket, see §4 |
| `POST /api/transfers` | JWT-h, `TransferPayload`, scope `transfer`, `sub` = server UUID | `router/router_transfer.go:postTransfers` | **Destination node** receives a server transfer archive from the source node |

### 3.3 Node-token routes (global)

| Method & path | Handler | Request | Response | Purpose |
|---|---|---|---|---|
| `POST /api/update` | `router/router_system.go:postUpdateConfiguration` | `config.PanelConfiguration`: `{"debug","uuid","token_id","token","api":{"host","port","ssl":{"enabled","cert","key"},"upload_limit"},"system":{"sftp":{"bind_port"}}}`. Any other keys are ignored. Missing keys keep their current values. `ssl.cert`/`ssl.key` are **always ignored**, so the local values stay | `{"applied": bool}`. It's `false` when `ignore_panel_config_updates` is set | Panel pushes node config. The config is written to disk, applied globally, and the outbound client credentials rotate |
| `GET /api/system[?v=2]` | `router/router_system.go:getSystemInformation` | – | v1: `{"architecture","cpu_count","kernel_version","os","version"}`. `v=2`: `system.Information` `{"version","docker":{...},"system":{"architecture","cpu_threads","memory_bytes","kernel_version","os","os_type"}}` (`system/system.go`) | Node info / heartbeat |
| `GET /api/servers` | `router/router_system.go:getAllServers` | – | `[server.APIResponse]` | List servers |
| `POST /api/servers` | `router/router_system.go:postCreateServer` | `{"uuid": "<uuidv4>", "start_on_completion": bool}` (`server/installer/installer.go:ServerDetails`) | `202`. Also `409` if it exists, or `422` if the UUID is invalid | Create a server. Wings pulls its config from the panel (§2 #3), then installs in the background |
| `DELETE /api/transfers/:server` | `router/router_transfer.go:deleteTransfer` | – | `202` / `409` | Cancel an **incoming** transfer |
| `POST /api/deauthorize-user` | `router/router_system.go:postDeauthorizeUser` | `{"user": "<user uuid>", "servers": ["<uuid>", ...]}`. An empty `servers` means every server | `204` | Revokes the user's JWTs issued until now (`tokens.DenyForServer`), closes their websockets and SFTP sessions |

`server.APIResponse` (`server/server.go`):
`{"state": "offline|starting|running|stopping", "is_suspended": bool, "utilization": ResourceUsage, "configuration": <settings object as §2.1>}`.

`ResourceUsage` (`server/resources.go`, `environment/stats.go`):
`{"memory_bytes","memory_limit_bytes","cpu_absolute","network":{"rx_bytes","tx_bytes"},"uptime","state","disk_bytes"}`.

### 3.4 Node-token routes (per server, `/api/servers/:server`, NODE+SE)

| Method & path | Handler | Request | Response | Purpose |
|---|---|---|---|---|
| `GET ""` | `router/router_server.go:getServer` | – | `server.APIResponse` | Server details |
| `DELETE ""` | `router/router_server.go:deleteServer` | – | `204` | Suspend, emit `deleted` (and `transfer status: completed` if transferring), destroy the container, delete files in the background, unregister |
| `GET /logs?size=` | `getServerLogs` | `size` 1–100, default 100 | `{"data": ["line", ...]}` | Container log tail |
| `POST /power` | `postServerPower` | `{"action":"start\|stop\|restart\|kill","wait_seconds": int}`. `wait_seconds` outside 0–300 becomes 30 | `202`. `422` for a bad action, `400` if suspended and the action is start/restart | Async power action |
| `POST /commands` | `postServerCommands` | `{"commands": ["..."]}` | `204`, or `502` if not running | Send console commands |
| `POST /install` | `postServerInstall` | – | `202` | Sync, then install in the background |
| `POST /reinstall` | `postServerReinstall` | – | `202`, or `409` if a power action is running | Reinstall in the background |
| `POST /sync` | `postServerSync` | – | `204` | Re-pull config from the panel (§2 #3) |
| `POST /ws/deny` | `postServerDenyWSTokens` | `{"jtis": ["..."]}` | `204` | **Deprecated** JTI denylist (`tokens.DenyJTI`). Use `/api/deauthorize-user` instead |
| `POST /transfer` | `router/router_server_transfer.go:postServerTransfer` | `{"url": "<dest>/api/transfers", "token": "<Authorization header value>", "server": ServerDetails}` | `202`, or `409` | **Source node**: stop the server, stream the archive to the destination (§3.6) |
| `DELETE /transfer` | `deleteServerTransfer` | – | `202` / `409` | Cancel an **outgoing** transfer |
| `GET /files/contents?file=[&download=1]` | `router/router_server_files.go:getServerFileContents` | – | Raw bytes. Headers `X-Mime-Type`, `Content-Length`, plus `Content-Disposition` when `download` is set. Named pipes → `400` | Read a file |
| `GET /files/list-directory?directory=` | `getServerListDirectory` | – | `[Stat]` | List a directory |
| `PUT /files/rename` | `putServerRenameFiles` | `{"root": "", "files": [{"from": "", "to": ""}]}` | `204`. `400` if the destination exists, `422` if the list is empty | Rename/move. A missing source is skipped |
| `POST /files/copy` | `postServerCopyFile` | `{"location": ""}` | `204` | Duplicate a file |
| `POST /files/write?file=` | `postServerWriteFile` | raw body, `Content-Length` **required** | `204`. `400` if no length or the target is a directory | Write a file |
| `POST /files/create-directory` | `postServerCreateDirectory` | `{"name": "", "path": ""}` | `204` | mkdir |
| `POST /files/delete` | `postServerDeleteFiles` | `{"root": "", "files": [""]}` | `204` | Delete |
| `POST /files/compress` | `postServerCompressFiles` | `{"root": "", "files": [""]}` | `Stat` of the new `.tar.gz` (mime `application/tar+gzip`). `409` if the disk is full | Archive |
| `POST /files/decompress` | `postServerDecompressFiles` | `{"root": "", "file": ""}` | `204`. `400` for an unknown format or "text file busy" | Extract archive |
| `POST /files/chmod` | `postServerChmodFile` | `{"root": "", "files": [{"file": "", "mode": "<octal string>"}]}` | `204`, or `400` for a bad mode | chmod |
| `GET /files/pull` | `getServerPullingFiles` | – | `{"downloads": [{"Identifier": "", "Progress": 0.0}]}`. The keys are **PascalCase** (`router/downloader/downloader.go:Download.MarshalJSON`) | List remote downloads. Returns `400` if `api.disable_remote_download` is set (`middleware.RemoteDownloadEnabled`) |
| `POST /files/pull` | `postServerPullRemoteFile` | `{"root": "", "directory": "" /*deprecated*/, "url": "", "file_name": "", "use_header": bool, "foreground": bool}` | background: `202 {"identifier"}`; foreground: `200 Stat`, or `400 {"identifier","message"}`. At most 3 concurrent downloads per server | Pull a URL into the server |
| `DELETE /files/pull/:download` | `deleteServerPullRemoteFile` | – | `204` | Cancel a remote download |
| `POST /backup` | `router/router_server_backup.go:postServerBackup` | `{"adapter": "wings"\|"s3", "uuid": "<lower-case uuid>", "ignore": "<gitignore-style lines>"}` | `202` | Create a backup in the background. Reports back through §2 #9/#10 and the `backup completed` WS event |
| `POST /backup/:backup/restore` | `postServerRestoreBackup` | `{"adapter": "wings"\|"s3", "truncate_directory": bool, "download_url": "" /* required for s3 */}` | `202`, or `400` for a bad URL, blocked host, non-200 status or non-gzip content type | Restore in the background. Reports back through §2 #11 and the `backup restore completed` WS event |
| `DELETE /backup/:backup` | `deleteServerBackup` | – | `204`, or `404` | Delete a **local** backup |

`Stat` (`server/filesystem/stat.go:Stat.MarshalJSON`):
`{"name","created","modified","mode","mode_bits","size","directory","file","symlink","mime"}`.
`created` and `modified` are RFC3339. `mode_bits` is an octal string.

Backup UUIDs must be canonical lower-case UUIDs (`router_server_backup.go:parseBackupUuid`).

### 3.5 Filesystem errors (all file routes)

`router/middleware/request_error.go:asFilesystemError` maps filesystem error
codes to: `404` not found, `403` "present in egg denylist", `400` is a
directory / not a regular file / no disk space / name too long, and `404`
directory missing.

### 3.6 Server transfer (three parties)

1. **Panel → source**: `POST /api/servers/:server/transfer` with `{url, token, server}` (§3.4).
2. **Source → destination**: `POST <url>` (`server/transfer/source.go:Transfer.PushArchiveToTarget`):
   - `Authorization: <token>`. The **raw** `token` value is used as the header.
     The destination (`postTransfers`) expects `Bearer <jwt>`, so the panel
     must include the `Bearer ` prefix in `token`. (Panel side **UNVERIFIED**.)
   - `Content-Type: multipart/form-data`. Part `archive` (`archive.tar.gz`)
     comes first, then field `checksum` = hex SHA-256 of the archive bytes.
   - The source requires `200`. Progress is sent as `transfer logs` WS events every 5 s.
3. **Destination** (`router/router_transfer.go:postTransfers`):
   - Validates the JWT (`TransferPayload`, scope `transfer`). `sub` must parse as a UUID.
   - `installer.New` → `GET /servers/{uuid}` (§2 #3), then `ExtractTransfer`
     streams the archive into the data directory, verifies the checksum and
     creates the environment.
   - `completeIncomingTransfer` → `POST /servers/{uuid}/transfer/success|failure`
     (§2 #6). On failure, all local state is removed.
4. **Panel → source** afterwards: `DELETE /api/servers/:server`. When it's
   deleted mid-transfer, the source emits `transfer status: completed`. Who
   triggers this deletion is panel behaviour, so **UNVERIFIED** here.

---

## 4. Console websocket

Route: `GET /api/servers/:server/ws` (`router/router.go`). Handler:
`router/router_server_ws.go:handleServerWebsocket`. Message logic:
`router/websocket/websocket.go:Handler.HandleInbound`.

### 4.1 Connection rules

| Rule | Value | Source |
|---|---|---|
| Origin check | `Origin` must equal `remote` (panel URL) or match `allowed_origins` (`*` allowed) | `websocket.go:GetHandler` (`CheckOrigin`) |
| Max authenticated sockets per server | 30, across all users. Over the limit: HTTP `400` before upgrade, or `errTooManyWebsockets` on `auth` | `router_server_ws.go:maxWebsocketConnections`, `server/websockets.go:TryPush` |
| Auth deadline | 30 s after connecting, else closed | `router_server_ws.go:websocketAuthenticationTimeout` |
| Suspended server | Upgrade, then close frame **4409** `"server is suspended"` | `handleServerWebsocket` |
| Frame limits | Read limit 4096 bytes (compressed); text frames only; >32768 bytes dropped; invalid JSON silently ignored | `GetHandler`, `handleServerWebsocket` |
| Global inbound rate | 10 burst, refill every 200 ms. Over the limit → `throttled` `["global"]` | `handleServerWebsocket` |
| Per-event rate | `auth`, `send logs`: 2 burst, 1 per 5 s each. `send command`: burst 10, 1/s. Everything else shares burst 4, 1/s. Over the limit → `throttled` `["<event>"]` | `router/websocket/limiter.go:limitValuesFor` |
| Response headers | `Content-Security-Policy: default-src 'self'`, `X-Frame-Options: DENY` | `handleServerWebsocket` |

### 4.2 Message envelope

`router/websocket/message.go:Message`: `{"event": "<string>", "args": ["<string>", ...]}`.
Every arg is a string. Structured data is a JSON **string** inside `args[0]`.

### 4.3 Auth flow

1. Client connects, then sends `{"event":"auth","args":["<jwt>"]}`. The args are joined with `""`.
2. `websocket.go:NewTokenPayload` → `tokens.ParseToken`: HS256 signature and `exp`.
   It then rejects if denylisted (§5.3), if the `websocket.connect` permission
   is missing, or if the scope isn't `websocket`.
3. The token's `server_uuid` must equal the socket's server, else `jwt: server uuid mismatch`.
4. On the first successful auth, the socket is counted toward the 30 limit, listeners
   are registered, and Wings sends `auth success`, then `status` `[<state>]`.
   When the server is offline (and not installing or transferring) it also sends
   `stats` `[<json ResourceUsage>]`.
5. **Re-auth** with a fresh JWT: send `auth` again on the same socket. Wings
   replaces the token and sends `auth success`, but not the duplicate status
   (`websocket.go`, comment references panel#2077).
6. Before handling any non-`auth` event, and before **every** outbound event,
   the token is re-validated (`Handler.validJwt`): expiry, denylist,
   connect permission, scope, server UUID. If that fails, Wings sends
   `jwt error` `[<reason>]` instead of the event.
7. Expiry watcher, every 30 s (`listeners.go:listenForExpiration`): sends
   `token expiring` once ≤ 60 s remain, and `token expired` once past `exp`.
   The code uses 60 s, although its comment says 3 minutes.

JWT error strings (`websocket.go`): `jwt: no jwt present`,
`jwt: missing connect permission`, `jwt: server uuid mismatch`,
`jwt: created too far in past (denylist)`, plus the library's expiry
error (`jwt.ErrExpValidation`).

### 4.4 Client → Wings events

| Event | Args | Permission needed | Behaviour |
|---|---|---|---|
| `auth` | `[jwt]` | `websocket.connect` + scope `websocket` | See §4.3 |
| `set state` | `["start"\|"stop"\|"restart"\|"kill"]` | `control.start` / `control.stop` / `control.restart`. `kill` needs `control.stop` | `Server.HandlePowerAction`. Records `server:power.<action>` activity. If another action holds the lock: `daemon error`. Missing permission: silently ignored |
| `send logs` | – | (connect) | When running, sends the last `system.websocket_log_count` (default 150) lines as `console output` events |
| `send stats` | – | (connect) | Sends `stats` |
| `send command` | `[command]` | `control.console` | Ignored if offline, or starting and not yet attached. Records `server:console.command` activity |

Unknown events are ignored.

### 4.5 Wings → client events

| Event | Args | Emitted by | Permission filter |
|---|---|---|---|
| `auth success` | – | `HandleInbound` | – |
| `status` | `[offline\|starting\|running\|stopping]` | `server/server.go:OnStateChange`, auth | – |
| `stats` | `[json ResourceUsage]` | `server/listeners.go` (resource poll), `OnStateChange` (on offline), `send stats` | – |
| `console output` | `[line]` | Container log sink (`system.LogSink`), daemon messages (`server/console.go:PublishConsoleOutputFromDaemon`, prefixed `[<app_name> Daemon]:`) | – |
| `install output` | `[line]` | Install sink, image-pull status | `admin.websocket.install` |
| `install started` / `install completed` | `[""]` | `server/install.go:Server.install` | – |
| `daemon message` | `[text]` | Install/restore progress (`server/install.go`, `server/backup.go`, `router_server_backup.go`) | – |
| `backup completed` | `[json {"uuid","is_successful","checksum","checksum_type":"sha1","file_size"}]` | `server/backup.go:Server.Backup` publishes `backup completed:<uuid>`. `events/events.go:Bus.Publish` cuts the topic at the first `:`, so clients receive plain `backup completed` and must read `uuid` from the payload | `backup.read` (any event starting with `backup completed` is gated) |
| `backup restore completed` | `[""]` | `router_server_backup.go:postServerRestoreBackup` | `backup.read` (prefix match on `backup completed` does **not** match `backup restore completed`, so in practice there is **no filter**) |
| `transfer logs` | `[line]` | `server/transfer/transfer.go:SendMessage` | `admin.websocket.transfer` |
| `transfer status` | `[pending\|processing\|cancelling\|cancelled\|failed\|completed\|"failure"\|"success"]` | `transfer.go:SetStatus`, `router_transfer.go`, `router_server_transfer.go`, `deleteServer` | – |
| `deleted` | none. The payload is `nil`, marshalled as the arg `"null"` | `router_server.go:deleteServer` | – |
| `token expiring` / `token expired` | – | `listeners.go:listenForExpiration` | – |
| `daemon error` | `["Error Event [<uuid>]: <msg>"]`. The real message is shown only with `admin.websocket.errors`; otherwise a generic message | `websocket.go:SendErrorJson` | see text |
| `jwt error` | `[reason]` | `validJwt` failures, `SendErrorJson` for JWT errors | – |
| `throttled` | `["global"\|"<event>"]` | rate limiters | – |

Events reach the socket through `router/websocket/listeners.go:listenForServerEvents`.
The internal bus round-trips every event through JSON (`events/events.go`).
A string payload goes out as-is. Anything else (maps, structs, `nil`) is
JSON-encoded into `args[0]`.

**Not emitted:** remote-download progress. `router/router_server_files.go:getServerPullingFiles`
and `router/downloader/downloader.go:counter` have comments that mention websocket
progress events, but no code publishes one.

Wildcard rule (`router/tokens/websocket.go:WebsocketPayload.HasPermission`):
`"*"` grants every permission **except** those starting with `admin`.
Each `HasPermission` call also re-checks the denylist.

---

## 5. JWTs

### 5.1 Common rules

| Aspect | Value | Source |
|---|---|---|
| Signed by | The **panel** (Wings never issues JWTs) | Wings only verifies: `router/tokens/parser.go:ParseToken` |
| Algorithm / key | HMAC-SHA256 keyed with the node's secret `token` | `config/config.go:Set` → `jwt.NewHS256`, `config.GetJwtAlgorithm` |
| Library | `github.com/gbrlsnchs/jwt/v3` | `go.mod` |
| Validated claims | `exp` only (`jwt.ExpirationTimeValidator`). `iss`, `aud`, `nbf` are **not** checked | `parser.go:ParseToken` |
| Header `alg` check | Whether `jwt.Verify` rejects a mismatched `alg` header is library behaviour: **UNVERIFIED** | – |
| Scope claim | `"scope": "<space-separated scopes>"`. The type-specific scope must be present | `router/tokens/token.go:Scoped.HasScope` |
| Rotation | When `/api/update` changes the token, the HMAC key changes too, so earlier JWTs become invalid | `config.Set` |

The scope check came from upstream commit `d0ddc80` ("Verify scope of tokens
provided by panel", 2026-05-23). A stock panel that predates it won't send
`scope`, and every JWT route would reject its tokens. Which panel version
adds `scope`: **UNVERIFIED**.

### 5.2 Token types

| Type (Go) | File | Scope | Custom claims | Accepted by | One-time? | Denylist? |
|---|---|---|---|---|---|---|
| `WebsocketPayload` | `router/tokens/websocket.go` | `websocket` | `user_uuid`, `server_uuid`, `permissions: []string` | `GET /api/servers/:server/ws` (`auth` event) | No | Yes |
| `FilePayload` | `router/tokens/file.go` | `file-download` | `file_path`, `server_uuid`, `user_uuid`, `unique_id` | `GET /download/file` | Yes (`unique_id`) | Yes |
| `UploadPayload` | `router/tokens/upload.go` | `file-upload` | `server_uuid`, `user_uuid`, `unique_id` | `POST /upload/file` | Yes | Yes |
| `BackupPayload` | `router/tokens/backup.go` | `backup-download` | `server_uuid`, `user_uuid`, `backup_uuid`, `unique_id` | `GET /download/backup` | Yes | Yes |
| `TransferPayload` | `router/tokens/transfer.go` | `transfer` | none. Uses `sub` = server UUID | `POST /api/transfers` | No | No |

Each also embeds the registered claims (`jwt.Payload`: `iss`, `sub`, `aud`,
`exp`, `nbf`, `iat`, `jti`).

- **One-time use:** `unique_id` is cached in memory for 60 min.
  A second use → `404` (`router/tokens/token_store.go:TokenStore.IsValidToken`).
  The cache is per-process, so it resets when Wings restarts.
- **Transfer token signer:** the destination verifies with **its own**
  node token, so the panel has to sign the transfer JWT with the
  **destination** node's secret. (Required by the code; the panel side is **UNVERIFIED**.)
- **Expiry lengths** come from the panel and aren't visible here: **UNVERIFIED**.
- **Websocket JTI:** a code comment says the panel derives it as
  `MD5(user.id + server.uuid)` (`router/tokens/websocket.go`): **UNVERIFIED**.

### 5.3 Revocation / denylist

`router/tokens/websocket.go:isDenylisted` applies to websocket, file, upload
and backup tokens. A token is rejected if any of these holds:

1. `iat` is missing, or `server_uuid`/`user_uuid` is empty.
   (Tokens from a panel that leaves out `user_uuid` are therefore rejected: **compatibility risk, UNVERIFIED** against the stock panel.)
2. `iat` is earlier than the Wings process start (`wingsBootTime`).
   Restarting Wings invalidates every outstanding token.
3. The `jti` is on the deprecated JTI denylist (`POST /api/servers/:server/ws/deny`)
   with a deny time ≥ `iat`.
4. `server_uuid:user_uuid` is on the user denylist (`POST /api/deauthorize-user`)
   with a deny time ≥ `iat`.

All denylist state is in memory only.

---

## 6. SFTP authentication flow

Server: `sftp/server.go`. Per-session handler: `sftp/handler.go`.

1. **Listener:** `system.sftp.bind_address:bind_port`.
   The host key is `<system.data>/.sftp/id_ed25519` (ED25519, PKCS#8 PEM),
   generated on first run (`SFTPServer.Run`, `generateED25519PrivateKey`).
   KEX, ciphers and MACs are restricted to modern sets. `MaxAuthTries: 6`.
2. **Pre-auth limits** (`SFTPServer.serve`, `defaultLimits`):
   - handshake and auth must finish within 2 min
   - at most 128 pending handshakes in total
   - at most 16 per IP or IPv6 /64 (`max_authenticating_connections_per_ip`)
   - at most 32 sessions per authenticated user+server (`max_connections_per_user`)
3. **Username filter:** the username must match `^(?i)(.+)\.([a-z0-9]{8})$`
   (`<panel username>.<8-char server short id>`). Otherwise Wings rejects it
   locally without calling the panel (`validUsernameRegexp`, `makeCredentialsRequest`).
4. **Credential check:** both password and public-key auth call
   `POST /api/remote/sftp/auth` (§2 #8). For public keys, `password` holds
   `ssh.MarshalAuthorizedKey(key)`.
   - `4xx` → `SftpInvalidCredentialsError`, so auth fails.
   - Anything else that fails → error, so auth fails.
5. **Session permissions:** set from the panel's response as SSH extensions
   `ip`, `uuid` (= `server`), `user`, `permissions` (comma-joined).
   A response without `user` fails with "Panel 1.10 is required"
   (`sftp/handler.go:NewHandler`).
6. **Channel:** only the `session` channel type is allowed, and only the
   `subsystem` request with payload `sftp`. The server named in `uuid` must
   exist on this node (`SFTPServer.handle`).
7. **Authorization per operation** (`sftp/handler.go:Handler.can`):
   - permissions `file.read`, `file.read-content`, `file.create`, `file.update`, `file.delete`, or `*`
   - **every** operation is denied while the server is suspended or in a protected state (installing, transferring or restoring: `server/install.go:IsInProtectedState`)
   - `system.sftp.read_only` disables writes node-wide
   - at most 64 open files per session
8. **Revocation:** `POST /api/deauthorize-user` → `Server.Sftp().Cancel(user)`.
   A suspension found during `Sync` → `CancelAll()`. Live sessions are closed
   (`SFTPServer.Handle`).
9. **Activity:** SFTP file events (`server:sftp.write|create|create-directory|rename|delete`)
   are stored in the local SQLite DB (`sftp/event.go`, `internal/database`).
   They're merged by `internal/cron/sftp_cron.go`, then sent through `POST /activity` (§2 #12).

---

## 7. Activity event names sent to the panel

`server/activity.go`:

- `server:power.<start|stop|restart|kill>`
- `server:console.command`
- `server:sftp.write`, `server:sftp.create`, `server:sftp.create-directory`, `server:sftp.rename`, `server:sftp.delete`
- `server:file.uploaded`

---

## 8. Open questions (all UNVERIFIED)

- Exact `exp` lifetimes and `iss`/`aud` values the stock panel puts in each JWT type.
- Whether the current stock panel always includes `scope`, `user_uuid` and `iat`. Wings rejects tokens without them.
- Whether the stock panel's `/api/remote/*` routes care about the `Accept: application/vnd.pterodactyl.v1+json` or `User-Agent` headers. Keep both unchanged until this is checked.
- Whether the panel's transfer `token` field includes the `Bearer ` prefix. The code requires it.
- Whether `jwt.Verify` (gbrlsnchs/jwt v3) enforces the header `alg`.
- Whether any panel still calls `POST /api/servers/:server/ws/deny`, or handles `POST /servers/{uuid}/archive` (Wings never calls it).
