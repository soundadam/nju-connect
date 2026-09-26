#!/bin/zsh
set -euo pipefail

readonly repo_root="${0:A:h:h}"
source "${repo_root}/scripts/versioning.zsh"

expect() {
  local want="$1"
  shift
  local got="$($@)"
  [[ "$got" == "$want" ]] || {
    print -u2 -- "expected ${want}, got ${got}"
    exit 1
  }
}

expect "1.1.0-alpha.1" nju_connect_next_version "1.0.1" alpha
expect "1.0.2" nju_connect_next_version "1.0.1" stable
expect "1.1.0-alpha.2" nju_connect_next_version "1.1.0-alpha.1" alpha
expect "1.1.0-beta.1" nju_connect_next_version "v1.1.0-alpha.9" beta
expect "1.1.0-rc.1" nju_connect_next_version "1.1.0-beta.4" rc
expect "1.1.0" nju_connect_next_version "1.1.0-rc.3" stable
expect "10010009000" nju_connect_bundle_version "1.1.0"
expect "10010001001" nju_connect_bundle_version "1.1.0-alpha.1"

if nju_connect_next_version "1.1.0-beta.1" alpha >/dev/null 2>&1; then
  print -u2 -- "backward prerelease transition was accepted"
  exit 1
fi

print -- "versioning tests passed"
