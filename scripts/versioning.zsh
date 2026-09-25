#!/bin/zsh

# sing-box-style release versions:
#   1.0.2-alpha.1 -> 1.0.2-beta.1 -> 1.0.2-rc.1 -> 1.0.2
# Git tags add a leading v; embedded product versions do not.

soundconnect_parse_version() {
  local input="${1#v}"
  if [[ ! "$input" =~ '^([0-9]+)\.([0-9]+)\.([0-9]+)(-(alpha|beta|rc)\.([0-9]+))?$' ]]; then
    print -u2 -- "invalid version: ${1}"
    return 64
  fi
  typeset -g SOUNDCONNECT_VERSION="$input"
  typeset -g SOUNDCONNECT_VERSION_MAJOR="${match[1]}"
  typeset -g SOUNDCONNECT_VERSION_MINOR="${match[2]}"
  typeset -g SOUNDCONNECT_VERSION_PATCH="${match[3]}"
  typeset -g SOUNDCONNECT_VERSION_CHANNEL="${match[5]}"
  typeset -g SOUNDCONNECT_VERSION_SEQUENCE="${match[6]}"
}

soundconnect_next_version() {
  local current="${1#v}"
  local requested_channel="${2:-alpha}"
  soundconnect_parse_version "$current" || return
  [[ "$requested_channel" == alpha || "$requested_channel" == beta || \
     "$requested_channel" == rc || "$requested_channel" == stable ]] || {
    print -u2 -- "channel must be alpha, beta, rc, or stable"
    return 64
  }

  local major="$SOUNDCONNECT_VERSION_MAJOR"
  local minor="$SOUNDCONNECT_VERSION_MINOR"
  local patch="$SOUNDCONNECT_VERSION_PATCH"
  local current_channel="$SOUNDCONNECT_VERSION_CHANNEL"
  local current_sequence="${SOUNDCONNECT_VERSION_SEQUENCE:-0}"

  if [[ -z "$current_channel" ]]; then
    if [[ "$requested_channel" == stable ]]; then
      (( patch += 1 ))
      print -- "${major}.${minor}.${patch}"
    else
      (( minor += 1 ))
      print -- "${major}.${minor}.0-${requested_channel}.1"
    fi
    return
  fi

  if [[ "$requested_channel" == stable ]]; then
    print -- "${major}.${minor}.${patch}"
  elif [[ "$requested_channel" == "$current_channel" ]]; then
    print -- "${major}.${minor}.${patch}-${requested_channel}.$(( current_sequence + 1 ))"
  else
    local current_rank requested_rank
    case "$current_channel" in
      alpha) current_rank=1 ;;
      beta) current_rank=2 ;;
      rc) current_rank=3 ;;
    esac
    case "$requested_channel" in
      alpha) requested_rank=1 ;;
      beta) requested_rank=2 ;;
      rc) requested_rank=3 ;;
    esac
    (( requested_rank > current_rank )) || {
      print -u2 -- "cannot move version channel backward from ${current_channel} to ${requested_channel}"
      return 64
    }
    print -- "${major}.${minor}.${patch}-${requested_channel}.1"
  fi
}

soundconnect_bundle_version() {
  soundconnect_parse_version "$1" || return
  local channel_code sequence
  case "$SOUNDCONNECT_VERSION_CHANNEL" in
    alpha) channel_code=1 ;;
    beta) channel_code=2 ;;
    rc) channel_code=3 ;;
    "") channel_code=9 ;;
  esac
  sequence="${SOUNDCONNECT_VERSION_SEQUENCE:-0}"
  printf '%d%03d%03d%d%03d\n' \
    "$SOUNDCONNECT_VERSION_MAJOR" \
    "$SOUNDCONNECT_VERSION_MINOR" \
    "$SOUNDCONNECT_VERSION_PATCH" \
    "$channel_code" \
    "$sequence"
}
