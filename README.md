<div align="center">

# ⚔ appsassin

**Your Mac is full of things you forgot to close. appsassin finds them and takes them out.**

The `next dev` from a terminal tab you closed on Tuesday. The Slack helper that outlived Slack.
The SSH tunnel from last week. Spotify, open with no windows, holding 600 MB.
appsassin finds all of it, explains why each one looks forgotten, and kills what you pick.
You can also let it do this silently in the background.

[![CI](https://github.com/mikemich/appsassin/actions/workflows/ci.yml/badge.svg)](https://github.com/mikemich/appsassin/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mikemich/appsassin?color=e0245e)](https://github.com/mikemich/appsassin/releases/latest)
![macOS](https://img.shields.io/badge/macOS-12%2B-black?logo=apple)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

<img src="docs/demo.gif" alt="appsassin demo: marking three stray dev servers, killing them, engaging stealth mode, switching to minimal mode" width="880">

</div>

## Install

With Homebrew:

```bash
brew install mikemich/tap/appsassin
```

Or download the binary directly:

```bash
curl -fsSL https://github.com/mikemich/appsassin/releases/latest/download/appsassin-macos.tar.gz | tar xz
mkdir -p ~/.local/bin && mv appsassin ~/.local/bin/   # anywhere on your PATH
```

This is one universal binary for Apple Silicon and Intel Macs.

If you have Go:

```bash
go install github.com/mikemich/appsassin@latest
```

Then run `appsassin`. To look around first without risk, run `appsassin --demo`. Demo mode shows made-up targets, and kills are simulated.

## Why

Activity Monitor lists **everything**: about 700 processes, mostly macOS internals. `kill` needs a PID you have to dig up first. Neither knows which processes you *forgot about*.

appsassin only looks for **strays**:

| | What it is | How appsassin spots it |
|---|---|---|
| 🧟 **Orphans** | Dev servers, watchers, tunnels and agents whose terminal or parent process died | launchd adopted them (parent PID 1), they aren't launchd jobs, and they have no terminal |
| 👻 **Leftover helpers** | Helper processes that kept running after you quit their app | They live inside an `.app` bundle whose main app isn't running |
| 💤 **Forgotten apps** | Dock apps with no windows that you haven't touched in hours | Window count from CoreGraphics, plus a record of when each app was last in front |

Each target gets a **stray score** from 0 to 99, and every point comes with a reason you can read.

## Full mode

<img src="docs/full.png" alt="appsassin full mode" width="880">

The list is sorted by how forgotten each target looks. The detail pane shows the full command line, PID, children, ports and memory. Mark targets with `space` and kill them with `x`. You always confirm first:

<img src="docs/confirm.png" alt="kill confirmation" width="880">

Processes get `SIGTERM` first, then `SIGKILL` if they ignore it. Each target's whole process tree goes with it, so `npm` doesn't leave a `node` behind. **Apps get a polite Cmd-Q**, so if they have unsaved work they can ask you about it. `K` force-kills when you really mean it.

<img src="docs/killed.png" alt="after the kill: 3 down, 1.0G freed" width="880">

## Minimal mode

One line per target and nothing else, so it fits in a small tmux split. Start it with `appsassin -m` or press `m` to toggle.

<img src="docs/minimal.png" alt="appsassin minimal mode" width="560">

## Stealth mode

A silent background killer. It has no UI and prints nothing, and it runs at low CPU and I/O priority. It sweeps every 5 minutes and logs every kill.

```bash
appsassin stealth start            # for this login session (add --dry-run to only log)
appsassin stealth install          # start automatically at every login (LaunchAgent)
appsassin stealth status | log | stop | uninstall
```

You can also press `S` in the app.

<img src="docs/stealth.png" alt="stealth mode log" width="880">

Stealth mode is deliberately conservative:

- It only acts on targets scoring **≥ 70**.
- It kills orphans and leftover helpers outright.
- **Apps are only ever asked to quit**, never force-killed. An app with unsaved changes stays open, and stealth mode logs it.
- An app qualifies only if it has **no windows** and stealth mode **actually watched it go unused for 6 hours**. Having been open for a long time isn't enough.
- While it runs, it records which app is in front. That makes "unused" times in the app real numbers instead of estimates.

## How the score works

| Signal | Points |
|---|---|
| Leftover helper of a quit app | +55 |
| Orphaned process (parent exited) | +45 |
| …and it's a dev runtime (`node`, `python`, `vite`, `next`, `rails`, `jupyter`…) | +15 |
| App with no windows | +40 |
| App hidden | +10 |
| App unused for >1h / >6h / >24h (half if never seen in front since launch) | +8 / +15 / +25 |
| Listening on a TCP port | +10 |
| Idle: under 0.5% CPU over its whole lifetime | +10 |
| Running for >2h / >24h / >7d | +5 / +10 / +15 |
| Holding >150 MB / >500 MB / >2 GB (whole process tree) | +5 / +10 / +20 |
| App visible on screen | −15 |
| Busy right now (≥25% CPU) | −20 |

## What it will never touch

- Anything owned by root or another user, or anything under `/System/Library`, `/usr/libexec`, `/usr/sbin` or `/Library/Apple`
- launchd-managed jobs, app extensions (`.appex`) and XPC services
- Anything attached to a terminal, appsassin itself, and the shell and terminal app it's running in
- The front app, menu-bar and background-only apps, Finder, Dock, Control Center
- Anything on your ignore list. Press `i` to spare a target forever. `tmux`, `screen`, `ssh-agent` and `gpg-agent` are on it by default.

Just before it sends a signal, appsassin checks that the PID still belongs to the same executable. A PID reused by another process is never hit.

It needs **no Accessibility, Screen Recording, Automation or admin permissions**.

## Keys

| Key | Action | Key | Action |
|---|---|---|---|
| `↑↓` `j k` | move | `/` | filter |
| `space` | mark | `s` | sort: score → memory → age → name |
| `a` | mark all / none | `+` `-` | raise / lower the score threshold |
| `x` | kill marked (or current) | `A` | show everything with any score |
| `K` | force kill | `m` | toggle minimal mode |
| `i` | spare it forever (ignore list) | `S` | toggle stealth mode |
| `esc` | clear marks and filter | `?` / `q` | help / quit |

## Command line

```bash
appsassin list [--all] [--json]                  # print targets, scriptable
appsassin sweep [--dry-run] [--apps] [--threshold N]  # one unattended pass, like stealth mode
appsassin config                                 # where the config lives and what's in it
```

## Config

`~/.config/appsassin/config.json` is created on first run. Stealth mode re-reads it before every sweep.

```json
{
  "threshold": 30,
  "minimal": false,
  "ignore": ["tmux", "screen", "zellij", "ssh-agent", "gpg-agent", "mosh-server"],
  "stealth": { "interval": "5m", "threshold": 70, "apps": true, "appIdle": "6h", "dryRun": false }
}
```

Ignore entries match the name, executable, bundle ID or path, and can use globs such as `com.jetbrains.*`.

## Build from source

You need macOS, Go 1.22+ and the Xcode command-line tools. cgo is used for AppKit and CoreGraphics.

```bash
git clone https://github.com/mikemich/appsassin && cd appsassin
go build -o appsassin .
./scripts/screenshots.sh   # regenerates docs/ from --demo (needs tmux + ffmpeg)
```

## License

MIT. The screenshots and GIF use `--demo` data; no real processes were harmed.
