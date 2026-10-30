#!/usr/bin/env bash
# Regenerates docs/*.png and docs/demo.gif from `appsassin --demo`.
# Needs: tmux, swift (Xcode CLT), ffmpeg.
set -euo pipefail
cd "$(dirname "$0")/.."

go build -o appsassin .
tmp=$(mktemp -d)
trap 'tmux kill-session -t shot 2>/dev/null || true; rm -rf "$tmp"' EXIT
frame=0

start() { # cols rows args...
  COLS=$1 ROWS=$2
  tmux kill-session -t shot 2>/dev/null || true
  tmux new-session -d -s shot -x "$1" -y "$2" -e COLORTERM=truecolor -e TERM=xterm-256color "./appsassin ${*:3}"
  sleep "${WAIT:-1.2}"
}
keys() { tmux send-keys -t shot "$@"; sleep "${PAUSE:-0.35}"; }
grab() { tmux capture-pane -t shot -e -N -p >"$tmp/$1.ansi"; }
render() { swift scripts/shot.swift "$tmp/$1.ansi" "$2" "appsassin — zsh" "$COLS" "$ROWS"; }
gif() { # hold-seconds
  frame=$((frame + 1))
  grab "f$frame"
  render "f$frame" "$tmp/f$(printf %02d $frame).png"
  echo "file '$tmp/f$(printf %02d $frame).png'" >>"$tmp/frames.txt"
  echo "duration $1" >>"$tmp/frames.txt"
}

# Boot animation: grab it fast while it plays, render afterwards.
WAIT=0.08 start 118 30 --demo
for i in $(seq -w 1 12); do grab "boot$i"; sleep 0.05; done
for i in $(seq -w 1 12); do render "boot$i" "$tmp/b$i.png" & done
wait
for i in $(seq -w 1 12); do
  echo "file '$tmp/b$i.png'" >>"$tmp/frames.txt"
  echo "duration 0.06" >>"$tmp/frames.txt"
done
cp "$tmp/b08.png" docs/boot.png
sleep 1

# Full mode: mark three dev servers, land on an app to show its details.
gif 1.6
keys Space; gif 0.5
keys Down; gif 0.5
keys Space; gif 0.5
keys Space; gif 1.2
grab full && render full docs/full.png
keys x; gif 2.0
grab confirm && render confirm docs/confirm.png
keys y
sleep 1.3
gif 2.4
grab killed && render killed docs/killed.png
# Graveyard: land on something stealth mode killed and bring it back.
keys g; gif 1.6
PAUSE=0.15 keys Down; PAUSE=0.15 keys Down; keys Down; gif 1.4
grab graveyard && render graveyard docs/graveyard.png
keys r; gif 2.4
keys g; gif 1.2
# Everything running: tab over, find a dev server by port, kill it.
keys Tab; gif 2.0
for _ in 1 2 3 4 5 6 7; do PAUSE=0.05 keys Down; done
sleep 6 # let the status line clear
grab everything && render everything docs/everything.png
keys /; keys ':5174'; keys Enter; gif 1.8
keys x; gif 1.4
keys y
sleep 1.3
gif 1.8
keys Escape; keys Tab; gif 1.0
keys S; gif 2.2
keys y; gif 1.6
keys m; gif 2.4
# ffmpeg's concat demuxer ignores the last duration unless the frame repeats.
tail -2 "$tmp/frames.txt" | head -1 >>"$tmp/frames.txt"

# Minimal mode in a small split.
start 74 16 -m --demo
keys Down
keys Space
grab minimal && render minimal docs/minimal.png

# Stealth mode transcript (illustrative output, same format the CLI prints).
P=$'\e[38;2;95;215;135m❯\e[0m '
D=$'\e[38;2;124;124;134m'
R=$'\e[0m'
G=$'\e[38;2;95;215;135m'
cat >"$tmp/stealth.ansi" <<EOF
${P}appsassin stealth install
${G}◉${R} stealth mode will now run silently at every login
${P}appsassin stealth status
${G}◉${R} running (pid 81244)
  at login: true
  log: ~/Library/Logs/appsassin.log
${P}appsassin stealth log -n 5
${D}Oct 19 09:00:12${R}  stealth mode engaged: sweep every 5m0s, threshold 70, flags runaways (≥90% CPU or ≥4G and growing, for 10m) without killing them
${D}Oct 19 09:05:13${R}  killed orphan pid=48213 "next dev" score=97 mem=694M why=[orphaned (parent exited), dev runtime, listening :3000] (terminated)
${D}Oct 19 09:05:13${R}  killed helper pid=31877 "Slack Helper (Renderer)" score=85 mem=388M why=[outlived Slack, idle] (terminated)
${D}Oct 19 15:10:41${R}  killed app pid=1450 "Spotify" score=85 mem=611M why=[no windows, unused 6h, idle] (quit)
${D}Oct 19 15:10:44${R}  failed app pid=9031 "Preview" score=75 mem=74M why=[no windows, unused 6h]: app is still open (unsaved changes?)
${P}
EOF
render stealth docs/stealth.png

# Animated demo.
ffmpeg -loglevel error -y -f concat -safe 0 -i "$tmp/frames.txt" \
  -vf "scale=1100:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4" \
  docs/demo.gif
ls -lh docs
