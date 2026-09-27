# nju-connect

南京大学校园 VPN 的原生客户端：一个同时支持 EasyConnect 与 aTrust 的命令行程序，
以及 macOS 菜单栏应用。连接后在本机提供 SOCKS5 代理（默认 `127.0.0.1:1081`），
不改系统路由；密码保存在本机仅本人可读的文件里，不依赖系统钥匙串。

项目主页：<https://soundadam.github.io/nju-connect/> ·
下载：[Releases](https://github.com/soundadam/nju-connect/releases)

## 安装

macOS（包含菜单栏应用和 `nju-connect` 命令）：

```sh
brew install --cask soundadam/tap/nju-connect
```

应用未经 Apple 公证（ad-hoc 签名），第一次打开会被 Gatekeeper 拦截。确认信任这份构建后，
在「系统设置 › 隐私与安全性」里点「仍要打开」，或运行：

```sh
xattr -dr com.apple.quarantine /Applications/nju-connect.app
```

Linux / Windows 从源码构建（Go 1.25+）：

```sh
go build -o nju-connect ./cmd/nju-connect
```

然后：

```sh
nju-connect setup     # 引导式设置：后端、网关、账号、密码
nju-connect connect   # 连接；不带命令等同于 connect
```

aTrust 协议部分使用了 [mythologyli/zju-connect](https://github.com/mythologyli/zju-connect)
的客户端核心；EasyConnect 部分参考了
[lyc8503/NJUConnect](https://github.com/lyc8503/NJUConnect) 的实现。感谢两个项目的作者和贡献者。
本项目以 AGPL-3.0 发布，与南京大学、深信服均无隶属关系。

The rest of this README is developer documentation, in English.

## CLI setup

`nju-connect setup` keeps everything in one state directory,
`os.UserConfigDir()/nju-connect`: `config.toml` for non-secret settings, and
the long-lived VPN password and the aTrust session in their own owner-only
(`0600`) files next to it. nju-connect does not use the OS keyring on any
platform (see [`docs/cli-contract.md`](docs/cli-contract.md#credential-storage)).

Run in a terminal, `nju-connect setup` is a guided wizard: it asks for the
backend, the gateway, the aTrust sign-in method it discovers, the account and
the password, with the saved values as defaults. Re-running it changes only
what you answer or pass as flags; the listener, upstream proxy and TLS
settings are kept. The first `connect` without a configuration offers the
wizard too, and `nju-connect doctor` ends with the next command to run.

When the gateway rejects the saved username or password, `connect` in a
terminal asks for the password again (or a new account too), saves it, and
signs in again. Without a terminal it stops with a `credential_rejected:` line.
To fix a wrong password or account without connecting:

```sh
nju-connect account                  # show the saved account; a menu in a terminal
nju-connect account set-password     # replace only the password
nju-connect account set-username NEW # change the account, forgetting its aTrust session
nju-connect account forget --session # sign in to aTrust from scratch next time
```

Scripts can pipe the password with `setup --password-stdin` or
`account set-password --password-stdin`; piped input always gets plain line
prompts. `TERM=dumb` or `NJU_CONNECT_ACCESSIBLE=1` does the same on a
terminal, and is the fallback for a terminal that shows forms as stacked
copies instead of redrawing them in place.

Running `nju-connect` with no command is equivalent to `nju-connect connect`
and starts the native userspace runtime. Use `nju-connect dry-run` to validate
authentication and the gateway handoff without starting the dataplane.

While a foreground or detached native runtime is active, `nju-connect status`
queries its owner-only local control socket and prints only sanitized state.
Use `nju-connect status --json` for the versioned machine-readable form, and
add `--watch` to stream that snapshot once per second. A
stopped runtime is reported explicitly and returns a nonzero status; the
command does not infer liveness from a PID file or `runtime.log`.

## Protocol backends

nju-connect separates the application from the campus VPN protocol. The
EasyConnect backend is the default. The aTrust backend uses the pinned
AGPL-3.0 `mythologyli/zju-connect` client as its protocol core and runs in the
foreground (see `docs/architecture.md`).

```sh
nju-connect backends --json                 # presentation-safe backend catalog
nju-connect configure --backend atrust      # switch non-secret settings only
nju-connect setup --backend atrust --auth-type auth/psw
nju-connect logout                          # forget saved aTrust session state
```

Both backends share the one CLI-owned SOCKS5 listener (default
`127.0.0.1:1081`) and the saved VPN password. `configure` refuses to change
the profile while a runtime is active. Set an absolute
`NJU_CONNECT_CONFIG_DIR` to run against an isolated configuration directory.

## Campus speed test

`nju-connect speedtest` measures the pinned NJU campus IPv4 LibreSpeed target.
It first tries the system's direct path. If that path is unavailable, an active
nju-connect runtime may provide its owner-only loopback SOCKS5 path; otherwise
the command asks the user to connect nju-connect and retry. Results always
identify the selected path and never infer it from connection state alone.

LibreSpeed is a separate third-party executable. On macOS the Cask depends on
the `librespeed-cli-nju-connect` Formula, which builds the pinned upstream
source with the SOCKS and structured-progress patches nju-connect needs; the
app neither embeds nor downloads it. On other platforms, point
`NJU_CONNECT_LIBRESPEED_CLI` at a compatible absolute helper path. JSON and
redirected modes fail closed instead of waiting for input. Useful machine
interfaces are:

```sh
nju-connect speedtest campus --route auto --json
nju-connect speedtest component status --json
nju-connect speedtest last --json
```

The CLI retains only the latest compact result. The macOS UI separately keeps
bounded local graph samples in its own preferences so the inspector can restore
the previous curve; the client public IP is not stored.

## Packaging

```sh
make cli-release VERSION=v1.0.0          # darwin CLI archives + SHA256SUMS, clean tree only
make package-macos VERSION=1.0.0         # menu-bar app + universal CLI as a Cask ZIP
make package-macos-local VERSION=1.0.0   # same, from a dirty tree, into the local tap
```

The archives include `LICENSE`, `THIRD_PARTY_NOTICES`, and the linked modules'
license texts. The app is ad-hoc signed, not notarized; the Cask says so.
`package-macos-local` records `source_dirty=true`, writes a `file://` Cask into
the installed `soundadam/local` tap checkout (or
`~/workspaces/soundadam/homebrew-local`), and publishes nothing.

## Versioning and local iteration

nju-connect follows the sing-box prerelease sequence. Git tags add a leading
`v`, while the version embedded in the app and CLI does not:

```text
v1.1.0-alpha.1 -> v1.1.0-beta.1 -> v1.1.0-rc.1 -> v1.1.0
```

Repeated builds in one channel increment its sequence number. By default, a
local update from installed stable version `1.0.0` starts the next feature train
as `1.1.0-alpha.1`, and the next local update becomes `1.1.0-alpha.2`. Stable
maintenance releases remain on the patch line, so the stable successor to
`1.0.0` is `1.0.1`:

```sh
make local-update
make local-update CHANNEL=beta
make local-update CHANNEL=rc
make local-update CHANNEL=stable
```

`local-update` resolves the active tap with `brew --repo soundadam/local`. It
runs the release checks, builds the universal app, updates and commits the local
Cask, reinstalls it, removes quarantine only from the installed development
preview, and verifies the installed app, embedded CLI, code signature, and
helper hash. Set `PUSH_TAP=1` to push the resulting private-tap commit. Use
`INSTALL_UPDATE=0` when only packaging and committing the Cask is desired.

`make macos-dev` builds the CLI, menu-bar app, and aTrust OAuth helper, signs
them with a local Apple Development identity, and runs the app against the
freshly built CLI. `make macos-preview` opens the panel in a window with
simulated states; see [`macos/README.md`](macos/README.md).
