#!/bin/zsh
set -euo pipefail

usage() {
  print -u2 -- "usage: $0 VERSION SHA256 OUTPUT"
  exit 64
}

[[ $# -eq 3 ]] || usage

readonly version="$1"
readonly sha256="$2"
readonly output="$3"
readonly template="${0:A:h:h}/packaging/Casks/soundconnect.rb.in"

[[ "$version" == <->.<->.<-> ]] || {
  print -u2 -- "version must use MAJOR.MINOR.PATCH"
  exit 64
}
[[ ${#sha256} -eq 64 && -z "${sha256//[0-9a-f]/}" ]] || {
  print -u2 -- "sha256 must be 64 lowercase hexadecimal characters"
  exit 64
}
[[ -f "$template" ]] || {
  print -u2 -- "missing template: $template"
  exit 66
}

mkdir -p -- "${output:h}"
sed \
  -e "s/__VERSION__/${version}/g" \
  -e "s/__SHA256__/${sha256}/g" \
  "$template" > "$output"

print -- "$output"
