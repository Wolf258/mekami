# Platform support

Mekami runs on Linux, macOS, and Windows. The core indexing pipeline is portable; the per-OS bits are concentrated in three places.

## Service manager integration

`mekami service install` registers the supervisor as a per-user system service:

| OS | Backend | Unit file |
| --- | --- | --- |
| Linux | `systemd --user` | `~/.config/systemd/user/mekami-supervisor.service` |
| macOS | `launchd` (LaunchAgent) | `~/Library/LaunchAgents/dev.mekami.supervisor.plist` |
| Other | not implemented | — |

Source split:

- `cmd/mekami/service_linux.go` — `systemctl --user` enable/start.
- `cmd/mekami/service_darwin.go` — `launchctl bootstrap`/`kickstart`.
- `cmd/mekami/service_other.go` — returns "not implemented".

On unsupported platforms the supervisor still works: it is launched the first time any `mekami` command needs it, and the watchdog keeps it alive across reboots. You can also run the supervisor from your shell rc.

## Filesystem watching

`internal/watch` uses `github.com/fsnotify/fsnotify` on every platform. The `Source` abstraction lets the daemon swap `fsnotify` for a polling source on filesystems where inotify is unreliable:

- **Linux**: `inotify` via `fsnotify`. Per-user watch budget tracked by the supervisor (default 8192 watches, degraded at 80% usage to the poller).
- **macOS**: `FSEvents` via `fsnotify`. No per-user budget; the poller fallback is for NFS / SMB / FUSE mounts only.
- **Windows**: `ReadDirectoryChangesW` via `fsnotify`. Same NFS / SMB / FUSE fallback.
- **NFS / SMB / FUSE** (any OS): auto-detected and switched to the poller (`fallback: "auto"`).

## SQLite

`modernc.org/sqlite` is pure Go, so the same binary runs on every supported platform without CGo. The driver is loaded at link time and bundled into the binary.

## Tested CI matrix

`.github/workflows/mekami.yml` runs on every push to `main` and on every pull request, on:

- `ubuntu-latest`
- `macos-latest`
- `windows-latest`

with Go 1.26.

## Known platform quirks

- **macOS socket path limit.** Unix sockets on macOS are limited to 104 bytes for `sun_path`. The `internal/socktestutil` package ships `ShortSockDir` so tests can use a shorter path.
- **Windows service manager.** Windows is supported for the core CLI and the `serve` mode, but `service install` is not implemented. Run the supervisor from a scheduled task or manually.
