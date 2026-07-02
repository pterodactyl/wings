[![Logo Image](https://cdn.pterodactyl.io/logos/new/pterodactyl_logo.png)](https://pterodactyl.io)

# Pterodactyl Wings — Free Hosting Fork

This is a hardened fork of **Pterodactyl Wings** designed specifically for running **free/shared game hosting nodes**. It keeps everything that makes Wings great, but adds a set of aggressive safety, abuse-prevention, and resource-management features that are essential when you let strangers run arbitrary game servers on your hardware.

## What makes this fork different?

| Feature | Purpose |
|---------|---------|
| **No auto-start on boot** | Prevents a thundering herd of thousands of containers starting simultaneously after a node reboot. |
| **Egress firewall** | Blocks all outbound container traffic except DNS/HTTP(S), stopping most DDoS/malware/abuse vectors. |
| **Firewall whitelist** | Lets you exempt specific server UUIDs from the egress firewall in real time. |
| **Disk cleanup** | Automatically deletes old stopped servers and old backups when disk usage crosses a threshold. |
| **Server killer (disk)** | Forcibly stops/kills running servers when disk usage becomes critical. |
| **Memory killer** | Forcibly stops/kills running servers when system RAM becomes critical. |
| **AI startup scanner** | Sends startup commands to OpenRouter and bans servers that look malicious. |
| **Telegram notifications** | Alerts you about cleanups, kills, and AI bans in a single shared chat. |

---

## Quick config example

```yaml
# /etc/pterodactyl/config.yml

docker:
  network:
    restrict_outbound: true
    allowed_outbound_ports: [53, 80, 443]

system:
  disk_cleanup:
    volumes:
      enabled: true
      interval: 120
      threshold: 90
      target: 75
    backups:
      enabled: true
      interval: 300
      threshold: 85
      target: 70

  server_killer:
    enabled: true
    interval: 60
    path: "/var/lib/pterodactyl/volumes"
    soft:
      threshold: 92
      count: 5
      action: "kill"
      order: "oldest"
    hard:
      threshold: 97
      action: "stop"

  memory_killer:
    enabled: true
    interval: 30
    soft:
      threshold: 85
      count: 3
      action: "kill"
      order: "newest"
    hard:
      threshold: 95
      action: "kill"

  ai_scanner:
    enabled: true
    openrouter_api_key: "sk-or-v1-..."
    openrouter_model: "openai/gpt-3.5-turbo"
    startup_hang_seconds: 30
    danger_threshold: 7
    rules_path: "/etc/pterodactyl/ai-rules.json"
    bans_path: "/etc/pterodactyl/ai-bans.json"

  telegram_notifications:
    enabled: true
    bot_token: "123456:ABC-DEF..."
    chat_id: "-1001234567890"
    thread_id: 42
```

---

## Feature reference

### No auto-start on boot

By default, Wings restores every running container after a reboot. On a free node with thousands of servers this creates a massive load spike. This fork removes that behavior: containers are created and images are pulled, but servers remain offline until started manually through the Panel or API.

**File changed:** `cmd/root.go`

---

### Container egress firewall

When `docker.network.restrict_outbound` is enabled, every container on the `pterodactyl0` bridge is blocked from making outbound connections except to the ports listed in `allowed_outbound_ports`.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `restrict_outbound` | `false` | Enable/disable the egress firewall. |
| `allowed_outbound_ports` | `[53, 80, 443]` | Destination ports allowed outbound (DNS, HTTP, HTTPS). |

Requirements:
- Wings must run as root or with `CAP_NET_ADMIN`.
- `iptables` and `ip6tables` must be installed.
- Containers using a dedicated outgoing IP (`ForceOutgoingIP`) are on a separate network and are **not** covered.

**Files changed:** `environment/firewall.go`, `environment/docker.go`, `config/config_docker.go`

---

### Firewall whitelist

You can exempt specific servers from the egress firewall by adding their UUIDs to `/etc/pterodactyl/firewall-whitelist.json`:

```json
[
  "550e8400-e29b-41d4-a716-446655440000",
  "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
]
```

The file is read in real time, so changes apply immediately. Wings resolves each whitelisted UUID to its container IP and adds an iptables/ip6tables rule in the `PTERO-EGRESS-WHITELIST` chain.

**Files changed:** `environment/firewall.go`, `server/firewall_whitelist.go`, `server/power.go`, `cmd/root.go`

---

### Automatic disk cleanup

Monitors the `volumes` and `backups` directories. When usage exceeds `threshold`, the oldest objects are deleted until usage drops to `target`.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `false` | Enable cleanup for this directory. |
| `interval` | `120` (volumes) / `300` (backups) | Seconds between checks. |
| `threshold` | `90` / `85` | Usage % that triggers cleanup. |
| `target` | `80` / `70` | Usage % to reach after cleanup. |

- **volumes**: deletes whole stopped server directories, skipping running servers and servers in install/restore/transfer.
- **backups**: deletes individual backup archive files, oldest first.

**Warning:** this permanently deletes data. Use only on disposable free nodes.

**Files changed:** `internal/cron/disk_cleanup.go`, `system/disk.go`, `config/config.go`, `internal/cron/cron.go`

---

### Server killer (disk)

Emergency response to critical disk usage.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `interval` | `60` | Seconds between checks. |
| `path` | `""` (uses `system.data`) | Filesystem path to monitor. |
| `soft.threshold` | — | First-level trigger. |
| `soft.count` | — | How many running servers to act on. |
| `soft.action` | `"kill"` | `"stop"` (graceful) or `"kill"` (SIGKILL). |
| `soft.order` | `"oldest"` | `"oldest"` or `"newest"` by data directory mtime. |
| `hard.threshold` | — | Last-resort trigger. |
| `hard.action` | — | Action applied to **all** running servers. |

**Files changed:** `internal/cron/server_killer.go`, `internal/cron/resource_killer.go`, `config/config.go`, `internal/cron/cron.go`

---

### Memory killer

Same idea as the disk killer, but monitors system RAM via `/proc/meminfo`.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `interval` | `60` | Seconds between checks. |
| `soft.threshold` / `soft.count` / `soft.action` / `soft.order` | — | First-level response. |
| `hard.threshold` / `hard.action` | — | All-running-servers response. |

**Files changed:** `internal/cron/memory_killer.go`, `system/mem.go`, `config/config.go`, `internal/cron/cron.go`

---

### AI startup command scanner

After a server starts, Wings waits `startup_hang_seconds`. If the server is still running, its startup command is sent to OpenRouter. The model returns a danger score from 0 to 10. If the score is >= `danger_threshold`, the server UUID is banned, the container is killed, and a Telegram alert is sent.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `false` | Enable the scanner. |
| `openrouter_api_key` | — | Your OpenRouter API key. |
| `openrouter_model` | `"openai/gpt-3.5-turbo"` | Model identifier. |
| `startup_hang_seconds` | `30` | How long the command must be running before analysis. |
| `danger_threshold` | `7` | Minimum danger score (0-10) that triggers a ban. |
| `rules_path` | `/etc/pterodactyl/ai-rules.json` | Cache of dangerous commands. |
| `bans_path` | `/etc/pterodactyl/ai-bans.json` | Banned UUIDs. Read in real time. |

Fail-open: AI errors or unparseable responses do **not** ban servers.

**Files changed:** `server/ai_scanner.go`, `config/config.go`, `server/power.go`, `cmd/root.go`, `server/manager.go`, `server/server.go`

---

### Telegram notifications

One shared bot configuration for alerts from disk cleanup, killers, and AI scanner.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `false` | Enable Telegram alerts. |
| `bot_token` | — | Telegram bot token. |
| `chat_id` | — | Target chat ID. |
| `thread_id` | `0` | Optional message thread / topic ID. |

**Files changed:** `system/telegram.go`, `config/config.go`, `internal/cron/disk_cleanup.go`, `internal/cron/resource_killer.go`, `server/ai_scanner.go`

---

## Installation

1. Download the binary from the GitHub Actions artifacts.
2. Replace `wings` on the node:
   ```bash
   chmod +x wings
   mv wings /usr/local/bin/wings
   systemctl restart wings
   ```

## Building

### GitHub Actions

- **Actions** → **Build Custom Wings** → **Run workflow** produces `wings_linux_amd64` and `wings_linux_arm64`.
- Push to `develop` or tag `v*` triggers the original Docker and release workflows.

### Local build (Linux)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -v -trimpath \
  -ldflags="-s -w -X github.com/pterodactyl/wings/system.Version=custom" \
  -o wings \
  github.com/pterodactyl/wings
```

---

## Important warnings

- This fork is built for **free/disposable nodes**. Several features permanently delete data or forcibly kill running game servers.
- The egress firewall, disk cleanup, killers, and AI scanner are all **opt-in** and disabled by default.
- Test every feature with non-critical servers before enabling it in production.
- AI scanning sends startup commands to a third-party API. Review OpenRouter's privacy policy and costs.

## License

This fork inherits the license of the original [Pterodactyl/Wings](https://github.com/pterodactyl/wings) project.
