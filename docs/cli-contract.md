# soundconnect CLI contract

This document is the inventory of everything outside code can observe about
the `soundconnect` CLI: commands, flags, exit codes, the stdout/stderr split,
and the machine-readable output the macOS app parses. The golden tests in
`cmd/soundconnect` enforce it, so the refactors that follow (service layer,
Cobra) are checked against the same surface.

## Contract strength

- **Strong contract.** The macOS app, or a script, depends on it. A change
  needs a matching change on the consuming side in the same PR, and the shared
  fixtures in `testdata/contract/` must change deliberately. This covers JSON
  output, NDJSON event streams, exit codes, the command lines listed under
  [Invocations from the macOS app](#invocations-from-the-macos-app), the
  `Verification code` prompt text on stderr, and the stdin line protocols.
- **Weak contract.** It is for humans only. Help text, usage text, plain-text
  output and error wording may change, but the goldens must be regenerated and
  the diff reviewed in the PR.

## Global behaviour

- `soundconnect` with no arguments runs `connect` with no flags.
- `help`, `-h` and `--help` print the top-level usage on **stdout** and exit 0.
  `help <command>` prints that command's help.
- An unknown command prints `unknown command "<name>"` and the usage on
  **stderr** and exits 2.
- `<command> -h` / `--help` prints that command's flags on **stdout** and
  exits 0.
- An unknown flag, an extra positional argument, or an invalid flag
  combination exits 2. The message goes to stderr (flag errors also print the
  command's flags there), and stdout stays empty.
- Commands are a Cobra tree (`cmd/soundconnect`), and flags are parsed by
  pflag. Long flags take two dashes (`--json`); `-h` is the only short flag.
  The single-dash long form that Go's `flag` package accepted (`-json`) was
  dropped with the move to Cobra: it exits 2 with
  `unknown flag: -json (long flags take two dashes: --json)`, as
  `single_dash_long_flag.golden` records. Flags may come before or after
  positional arguments. Cobra's own messages, `completion` command and
  `Error:` prefix are switched off; `exitStatus` still owns every exit code
  and error line.
- Secrets never appear in argv, the environment, stdout or stderr. Passwords
  and verification codes arrive only through a hidden terminal prompt or an
  explicit `--*-stdin` pipe.
- `SOUNDCONNECT_CONFIG_DIR` (an absolute path) replaces the default state
  directory, which is `os.UserConfigDir()/soundconnect`. The runtime control
  socket is derived from this directory, so an isolated directory also
  isolates `status`, `disconnect` and `connect`, and gets its own keyring
  service (`com.soundadam.soundconnect.<hash>`) so it never touches the real
  saved password.
- When standard input and standard error are both terminals and no
  `--*-stdin` flag is given, `setup`, `account` and a first-run `connect`
  show interactive forms on stderr (`internal/tui`). `TERM=dumb` or
  `SOUNDCONNECT_ACCESSIBLE=1` keeps plain line prompts. Anything piped or
  redirected gets the line prompts described below, byte for byte.
- Forms lay out at most 80 columns wide and at least two columns short of
  the width the terminal reports (80 when it reports none), because the
  renderer redraws by moving the cursor up one row per line: a line that
  fills the last column takes two rows in terminals that wrap there, draw
  the `┃` border as two cells, or report a column more than they show, and
  every redraw would then stack below the last one. A terminal that cannot
  move the cursor up at all still stacks frames; `TERM=dumb` or
  `SOUNDCONNECT_ACCESSIBLE=1` is the workaround there.
- The linked aTrust core (zju-connect) narrates its requests, prompts and
  node probes through Go's standard logger. None of that reaches stdout or
  stderr: `internal/backend/atrust` captures it, answers its prompts through
  SoundConnect's own, and discards the lines. `SOUNDCONNECT_DEBUG=1` copies
  them raw to stderr for troubleshooting; that output is not contract, may
  include gateway messages and masked phone numbers, and breaks the
  one-line guarantees below, so the macOS app never sets it.

### Credential storage

The password (keyring account `password`) and the aTrust session
(`atrust-session`) live in the system keyring through go-keyring: the macOS
login Keychain, the Secret Service on Linux, or the Windows Credential
Manager. Secrets too large for one item are split into chunk items behind a
header item. `credential_store = "file"` in `config.toml` keeps them in
owner-only files (`credential`, `atrust-client-data`) instead, for hosts
without a keyring. A secret left by an earlier release (the pre-keyring
Keychain items `vpn-password` / `atrust-client-data`, or those files) is moved
into the keyring the first time it is read, and the old copy is removed.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success; `-h`/`--help`; the user cancelled (Ctrl-C) a prompt or an attended connect. |
| 1 | Runtime failure. Also used as an informational "no": `status` when nothing is running, `doctor` when not ready, `speedtest component status` when not installed, `speedtest last` with no saved result. |
| 2 | Usage error: unknown command or flag (including a single-dash long flag such as `-json`), a positional argument, an invalid combination, an unknown backend, or `connect --background` on aTrust. |
| 130 | `speedtest campus` cancelled by SIGINT. |

Services in `internal/app` return an `app.UsageError` for exit 2 and
`context.Canceled` for a cancellation; `exitStatus` in `cmd/soundconnect`
is the one place that turns errors into exit codes and stderr lines.

## Commands

| Command | Flags | stdout | Notes |
|---|---|---|---|
| *(none)*, `connect` | `--background`, `--verification-code-stdin` | Progress lines (`authentication: accepted`, `state: …`, `socks: …`); `background: pid=<n> log=<path>` | Foreground runs until Ctrl-C or `disconnect`. `--background` is EasyConnect only; on aTrust it exits 2 and points to a foreground `connect`. |
| `setup` | `--backend` (`easyconnect`), `--server`, `--username`, `--auth-type`, `--login-domain`, `--socks-listen`, `--upstream-proxy`, `--tls-insecure`, `--native-tls-insecure`, `--password-stdin` | `configuration:`, `backend:`, `credential:` | Prompts on stderr for a missing gateway or account; on a terminal it runs the guided wizard. Merges into the saved configuration: flags left out keep their saved values. Stores the password before the configuration. Changing the account, gateway or backend forgets the aTrust session. Refuses (exit 1) while a runtime is active. |
| `account` | — | Same as `account show` | On a terminal, opens a menu to change the account instead. |
| `account show` | `--json` | `configuration:`, `backend:`, `server:`, `username:`, `auth_type:`, `credential_store:`, `password:`, `atrust_session:` | Never reads a secret; `password` and `atrust_session` are `saved`, `missing`, `not_required` or `unavailable`. |
| `account set-password` | `--password-stdin` | `password: saved` | Replaces only the password. |
| `account set-username <name>` | — | `username:`, `atrust_session_cleared:` | Forgets the aTrust session when the name changes. |
| `account forget` | `--password`, `--session` | `password_forgotten:`, `atrust_session_forgotten:`, `oauth_profile_cleared:` | At least one flag (exit 2 otherwise). `--session` also clears the OAuth helper's browser profile when the helper is present. |
| `configure` | `--backend`, `--server`, `--username`, `--auth-type`, `--login-domain`, `--socks-listen`, `--upstream-proxy` | `configuration:`, `backend:`, `server:`, `socks_listen:` (and `auth_type:`, `login_domain:` for aTrust) | Merges into the existing configuration and never touches secrets. Refuses (exit 1) while a runtime is active. |
| `backends` | `--json` | Tab-separated catalog, or JSON | Strong: `--json`. |
| `auth-info` | `--backend` (`atrust`), `--server`, `--json` | Discovered aTrust methods | aTrust only; EasyConnect exits 2. |
| `migrate` | `--from` (`.`) | `configuration_migrated:`, `credential_migrated:`, `source_preserved: true` | Idempotent; copies, never moves. |
| `doctor` | `--json` | `ready`, `configuration`, `credential_store`, `upstream_proxy`, `next_step` (text: `next:`) | Exit 1 when not ready. `next_step` is the command to run next: `soundconnect setup`, `soundconnect account set-password`, `soundconnect configure --upstream-proxy` or `soundconnect connect`. Strong: `--json`. |
| `disconnect` | — | `stopping: true`, or `running: false` | Exit 0 in both cases. |
| `logout` | — | `atrust_session_cleared: true`, `oauth_profile_cleared: <bool>` | Clears only aTrust state; the shared password and the configuration stay. On macOS without the OAuth helper next to the binary (a bare `bin/soundconnect`), it still exits 0 with `oauth_profile_cleared: false` and one stderr line saying the browser sign-in state was not cleared. A helper that fails exits 1. |
| `dry-run` | — | Authentication and bootstrap summary | EasyConnect; never starts the dataplane. |
| `status` | `--json`, `--watch` | Text, one JSON object, or (with `--watch`) one JSON object per line per second | `--watch` requires `--json`. Exit 1 when stopped. Strong: `--json`, `--watch`. |
| `speedtest` / `speedtest campus` | `--route` (`auto`), `--json`, `--json-events` | Text result, one JSON result, or NDJSON events | `--json` and `--json-events` are mutually exclusive. |
| `speedtest probe` | `--route`, `--json` | `target`, `route`, `latency_ms` | |
| `speedtest last` | `--json` | Last saved result | |
| `speedtest component status` | `--json` | Component status | Exit 1 when not installed. `--yes` and `--json-events` are accepted by the parser but exit 2. |
| `speedtest component install` | `--yes`, `--json-events` | NDJSON progress, or a final line | Needs `--yes` outside a terminal. `--json` exits 2. |
| `version` | — | `soundconnect <version>` | |
| `_native-runtime` | *(hidden)* | Runtime log | See below. |

In JSON and NDJSON modes, speed-test failures are reported on **stdout** as
`{"schema_version":1,"type":"error","error":{"code":…,"message":…}}`, with
stderr kept empty. The error codes are `invalid_arguments`, `local_state`,
`component_missing`, `interaction_required`, `component_install_failed`,
`soundconnect_required`, `speedtest_unavailable`, `cancelled` and
`no_speedtest_result`.

## Stdin line protocols

- `setup --password-stdin` and `account set-password --password-stdin` read
  exactly one line, the password; `\n` or `\r\n` is stripped. An empty line
  exits 1 with `credential is empty`.
- Without a configuration, `connect`, the default command and `dry-run`
  fail with `load configuration: no configuration yet; run "soundconnect
  setup" first`; on a terminal they offer the guided setup instead. A missing
  password fails with `no saved VPN password; run "soundconnect account
  set-password"`.
- When the gateway rejects the saved username or password (EasyConnect's
  password step, or aTrust `auth/psw`), a non-terminal `connect` exits 1 and
  its entire stderr is one line, starting with the stable token
  `credential_rejected:` and ending with
  `run "soundconnect account set-password"`. On a terminal,
  `connect` instead offers to re-enter the password, change the username and
  password (which also forgets the aTrust session), or stop (exit 0). The new
  values are saved before the next attempt, and at most three sign-ins are
  made before it gives up with the same token.
- `connect --verification-code-stdin` prints `Verification code: ` on stderr
  when the gateway asks for a code, then reads one line from stdin. The
  aTrust core's own SMS prompt is never printed next to it. Without
  the flag the prompt needs a terminal and fails with
  `hidden prompt requires a terminal` on a pipe.
- An aTrust OAuth login without the bundled helper prints the login URL and
  `Callback URL: ` on stderr, then reads the pasted callback URL from stdin.
  The URL's host and port must match the gateway.

## Invocations from the macOS app

These command lines are strong contract. The app finds the CLI at
`Contents/Helpers/soundconnect`, or at `$SOUNDCONNECT_HELPER`.

| App action | Command line | Parsed output |
|---|---|---|
| Detect running / configured | `status --json`, `doctor --json` | `RuntimeStatusPayload`, `DoctorPayload.ready` |
| Live status | `status --json --watch` | One `RuntimeStatusPayload` per line |
| Backend catalog | `backends --json` | `SoundConnectBackendCatalog` |
| Save account | `setup --backend <b> --server <s> --username <u> --password-stdin` | Exit code; the password is written to stdin |
| Switch backend | `configure --backend <b>` | Exit code |
| Forget aTrust session | `account forget --session` | Exit code |
| Connect, EasyConnect | `connect --background --verification-code-stdin` | Exit code; `verification code` on stderr triggers the code field |
| Connect, aTrust | `connect --verification-code-stdin` | `backend: atrust` plus `authentication: accepted` or `authentication: resumed` on stdout marks the handoff; `verification code` on stderr |
| Turn off | SIGTERM to a foreground `connect`, then `disconnect` | Exit code |
| Speed test | `speedtest component status --json`, `speedtest component install --yes --json-events`, `speedtest probe --route auto --json`, `speedtest last --json`, `speedtest campus --route auto --json-events` | `ComponentStatus`, `CampusSpeedTestEvent`, `CampusProbeResult`, `CampusSpeedTestResult` |

The app classifies a failed connect as a credential problem when stderr
contains `credential_rejected:` or `no saved VPN password`, and a cancelled
Keychain prompt by `Keychain access was cancelled` or `OSStatus -128`. These
substrings are strong contract; the shared fixtures
`testdata/contract/connect_credential_rejected*.stderr` pin the first one.

## Background runtime handoff

`connect --background` authenticates in the foreground, then re-executes the
same binary as `soundconnect _native-runtime` with:

- stdin set to `/dev/null`, and stdout/stderr appended to
  `<config dir>/runtime.log` (owner-only, `0600`);
- **fd 3**: a pipe carrying one JSON handoff (at most 16 KiB): settings,
  dataplane plan, the 48-byte native gateway token, the native profile
  (`community-utls` only), and the absolute `.sock` status path. The parent
  clears its copy of the token after writing;
- **fd 4**: a readiness pipe. The child writes the single byte `0x01` once the
  runtime first reaches `connected`.

The parent waits up to 45 s for readiness, then prints
`background: pid=<n> log=<path>` and exits 0. If the child exits first, or the
handoff is invalid, the parent kills it and fails with
`background native runtime did not initialize`. `_native-runtime` accepts no
arguments (exit 2 otherwise) and is omitted from the usage text.

## Runtime control socket

A running runtime serves a Unix socket (`internal/runtimecontrol`) at
`$TMPDIR/soundconnect-runtime-<euid>/<sha256(config dir)[:12]>.sock`. Both the
directory (`0700`) and the socket (`0600`) must be owned by the current user.

- A client that sends nothing receives one `status --json` snapshot.
- A client that sends `{"command":"disconnect"}` receives `{"ok":true}` and
  the runtime stops.

`status` and `disconnect` treat a missing or dead socket as "not running".

## Tests and fixtures

- `internal/app`: unit tests for each service over an isolated state
  directory built into `app.Deps`, runnable in parallel. Guided flows run
  through `LineInteraction` with scripted answers.
- `internal/credential`: the keyring store runs against go-keyring's
  in-memory mock; `cmd/soundconnect` and `internal/app` tests install the
  mock too, so no test or re-executed runtime child reaches the real keyring.
- `internal/tui`: huh forms driven by scripted key presses.
- `cmd/soundconnect/clitest_test.go`: the harness. It runs the real Cobra command tree
  with its own `app.Deps` (built by `testDeps` in `deps_test.go`) against an
  isolated `SOUNDCONNECT_CONFIG_DIR`, with file-backed secret stores, a fake
  aTrust core, and no EasyConnect runtime or network. `cmd/soundconnect` has
  no package-level dependency variables; `run` takes the `app.Deps` that
  `main` builds with `productionDeps`. Fake EasyConnect gateways and status
  sockets live next to the tests that use them.
- `cmd/soundconnect/testdata/cli/*.golden`: the arguments, exit code, stdout
  and stderr of each case. The config directory appears as `$CONFIG_DIR`.
- `testdata/contract/*`: JSON, NDJSON and stderr produced by real CLI runs and read
  by `macos/Tests/SoundConnectUITests/CLIContractFixtureTests.swift`.
  Wall-clock times, latency and the CPU architecture are normalized to fixed
  values of the same shape; timestamps keep Go's nanosecond digits.

The dependency runs one way: the Go core owns the contract and generates the
fixtures, and the Swift UI shell consumes them. CI checks the core in its own
jobs (`Core (Go, …)`) without Swift, so a failing `macOS UI shell (Swift)` job
points at the app, never at the core.

To regenerate both after an intended change, run the command below, review
`git diff`, and update the Swift decoders in the same PR if a fixture changed:

```bash
go test ./cmd/soundconnect -update
```
