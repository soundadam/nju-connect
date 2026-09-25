#!/bin/zsh
set -euo pipefail

usage() {
  print -u2 -- "usage: $0 VERSION SHA256 OUTPUT [URL [HOMEPAGE]]"
  exit 64
}

[[ $# -ge 3 && $# -le 5 ]] || usage

readonly version="${1#v}"
readonly sha256="$2"
readonly output="$3"
readonly template="${0:A:h:h}/packaging/Casks/soundconnect.rb.in"
readonly url="${4:-https://github.com/soundadam/homebrew-dist/releases/download/soundconnect-v${version}/soundconnect-${version}-macos-universal.zip}"
readonly homepage="${5:-https://github.com/soundadam/homebrew-dist/releases/tag/soundconnect-v${version}}"
source "${0:A:h}/versioning.zsh"

soundconnect_parse_version "$version"
[[ ${#sha256} -eq 64 && -z "${sha256//[0-9a-f]/}" ]] || {
  print -u2 -- "sha256 must be 64 lowercase hexadecimal characters"
  exit 64
}
[[ -f "$template" ]] || {
  print -u2 -- "missing template: $template"
  exit 66
}
[[ "$url" == https://* || "$url" == file:///* ]] || {
  print -u2 -- "URL must use https:// or an absolute file:// URL"
  exit 64
}

typeset escaped_url="${url//\\/\\\\}"
escaped_url="${escaped_url//&/\\&}"
escaped_url="${escaped_url//|/\\|}"
typeset escaped_homepage="${homepage//\\/\\\\}"
escaped_homepage="${escaped_homepage//&/\\&}"
escaped_homepage="${escaped_homepage//|/\\|}"

mkdir -p -- "${output:h}"
sed \
  -e "s/__VERSION__/${version}/g" \
  -e "s/__SHA256__/${sha256}/g" \
  -e "s|__URL__|${escaped_url}|g" \
  -e "s|__HOMEPAGE__|${escaped_homepage}|g" \
  "$template" > "$output"

print -- "$output"
