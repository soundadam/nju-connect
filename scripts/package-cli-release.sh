#!/bin/zsh

set -euo pipefail

project_root=${0:A:h:h}
version=${1:-}

if [[ ! ${version} =~ '^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$' ]]; then
  print -u2 'usage: scripts/package-cli-release.sh vMAJOR.MINOR.PATCH'
  exit 2
fi
if [[ -n $(git -C "${project_root}" status --porcelain) ]]; then
  print -u2 'release packaging requires a clean worktree'
  exit 1
fi

release_version=${version#v}
commit=$(git -C "${project_root}" rev-parse HEAD)
dist_dir=${project_root}/dist
stage_root=${project_root}/.stage/cli-release

rm -rf "${dist_dir}" "${stage_root}"
mkdir -p "${dist_dir}" "${stage_root}"

license_dir=${stage_root}/third_party_licenses
mkdir -p "${license_dir}"
cp "${project_root}/packaging/licenses/librespeed-cli-LGPL-3.0.txt" \
  "${license_dir}/librespeed-cli-LGPL-3.0.txt"
typeset -A copied_license_paths
while IFS='|' read -r module_path module_dir; do
  [[ -n ${module_path} && -d ${module_dir} ]] || continue
  [[ ${module_path} != github.com/soundadam/nju-connect ]] || continue
  while IFS= read -r license_path; do
    relative_path=${license_path#${module_dir}/}
    destination_name=${module_path//\//_}__${relative_path//\//_}
    [[ -z ${copied_license_paths[${destination_name}]-} ]] || continue
    cp "${license_path}" "${license_dir}/${destination_name}"
    copied_license_paths[${destination_name}]=1
  done < <(find "${module_dir}" -maxdepth 2 -type f \
    \( -iname 'LICENSE*' -o -iname 'NOTICE*' \) -print | LC_ALL=C sort)
done < <(
  cd "${project_root}"
  go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' ./cmd/nju-connect | \
    awk 'NF' | LC_ALL=C sort -u
)
if (( ${#copied_license_paths} == 0 )); then
  print -u2 'release packaging found no linked third-party license texts'
  exit 1
fi

for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os=${platform%/*}
  arch=${platform#*/}
  artifact="nju-connect_${release_version}_${os}_${arch}"
  stage_dir=${stage_root}/${artifact}
  binary=nju-connect
  [[ ${os} != windows ]] || binary=nju-connect.exe
  cgo=0
  [[ ${os} != darwin ]] || cgo=1
  mkdir -p "${stage_dir}"
  (
    cd "${project_root}"
    CGO_ENABLED=${cgo} GOOS=${os} GOARCH=${arch} go build \
      -trimpath \
      -buildvcs=true \
      -ldflags "-s -w -X main.version=${release_version}" \
      -o "${stage_dir}/${binary}" \
      ./cmd/nju-connect
  )
  cp "${project_root}/LICENSE" "${project_root}/THIRD_PARTY_NOTICES" "${stage_dir}/"
  cp -R "${license_dir}" "${stage_dir}/"
  if [[ ${os} == windows ]]; then
    (cd "${stage_root}" && zip -qrX "${dist_dir}/${artifact}.zip" "${artifact}")
  else
    COPYFILE_DISABLE=1 tar -czf "${dist_dir}/${artifact}.tar.gz" -C "${stage_root}" "${artifact}"
  fi
done

(
  cd "${dist_dir}"
  shasum -a 256 ./*.tar.gz ./*.zip > SHA256SUMS
)

print "version: ${release_version}"
print "commit: ${commit}"
print "artifacts: ${dist_dir}"
