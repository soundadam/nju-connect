#!/bin/zsh
set -euo pipefail

readonly repo_root="${0:A:h:h}"
source "${repo_root}/scripts/versioning.zsh"

readonly tap_name="${NJU_CONNECT_LOCAL_TAP:-soundadam/local}"
readonly cask_name="${NJU_CONNECT_CASK:-nju-connect}"
readonly channel="${CHANNEL:-alpha}"
readonly requested_version="${VERSION:-}"
readonly install_update="${INSTALL_UPDATE:-1}"
readonly commit_tap="${COMMIT_TAP:-1}"
readonly push_tap="${PUSH_TAP:-0}"
readonly app_path="${NJU_CONNECT_APP_PATH:-/Applications/nju-connect.app}"

for tool in brew git make plutil shasum codesign xattr; do
  command -v "$tool" >/dev/null || {
    print -u2 -- "required tool not found: $tool"
    exit 69
  }
done

readonly tap_root="$(brew --repo "$tap_name")"
[[ -d "${tap_root}/.git" || -f "${tap_root}/.git" ]] || {
  print -u2 -- "tap is not a Git repository: ${tap_root}"
  exit 66
}

typeset current_version
if [[ -f "${app_path}/Contents/Info.plist" ]]; then
  current_version="$(plutil -extract CFBundleShortVersionString raw "${app_path}/Contents/Info.plist")"
else
  current_version="$(plutil -extract CFBundleShortVersionString raw "${repo_root}/packaging/macos/Info.plist")"
fi

typeset version
if [[ -n "$requested_version" ]]; then
  nju_connect_parse_version "$requested_version"
  version="$NJU_CONNECT_VERSION"
else
  version="$(nju_connect_next_version "$current_version" "$channel")"
fi

print -- "current_version=${current_version}"
print -- "next_version=${version}"
print -- "tap=${tap_name}"
print -- "tap_root=${tap_root}"

make -C "$repo_root" release-check
"${repo_root}/scripts/package_local_macos.zsh" "$version" "$tap_root"
git -C "$tap_root" diff --check

if [[ "$commit_tap" == 1 ]] && ! git -C "$tap_root" diff --quiet -- "Casks/${cask_name}.rb"; then
  git -C "$tap_root" add -- "Casks/${cask_name}.rb"
  git -C "$tap_root" commit -m "${cask_name}: update to ${version}"
fi

if [[ "$install_update" == 1 ]]; then
  HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_FROM_API=1 \
    brew reinstall --cask "${tap_name}/${cask_name}"
  xattr -dr com.apple.quarantine "$app_path"

  readonly installed_version="$(plutil -extract CFBundleShortVersionString raw "${app_path}/Contents/Info.plist")"
  readonly helper_version="$("${app_path}/Contents/Helpers/nju-connect" version)"
  readonly staged_helper="${repo_root}/.stage/macos-release-${version}/nju-connect.app/Contents/Helpers/nju-connect"
  [[ "$installed_version" == "$version" ]]
  [[ "$helper_version" == "nju-connect ${version}" ]]
  [[ "$(shasum -a 256 "$staged_helper" | awk '{print $1}')" == \
     "$(shasum -a 256 "${app_path}/Contents/Helpers/nju-connect" | awk '{print $1}')" ]]
  codesign --verify --deep --strict --verbose=2 "$app_path"
fi

if [[ "$push_tap" == 1 ]]; then
  git -C "$tap_root" push
fi

print -- "updated_version=${version}"
print -- "tap_commit=$(git -C "$tap_root" rev-parse HEAD)"
print -- "installed=${install_update}"
print -- "pushed=${push_tap}"
