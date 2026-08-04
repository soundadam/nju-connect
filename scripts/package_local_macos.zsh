#!/bin/zsh
set -euo pipefail

usage() {
  print -u2 -- "usage: $0 VERSION [LOCAL_TAP_ROOT]"
  exit 64
}

[[ $# -ge 1 && $# -le 2 ]] || usage

readonly version="$1"
readonly repo_root="${0:A:h:h}"
readonly local_tap_root="${2:-${HOME}/workspaces/soundadam/homebrew-local}"
readonly archive="${repo_root}/dist/${version}/soundconnect-${version}-macos-universal.zip"
readonly cask_output="${local_tap_root}/Casks/soundconnect.rb"

[[ -d "${local_tap_root}/.git" ]] || {
  print -u2 -- "local tap is not a Git repository: ${local_tap_root}"
  exit 66
}

SOUNDCONNECT_ALLOW_DIRTY=1 "${repo_root}/scripts/package_macos_release.zsh" "$version"
readonly sha256="$(shasum -a 256 "$archive" | awk '{print $1}')"
"${repo_root}/scripts/render_homebrew_cask.zsh" \
  "$version" "$sha256" "$cask_output" "file://${archive}"

print -- "archive=${archive}"
print -- "cask=${cask_output}"
print -- "sha256=${sha256}"
