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

component_build_root=${stage_root}/component-build
"${project_root}/scripts/build_speedtest_component.sh" "${component_build_root}"
component_version=$(plutil -extract component_version raw -o - "${component_build_root}/component-manifest.json")

license_dir=${stage_root}/third_party_licenses
mkdir -p "${license_dir}"
cp "${project_root}/packaging/licenses/librespeed-cli-LGPL-3.0.txt" \
  "${license_dir}/librespeed-cli-LGPL-3.0.txt"
typeset -A copied_license_paths
while IFS='|' read -r module_path module_dir; do
  [[ -n ${module_path} && -d ${module_dir} ]] || continue
  [[ ${module_path} != github.com/soundadam/soundconnect ]] || continue
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
  go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' ./cmd/soundconnect | \
    awk 'NF' | LC_ALL=C sort -u
)
if (( ${#copied_license_paths} == 0 )); then
  print -u2 'release packaging found no linked third-party license texts'
  exit 1
fi

for arch in amd64 arm64; do
  artifact="soundconnect_${release_version}_darwin_${arch}"
  stage_dir=${stage_root}/${artifact}
  mkdir -p "${stage_dir}"
  (
    cd "${project_root}"
    CGO_ENABLED=1 GOOS=darwin GOARCH=${arch} go build \
      -trimpath \
      -buildvcs=true \
      -ldflags "-s -w -X main.version=${release_version}" \
      -o "${stage_dir}/soundconnect" \
      ./cmd/soundconnect
  )
  cp "${project_root}/LICENSE" "${project_root}/THIRD_PARTY_NOTICES" "${stage_dir}/"
  cp -R "${license_dir}" "${stage_dir}/"
  mkdir -p "${stage_dir}/speedtest_component_source" \
    "${stage_dir}/campus-speed/${component_version}/${arch}"
  cp -R "${component_build_root}/source/." "${stage_dir}/speedtest_component_source/"
  install -m 0755 "${component_build_root}/librespeed-cli-${arch}" \
    "${stage_dir}/campus-speed/${component_version}/${arch}/librespeed-cli"
  COPYFILE_DISABLE=1 tar -czf "${dist_dir}/${artifact}.tar.gz" -C "${stage_root}" "${artifact}"
done

(
  cd "${dist_dir}"
  shasum -a 256 ./*.tar.gz > SHA256SUMS
)

print "version: ${release_version}"
print "commit: ${commit}"
print "artifacts: ${dist_dir}"
