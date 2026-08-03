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
the public Cask must continue to disclose that the UI is not yet connected to
the CLI and that ad-hoc signing is not Apple notarization.
