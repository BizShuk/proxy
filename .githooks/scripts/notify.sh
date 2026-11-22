#!/bin/bash
#
# scripts/notify.sh: pre-push 背景任務共用的 macOS 通知 (格式與 `inf deploy` 相同)。
#
# 用法: scripts/notify.sh <repo-dir> <message>
#   標題: <repo 名> <路徑 (~ 縮寫)>
#   內文: <message>, 例如 `Installed locally`
# terminal-notifier 優先 (全部 inf 通知共用 group `inf`, 新通知取代舊的), 否則 osascript; 非 macOS 略過。

set -uo pipefail

dir="${1:?usage: notify.sh <repo-dir> <message>}"
message="${2:?usage: notify.sh <repo-dir> <message>}"
[[ "$(uname -s)" == "Darwin" ]] || exit 0

title="$(basename "$dir") ${dir/#"$HOME"/\~}"
if command -v terminal-notifier >/dev/null 2>&1; then
	terminal-notifier -title "$title" -message "$message" -group "inf" >/dev/null 2>&1
else
	osascript -e "display notification \"${message//\"/\\\"}\" with title \"${title//\"/\\\"}\"" >/dev/null 2>&1
fi
exit 0
