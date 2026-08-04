#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=v1.0.13
SOURCE_SHA256=5ad938b61e3edc0ca95e2ccff0c06e97a69383f3cbb0243bd47b21b9865f9f55
HELPER_VERSION=v1.0.13-soundconnect.1
OUTPUT=${1:-"$ROOT/dist/speedtest-component"}
case "$OUTPUT" in
  /*) ;;
  *) OUTPUT="$ROOT/$OUTPUT" ;;
esac

for tool in curl shasum tar patch go; do
  command -v "$tool" >/dev/null || { echo "required tool not found: $tool" >&2; exit 69; }
done

work=$(mktemp -d "${TMPDIR:-/tmp}/soundconnect-speedtest.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
archive="$work/librespeed-cli.tar.gz"
curl --fail --location --proto '=https' --tlsv1.2 \
  --output "$archive" "https://github.com/librespeed/speedtest-cli/archive/refs/tags/${VERSION}.tar.gz"
actual=$(shasum -a 256 "$archive" | awk '{print $1}')
[ "$actual" = "$SOURCE_SHA256" ] || { echo "source checksum mismatch" >&2; exit 65; }
tar -xzf "$archive" -C "$work"
source="$work/speedtest-cli-${VERSION#v}"
patch -d "$source" -p1 < "$ROOT/patches/librespeed-cli-v1.0.13-progress-json.patch"
patch -d "$source" -p1 < "$ROOT/patches/librespeed-cli-v1.0.13-socks5.patch"

mkdir -p "$OUTPUT"
mkdir -p "$OUTPUT/source"
cp "$archive" "$OUTPUT/source/librespeed-cli-v1.0.13.tar.gz"
cp "$ROOT/packaging/licenses/librespeed-cli-LGPL-3.0.txt" "$OUTPUT/source/LICENSE-LGPL-3.0.txt"
cp "$ROOT/patches/librespeed-cli-v1.0.13-progress-json.patch" \
  "$ROOT/patches/librespeed-cli-v1.0.13-socks5.patch" "$OUTPUT/source/"
for arch in arm64 amd64; do
  GOOS=darwin GOARCH=$arch CGO_ENABLED=0 GOTOOLCHAIN=go1.24.2 go build -C "$source" \
    -trimpath \
    -ldflags "-s -w -buildid= -X github.com/librespeed/speedtest-cli/defs.ProgName=librespeed-cli -X github.com/librespeed/speedtest-cli/defs.ProgVersion=$HELPER_VERSION -X github.com/librespeed/speedtest-cli/defs.BuildDate=1970-01-01T00:00:00Z" \
    -o "$OUTPUT/librespeed-cli-$arch" ./
  chmod 0700 "$OUTPUT/librespeed-cli-$arch"
done

(cd "$OUTPUT" && shasum -a 256 librespeed-cli-* > SHA256SUMS)
arm64_size=$(wc -c < "$OUTPUT/librespeed-cli-arm64" | tr -d ' ')
amd64_size=$(wc -c < "$OUTPUT/librespeed-cli-amd64" | tr -d ' ')
arm64_sha=$(shasum -a 256 "$OUTPUT/librespeed-cli-arm64" | awk '{print $1}')
amd64_sha=$(shasum -a 256 "$OUTPUT/librespeed-cli-amd64" | awk '{print $1}')
printf '{\n  "schema_version": 1,\n  "component_version": "1.0.0",\n  "helper_version": "%s",\n  "assets": {\n    "arm64": {"url": "", "size": %s, "sha256": "%s"},\n    "amd64": {"url": "", "size": %s, "sha256": "%s"}\n  }\n}\n' \
  "$HELPER_VERSION" "$arm64_size" "$arm64_sha" "$amd64_size" "$amd64_sha" \
  > "$OUTPUT/component-manifest.json"
printf 'component_version=1.0.0\nhelper_version=%s\nsource_version=%s\nsource_sha256=%s\n' \
  "$HELPER_VERSION" "$VERSION" "$SOURCE_SHA256" > "$OUTPUT/provenance.txt"
echo "speed-test component assets: $OUTPUT"
