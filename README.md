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
Use `soundconnect status --json` for the versioned machine-readable form. A
stopped runtime is reported explicitly and returns a nonzero status; the
command does not infer liveness from a PID file or `runtime.log`.

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

Only the latest compact result is retained locally. Per-second samples and the
client public IP are not stored. Developer ID signing, notarization, and a real
bandwidth test remain explicit release/operator gates.

## CLI release packaging

From a clean worktree, package the two supported macOS CLI architectures with
an injected semantic version and checksums:

```sh
make cli-release VERSION=v0.1.0
```

The archives include `LICENSE`, `THIRD_PARTY_NOTICES`, and the linked modules'
license texts. Signing, notarization, tag creation, upload, and publication
remain separate release gates.

## macOS development first run

For an unsigned or quarantined development copy only:

\`\`\`sh
xattr -d com.apple.quarantine /path/to/soundconnect
\`\`\`

This is a local testing workaround, not a release installation step. Release
artifacts should be signed and notarized.

The current Homebrew Cask packages an explicitly labeled macOS design preview
plus the native universal CLI. Build it with `make package-macos VERSION=X.Y.Z`;
the public Cask must continue to disclose that VPN setup/control remains a
simulated design preview while campus speed testing uses the real bundled CLI,
and that ad-hoc signing is not Apple notarization.

For an explicitly local, dirty-tree preview, use
`make package-macos-local VERSION=X.Y.Z`. It records `source_dirty=true`, writes
a `file://` Cask into
`~/workspaces/soundadam/homebrew-local`, and never publishes an artifact.
