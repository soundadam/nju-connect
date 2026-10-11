# frozen_string_literal: true

# release workflow 的 homebrew job 把 @VERSION@ 与各平台的 SHA-256 填好，写到 soundadam/homebrew-tap 的 Casks/nju-connect.rb
cask "nju-connect" do
  version "@VERSION@"

  on_macos do
    sha256 "@MACOS_SHA256@"

    url "https://github.com/soundadam/nju-connect/releases/download/v#{version}/nju-connect-#{version}-macos-universal.zip"

    depends_on macos: :ventura

    app "nju-connect.app"
    binary "#{appdir}/nju-connect.app/Contents/Helpers/nju-connect"

    # ad-hoc 签名、未公证：去掉隔离属性，与自更新换上的文件一样不被 Gatekeeper 拦下
    postflight_steps do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{appdir}}/nju-connect.app"]
    end

    uninstall quit: "io.github.soundadam.nju-connect.menu"

    zap trash: [
      "~/Library/Application Support/nju-connect",
      "~/Library/Caches/io.github.soundadam.nju-connect.menu",
      "~/Library/Preferences/io.github.soundadam.nju-connect.menu.plist",
    ]
  end
  on_linux do
    on_arm do
      sha256 "@LINUX_AARCH64_SHA256@"

      url "https://github.com/soundadam/nju-connect/releases/download/v#{version}/nju-connect-#{version}-linux-aarch64.tar.gz"

      binary "nju-connect-#{version}-linux-aarch64/nju-connect"
    end
    on_intel do
      sha256 "@LINUX_X86_64_SHA256@"

      url "https://github.com/soundadam/nju-connect/releases/download/v#{version}/nju-connect-#{version}-linux-x86_64.tar.gz"

      binary "nju-connect-#{version}-linux-x86_64/nju-connect"
    end

    zap trash: "~/.config/nju-connect"
  end

  name "nju-connect"
  desc "NJU campus VPN client with a local SOCKS5 proxy"
  homepage "https://soundadam.com/projects/nju-connect/"

  livecheck do
    url :url
    strategy :github_latest
  end

  # 校园测速用的 LibreSpeed 不随 nju-connect 发布，见 README 的 Campus speed test
  depends_on formula: "soundadam/tap/librespeed-cli-nju-connect"
end
