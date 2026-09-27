#!/bin/zsh
set -euo pipefail

readonly mode="${1:-run}"
[[ "$mode" == "run" || "$mode" == "--build-only" ]] || {
  print -u2 -- "usage: $0 [--build-only]"
  exit 64
}

readonly repo_root="${0:A:h:h}"
cd "$repo_root"

identity="${NJU_CONNECT_CODESIGN_IDENTITY:-}"
if [[ -z "$identity" ]]; then
  identity="$(security find-identity -v -p codesigning | awk '/"Apple Development:/{print $2; exit}')"
fi
[[ -n "$identity" ]] || {
  print -u2 -- "no Apple Development signing identity is available"
  exit 78
}

mkdir -p bin
go build -o bin/nju-connect ./cmd/nju-connect
swift build --package-path macos --product nju-connect-menu
swift build --package-path macos --product nju-connect-atrust-oauth-helper

readonly swift_bin="$(swift build --package-path macos --show-bin-path)"
readonly menu_binary="${swift_bin}/nju-connect-menu"
readonly oauth_helper="${swift_bin}/nju-connect-atrust-oauth-helper"

codesign --force --sign "$identity" --timestamp=none \
  --identifier com.soundadam.nju-connect.cli bin/nju-connect
codesign --force --sign "$identity" --timestamp=none \
  --identifier com.soundadam.nju-connect.oauth-helper "$oauth_helper"
codesign --force --sign "$identity" --timestamp=none \
  --identifier com.soundadam.nju-connect.menu "$menu_binary"

for artifact in bin/nju-connect "$oauth_helper" "$menu_binary"; do
  # Remove only quarantine inherited by copied development artifacts. Keep
  # provenance and unrelated extended attributes intact.
  xattr -d com.apple.quarantine "$artifact" 2>/dev/null || true
  codesign --verify --strict --verbose=1 "$artifact"
done

if [[ "$mode" == "--build-only" ]]; then
  print -- "signed_cli=${repo_root}/bin/nju-connect"
  print -- "signed_menu=${menu_binary}"
  print -- "signed_oauth_helper=${oauth_helper}"
  exit 0
fi

exec env \
  NJU_CONNECT_HELPER="${repo_root}/bin/nju-connect" \
  NJU_CONNECT_ATRUST_OAUTH_HELPER="$oauth_helper" \
  NJU_CONNECT_UI_LANGUAGE="${NJU_CONNECT_UI_LANGUAGE:-en}" \
  "$menu_binary"
