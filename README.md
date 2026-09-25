# soundconnect

soundconnect is a native command-line client for secure campus connectivity.

## CLI setup and migration

`soundconnect setup` stores non-secret configuration in the operating system's
user configuration directory. On macOS the long-lived VPN password is stored
in the login Keychain under service `com.soundadam.soundconnect`; it is not
written to the TOML configuration.

Pre-release worktree state can be imported explicitly without overwriting an
existing destination:

```sh
soundconnect migrate --from /path/to/old/soundconnect-worktree
```

The migration verifies and copies the old state and preserves the source for
manual rollback or deletion after verification.

Running `soundconnect` with no command is equivalent to `soundconnect connect`
and starts the native userspace runtime. Use `soundconnect dry-run` to validate
authentication and the gateway handoff without starting the dataplane.

While a foreground or detached native runtime is active, `soundconnect status`
queries its owner-only local control socket and prints only sanitized state.
Use `soundconnect status --json` for the versioned machine-readable form, and
add `--watch` to stream that snapshot once per second. A
stopped runtime is reported explicitly and returns a nonzero status; the
command does not infer liveness from a PID file or `runtime.log`.

## Protocol backends

soundconnect separates the application from the campus VPN protocol. The
EasyConnect backend is the default. An aTrust backend is selectable, but this
build contains only its SoundConnect-owned seam: `connect` with the aTrust
backend stops with "aTrust protocol support is not available in this build"
and exit code 1 until the independently written protocol core lands (see
`docs/atrust-cleanroom.md`).

```sh
soundconnect backends --json                 # presentation-safe backend catalog
soundconnect configure --backend atrust      # switch non-secret settings only
soundconnect setup --backend atrust --auth-type auth/psw
soundconnect logout                          # forget saved aTrust session state
```

Both backends share the one CLI-owned SOCKS5 listener (default
`127.0.0.1:1081`) and the Keychain VPN password. `configure` refuses to change
the profile while a runtime is active. Set an absolute
`SOUNDCONNECT_CONFIG_DIR` to run against an isolated configuration directory.

## Campus speed test

`soundconnect speedtest` measures the pinned NJU campus IPv4 LibreSpeed target.
It first tries the system's direct path. If that path is unavailable, an active
soundconnect runtime may provide its owner-only loopback SOCKS5 path; otherwise
the command asks the user to connect soundconnect and retry. Results always
identify the selected path and never infer it from connection state alone.

LibreSpeed remains a separate third-party executable. On macOS, the preview
Cask depends on the `librespeed-cli-soundconnect` Formula, which builds the
pinned upstream source with the explicit SOCKS and structured-progress patches
required by soundconnect. The App neither embeds nor downloads this helper.
Linux packaging remains a separate decision; developers may point to a
compatible absolute helper path with `SOUNDCONNECT_LIBRESPEED_CLI`. JSON and
redirected modes fail closed instead of waiting for input. Useful machine
interfaces are:

```sh
soundconnect speedtest campus --route auto --json
soundconnect speedtest component status --json
soundconnect speedtest last --json
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

The archives include `LICENSE`, `THIRD_PARTY_NOTICES`, and the linked modules'
license texts. Signing, notarization, tag creation, upload, and publication
remain separate release gates.

## macOS development first run

For an unsigned or quarantined development copy only:

```sh
xattr -d com.apple.quarantine /path/to/soundconnect
```

This is a local testing workaround, not a release installation step. Release
artifacts should be signed and notarized.

The current Homebrew Cask packages the macOS menu-bar client plus the native
universal CLI. The menu bar saves credentials through the CLI into Keychain,
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

soundconnect follows the sing-box prerelease sequence. Git tags add a leading
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
