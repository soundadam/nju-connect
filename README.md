# nju-connect

南京大学校园 VPN 的原生客户端：一个同时支持 EasyConnect 与 aTrust 的命令行程序，
以及 macOS 菜单栏应用。连接后在本机提供 SOCKS5 代理（默认 `127.0.0.1:1081`），
不改系统路由；密码保存在系统钥匙串里。

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
的客户端核心。本项目以 AGPL-3.0 发布，与南京大学、深信服均无隶属关系。

The rest of this README is developer documentation, in English.

## CLI setup and migration

`nju-connect setup` stores non-secret configuration in the operating system's
user configuration directory. The long-lived VPN password and the aTrust
session are kept in the system keyring under service
`com.soundadam.nju-connect`: the login Keychain on macOS, the Secret Service
on Linux, or the Windows Credential Manager. They are never written to the
TOML configuration. On a host without a keyring, such as a headless Linux box
over SSH, add `credential_store = "file"` to `config.toml` to keep them in
owner-only files instead.

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

Pre-release worktree state can be imported explicitly without overwriting an
existing destination:

```sh
nju-connect migrate --from /path/to/old/nju-connect-worktree
```

The migration verifies and copies the old state and preserves the source for
manual rollback or deletion after verification.

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

LibreSpeed remains a separate third-party executable. On macOS, the preview
Cask depends on the `librespeed-cli-nju-connect` Formula, which builds the
pinned upstream source with the explicit SOCKS and structured-progress patches
required by nju-connect. The App neither embeds nor downloads this helper.
Linux packaging remains a separate decision; developers may point to a
compatible absolute helper path with `NJU_CONNECT_LIBRESPEED_CLI`. JSON and
redirected modes fail closed instead of waiting for input. Useful machine
interfaces are:

```sh
nju-connect speedtest campus --route auto --json
nju-connect speedtest component status --json
nju-connect speedtest last --json
```

The CLI retains only the latest compact result. The macOS UI separately keeps
bounded local graph samples in its own preferences so the inspector can restore
the previous curve; the client public IP is not stored. Developer ID signing,
notarization, and a real bandwidth test remain explicit release/operator gates.

## CLI release packaging

From a clean worktree, package the two supported macOS CLI architectures with
an injected semantic version and checksums:

```sh
make cli-release VERSION=v1.0.0
```

nju-connect is licensed under AGPL-3.0; see `LICENSE`. The archives include
`LICENSE`, `THIRD_PARTY_NOTICES`, and the linked modules' license texts. Signing, notarization, tag creation, upload, and publication
remain separate release gates.

## macOS development first run

For an unsigned or quarantined development copy only:

```sh
xattr -d com.apple.quarantine /path/to/nju-connect
```

This is a local testing workaround, not a release installation step. Release
artifacts should be signed and notarized.

The current Homebrew Cask packages the macOS menu-bar client plus the native
universal CLI. The menu bar saves credentials through the CLI into the Keychain,
starts and stops the real background userspace runtime, submits one-time codes
through a private stdin pipe, and streams sanitized runtime state while the
panel is open. Build it with
`make package-macos VERSION=X.Y.Z`; the Cask must continue to disclose that
ad-hoc signing is not Apple notarization.

For an explicitly local, dirty-tree preview, use
`make package-macos-local VERSION=X.Y.Z`. It records `source_dirty=true`, writes
a `file://` Cask into the installed `soundadam/local` tap checkout (falling back
to `~/workspaces/soundadam/homebrew-local` when the tap is not installed), and
never publishes an artifact.

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
freshly built CLI.

Local Casks use `file://` artifacts and are not public releases. A public
release must start from a clean tagged commit, use immutable uploaded assets,
verify the downloaded SHA-256, and satisfy signing and notarization gates.
