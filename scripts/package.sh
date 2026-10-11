#!/usr/bin/env bash
# 构建 release 产物到 dist/，名字与内容照 appkit 的约定
# （https://github.com/soundadam/appkit#发布产物）。
#   macOS: nju-connect-<ver>-macos-universal.zip，ad-hoc 签名的 nju-connect.app，
#          命令行与 aTrust 登录窗口在 Contents/Helpers/
#   Linux: nju-connect-<ver>-linux-<arch>.tar.gz 与 nju-connect-<ver>-windows-<arch>.zip，
#          交叉编译，CGO_ENABLED=0
# 用法：scripts/package.sh <version>（不带 v；release workflow 传 tag 去掉 v 之后的部分）
set -euo pipefail
cd "$(dirname "$0")/.."

version=${1:?usage: scripts/package.sh <version>}
semver='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
[[ $version =~ $semver ]] || { echo "not a version: $version" >&2; exit 64; }

extras=(README.md LICENSE THIRD_PARTY_NOTICES)

rm -rf dist
mkdir -p dist
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

# 链进 GOOS/GOARCH 这份可执行文件的每个模块的许可证文本，与 LibreSpeed 的一起放进 $1
collect_licenses() {
    local into=$1 goos=$2 goarch=$3 module dir license
    mkdir -p "$into"
    cp packaging/licenses/librespeed-cli-LGPL-3.0.txt "$into/"
    GOOS=$goos GOARCH=$goarch go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' ./cmd/nju-connect |
        awk 'NF' | LC_ALL=C sort -u |
        while IFS='|' read -r module dir; do
            [[ $module != github.com/soundadam/nju-connect && -d $dir ]] || continue
            find "$dir" -maxdepth 2 -type f \( -iname 'LICENSE*' -o -iname 'NOTICE*' \) -print | LC_ALL=C sort |
                while IFS= read -r license; do
                    rel=${license#"$dir"/}
                    dest="$into/${module//\//_}__${rel//\//_}"
                    [[ -e $dest ]] || cp "$license" "$dest"
                done
        done
    [[ -n $(find "$into" -name '*__*' -print -quit) ]] || { echo "no module license texts for $goos/$goarch" >&2; exit 1; }
}

go_build() { # <goos> <goarch> <cgo> <output>
    CGO_ENABLED=$3 GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$4" ./cmd/nju-connect
}

# CFBundleVersion 要随发布单调增大，预发布也算：MAJOR MINOR(3 位) PATCH(3 位) 渠道(alpha 1 / beta 2 / rc 3 / 正式 9) 序号(3 位)，
# 1.1.0-alpha.1 → 10010001001，1.1.0 → 10010009000。已经发出去的版本都是这样编的，换写法就比旧版小。
bundle_version() {
    local re='^([0-9]+)\.([0-9]+)\.([0-9]+)(-(alpha|beta|rc)\.([0-9]+))?$'
    [[ $1 =~ $re ]] || { echo "prerelease must be -alpha.N, -beta.N or -rc.N: $1" >&2; exit 64; }
    local channel
    case ${BASH_REMATCH[5]} in alpha) channel=1 ;; beta) channel=2 ;; rc) channel=3 ;; *) channel=9 ;; esac
    printf '%d%03d%03d%d%03d\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "$channel" "${BASH_REMATCH[6]:-0}"
}

case "$(uname -s)" in
Darwin)
    swift_products=(nju-connect-menu nju-connect-atrust-oauth-helper)
    for product in "${swift_products[@]}"; do
        swift build --package-path macos -c release --arch arm64 --arch x86_64 --product "$product"
    done
    swift_bin=macos/.build/apple/Products/Release

    app="$stage/nju-connect.app"
    contents="$app/Contents"
    mkdir -p "$contents/MacOS" "$contents/Helpers" "$contents/Resources"
    install -m 0755 "$swift_bin/nju-connect-menu" "$contents/MacOS/"
    install -m 0755 "$swift_bin/nju-connect-atrust-oauth-helper" "$contents/Helpers/"
    for arch in arm64 amd64; do
        go_build darwin "$arch" 1 "$stage/nju-connect-$arch"
    done
    lipo -create -output "$contents/Helpers/nju-connect" "$stage"/nju-connect-{arm64,amd64}
    bundle=$(bundle_version "$version")
    sed -e "s/@VERSION@/$version/g" -e "s/@BUNDLE_VERSION@/$bundle/g" \
        packaging/macos/Info.plist >"$contents/Info.plist"
    plutil -lint "$contents/Info.plist"
    cp packaging/macos/AppIcon.icns "$contents/Resources/"
    # 许可证也放进 bundle：Cask 只装 .app，自更新也只换 .app
    cp LICENSE THIRD_PARTY_NOTICES "$contents/Resources/"
    collect_licenses "$contents/Resources/third_party_licenses" darwin arm64

    # 没有开发者证书时只能 ad-hoc；Apple Silicon 不接受未签名的 bundle
    for exe in "$contents"/Helpers/* "$contents/MacOS/nju-connect-menu"; do
        codesign --force --sign - --timestamp=none "$exe"
    done
    codesign --force --sign - --timestamp=none "$app"
    codesign --verify --deep --strict "$app"
    for exe in "$contents"/Helpers/* "$contents/MacOS/nju-connect-menu"; do
        lipo "$exe" -verify_arch arm64 x86_64
    done
    test "$("$contents/Helpers/nju-connect" --version)" = "nju-connect $version"

    cp "${extras[@]}" "$stage/"
    cp -R "$contents/Resources/third_party_licenses" "$stage/"
    rm "$stage"/nju-connect-{arm64,amd64}
    (cd "$stage" && ditto -c -k --sequesterRsrc . "$OLDPWD/dist/nju-connect-$version-macos-universal.zip")
    ;;
Linux)
    for platform in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
        goos=${platform%/*} goarch=${platform#*/}
        arch=$([[ $goarch == amd64 ]] && echo x86_64 || echo aarch64)
        name="nju-connect-$version-$goos-$arch"
        exe=nju-connect$([[ $goos == windows ]] && echo .exe || true)
        mkdir -p "$stage/$name"
        go_build "$goos" "$goarch" 0 "$stage/$name/$exe"
        cp "${extras[@]}" "$stage/$name/"
        collect_licenses "$stage/$name/third_party_licenses" "$goos" "$goarch"
        if [[ $goos == windows ]]; then
            (cd "$stage" && zip -qrX "$OLDPWD/dist/$name.zip" "$name")
        else
            tar -C "$stage" -czf "dist/$name.tar.gz" "$name"
        fi
    done
    host="$stage/nju-connect-$version-linux-$(uname -m)/nju-connect"
    if [[ -x $host ]]; then
        test "$("$host" --version)" = "nju-connect $version"
    fi
    ;;
*)
    echo "unsupported platform: $(uname -s)" >&2
    exit 1
    ;;
esac

ls -l dist
