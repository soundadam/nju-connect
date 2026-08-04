#!/bin/zsh
set -euo pipefail

usage() {
  print -u2 -- "usage: $0 VERSION [OUTPUT_DIRECTORY]"
  exit 64
}

[[ $# -ge 1 && $# -le 2 ]] || usage

readonly version="$1"
readonly repo_root="${0:A:h:h}"
readonly output_root="${2:-${repo_root}/dist}"
readonly app_name="soundconnect.app"
readonly archive_name="soundconnect-${version}-macos-universal.zip"
readonly stage_root="${repo_root}/.stage/macos-release-${version}"
readonly app_root="${stage_root}/${app_name}"
readonly contents_root="${app_root}/Contents"
readonly release_dir="${output_root}/${version}"
readonly allow_dirty="${SOUNDCONNECT_ALLOW_DIRTY:-0}"

[[ "$version" == <->.<->.<-> ]] || {
  print -u2 -- "version must use MAJOR.MINOR.PATCH"
  exit 64
}

for tool in go swift ditto lipo codesign plutil shasum git; do
  command -v "$tool" >/dev/null || {
    print -u2 -- "required tool not found: $tool"
    exit 69
  }
done

[[ -f "${repo_root}/packaging/macos/Info.plist" ]] || {
  print -u2 -- "missing packaging/macos/Info.plist"
  exit 66
}
[[ -f "${repo_root}/packaging/macos/AppIcon.icns" ]] || {
  print -u2 -- "missing packaging/macos/AppIcon.icns"
  exit 66
}
cd "$repo_root"

typeset source_dirty=false
if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  [[ "$allow_dirty" == 1 ]] || {
    print -u2 -- "macOS release packaging requires a clean worktree"
    exit 65
  }
  source_dirty=true
fi

rm -rf -- "$stage_root"
mkdir -p -- \
  "${contents_root}/MacOS" \
  "${contents_root}/Helpers" \
  "${contents_root}/Resources/third_party_licenses" \
  "$release_dir"

swift build \
  --package-path "${repo_root}/macos" \
  -c release \
  --arch arm64 \
  --arch x86_64 \
  --product soundconnect-menu

install -m 0755 \
  "${repo_root}/macos/.build/apple/Products/Release/soundconnect-menu" \
  "${contents_root}/MacOS/soundconnect-menu"

for arch in arm64 amd64; do
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${version}" \
    -o "${stage_root}/soundconnect-${arch}" \
    ./cmd/soundconnect
done
lipo -create \
  "${stage_root}/soundconnect-arm64" \
  "${stage_root}/soundconnect-amd64" \
  -output "${contents_root}/Helpers/soundconnect"
chmod 0755 "${contents_root}/Helpers/soundconnect"

install -m 0644 "${repo_root}/packaging/macos/Info.plist" "${contents_root}/Info.plist"
install -m 0644 "${repo_root}/packaging/macos/AppIcon.icns" \
  "${contents_root}/Resources/AppIcon.icns"
plutil -replace CFBundleShortVersionString -string "$version" "${contents_root}/Info.plist"
plutil -replace CFBundleVersion -string "${version//./}" "${contents_root}/Info.plist"

install -m 0644 "${repo_root}/LICENSE" "${contents_root}/Resources/LICENSE"
install -m 0644 "${repo_root}/THIRD_PARTY_NOTICES" "${contents_root}/Resources/THIRD_PARTY_NOTICES"
install -m 0644 "${repo_root}/packaging/licenses/librespeed-cli-LGPL-3.0.txt" \
  "${contents_root}/Resources/third_party_licenses/librespeed-cli-LGPL-3.0.txt"

typeset -A copied_license_paths
while IFS='|' read -r module_path module_dir; do
  [[ -n "$module_path" && -d "$module_dir" ]] || continue
  [[ "$module_path" != "github.com/soundadam/soundconnect" ]] || continue
  while IFS= read -r license_path; do
    relative_path="${license_path#${module_dir}/}"
    destination_name="${module_path//\//_}__${relative_path//\//_}"
    [[ -z "${copied_license_paths[$destination_name]-}" ]] || continue
    install -m 0644 "$license_path" \
      "${contents_root}/Resources/third_party_licenses/${destination_name}"
    copied_license_paths[$destination_name]=1
  done < <(find "$module_dir" -maxdepth 2 -type f \
    \( -iname 'LICENSE*' -o -iname 'NOTICE*' \) -print | LC_ALL=C sort)
done < <(
  cd "$repo_root"
  go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' ./cmd/soundconnect | \
    awk 'NF' | LC_ALL=C sort -u
)

(( ${#copied_license_paths} > 0 )) || {
  print -u2 -- "no linked third-party license texts were collected"
  exit 65
}

codesign --force --sign - --timestamp=none "${contents_root}/Helpers/soundconnect"
codesign --force --sign - --timestamp=none "${contents_root}/MacOS/soundconnect-menu"
codesign --force --sign - --timestamp=none "$app_root"

codesign --verify --deep --strict --verbose=2 "$app_root"
plutil -lint "${contents_root}/Info.plist"
[[ "$("${contents_root}/Helpers/soundconnect" version)" == "soundconnect ${version}" ]]
file "${contents_root}/MacOS/soundconnect-menu" | grep -q 'universal binary'
file "${contents_root}/Helpers/soundconnect" | grep -q 'universal binary'

rm -f -- "${release_dir}/${archive_name}"
COPYFILE_DISABLE=1 ditto -c -k --keepParent "$app_root" "${release_dir}/${archive_name}"

readonly source_commit="$(git -C "$repo_root" rev-parse HEAD)"
readonly archive_sha256="$(shasum -a 256 "${release_dir}/${archive_name}" | awk '{print $1}')"
readonly manifest_path="${release_dir}/soundconnect-${version}-manifest.txt"
readonly release_notes_path="${release_dir}/soundconnect-${version}-release-notes.md"

{
  print -- "product=soundconnect"
  print -- "version=${version}"
  print -- "source_repository=https://github.com/soundadam/soundconnect"
  print -- "source_commit=${source_commit}"
  print -- "source_dirty=${source_dirty}"
  print -- "asset=${archive_name}"
  print -- "sha256=${archive_sha256}"
  print -- "architectures=arm64,x86_64"
  print -- "minimum_macos=13.0"
  print -- "signature=ad-hoc"
  print -- "notarized=false"
  print -- "ui_backend=native-cli-control"
  print -- "campus_speed_helper=external-homebrew-formula"
  print -- "campus_speed_helper_formula=librespeed-cli-soundconnect"
  print -- "license=proprietary; authorized users only"
} > "$manifest_path"

{
  print -- "# soundconnect ${version} macOS release"
  print
  print -- "This release contains a universal macOS menu-bar app and the bundled native CLI."
  print
  print -- "Release boundary:"
  print
  print -- "- VPN setup, background runtime control, status, traffic, and campus speed testing use the bundled CLI."
  print -- "- Campus speed testing requires the separate librespeed-cli-soundconnect Homebrew Formula."
  print -- "- The app and CLI are ad-hoc signed and are not Apple-notarized."
  print -- "- The source repository is private and the software is proprietary; public download does not grant a license."
  print -- "- The Cask does not remove quarantine or bypass Gatekeeper."
  print
  print -- "Provenance:"
  print
  print -- "- Source repository: https://github.com/soundadam/soundconnect"
  print -- "- Source commit: \`${source_commit}\`"
  print -- "- Dirty source tree: \`${source_dirty}\`"
  print -- "- Asset SHA-256: \`${archive_sha256}\`"
  print -- "- Architectures: arm64 and x86_64"
  print -- "- Minimum macOS: 13 Ventura"
} > "$release_notes_path"

print -- "archive=${release_dir}/${archive_name}"
print -- "manifest=${manifest_path}"
print -- "release_notes=${release_notes_path}"
print -- "sha256=${archive_sha256}"
print -- "source_commit=${source_commit}"
