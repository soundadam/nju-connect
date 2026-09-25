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
- An unknown command prints `unknown command "<name>"` and the usage on
  **stderr** and exits 2.
- `<command> -h` / `--help` prints that command's flags on **stdout** and
  exits 0.
- An unknown flag, an extra positional argument, or an invalid flag
  combination exits 2. The message goes to stderr (flag errors also print the
  command's flags there), and stdout stays empty.
- Flags are parsed with Go's `flag` package. Long flags therefore accept
  either one or two dashes (`-json` or `--json`), and flags must come before
  positional arguments. Nothing in the app, scripts or docs uses the
  single-dash form. It is scheduled to be dropped when the CLI moves to
  Cobra; `single_dash_long_flag.golden` records it until then.
- Secrets never appear in argv, the environment, stdout or stderr. Passwords
  and verification codes arrive only through a hidden terminal prompt or an
  explicit `--*-stdin` pipe.
- `SOUNDCONNECT_CONFIG_DIR` (an absolute path) replaces the default state
  directory, which is `os.UserConfigDir()/soundconnect`. The runtime control
  socket is derived from this directory, so an isolated directory also
  isolates `status`, `disconnect` and `connect`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success; `-h`/`--help`; the user cancelled (Ctrl-C) a prompt or an attended connect. |
| 1 | Runtime failure. Also used as an informational "no": `status` when nothing is running, `doctor` when not ready, `speedtest component status` when not installed, `speedtest last` with no saved result. |
| 2 | Usage error: unknown command or flag, a positional argument, an invalid combination, an unknown backend, or `connect --background` on aTrust. |
| 130 | `speedtest campus` cancelled by SIGINT. |

## Commands

| Command | Flags | stdout | Notes |
|---|---|---|---|
| *(none)*, `connect` | `--background`, `--verification-code-stdin` | Progress lines (`authentication: accepted`, `state: …`, `socks: …`); `background: pid=<n> log=<path>` | Foreground runs until Ctrl-C or `disconnect`. `--background` is EasyConnect only. |
| `setup` | `--backend` (`easyconnect`), `--server`, `--username`, `--auth-type`, `--login-domain`, `--socks-listen`, `--upstream-proxy`, `--tls-insecure`, `--native-tls-insecure`, `--password-stdin` | `configuration:`, `backend:`, `credential:` | Prompts on stderr for a missing gateway or account. Rewrites the whole configuration from its flags. |
| `configure` | `--backend`, `--server`, `--username`, `--auth-type`, `--login-domain`, `--socks-listen`, `--upstream-proxy` | `configuration:`, `backend:`, `server:`, `socks_listen:` (and `auth_type:`, `login_domain:` for aTrust) | Merges into the existing configuration and never touches secrets. Refuses (exit 1) while a runtime is active. |
| `backends` | `--json` | Tab-separated catalog, or JSON | Strong: `--json`. |
| `auth-info` | `--backend` (`atrust`), `--server`, `--json` | Discovered aTrust methods | aTrust only; EasyConnect exits 2. |
| `migrate` | `--from` (`.`) | `configuration_migrated:`, `credential_migrated:`, `source_preserved: true` | Idempotent; copies, never moves. |
| `doctor` | `--json` | `ready`, `configuration`, `credential_store`, `upstream_proxy` | Exit 1 when not ready. Strong: `--json`. |
| `disconnect` | — | `stopping: true`, or `running: false` | Exit 0 in both cases. |
| `logout` | — | `atrust_session_cleared: true`, `oauth_profile_cleared: <bool>` | Clears only aTrust state; the shared password and the configuration stay. |
| `dry-run` | — | Authentication and bootstrap summary | EasyConnect; never starts the dataplane. |
| `status` | `--json`, `--watch` | Text, one JSON object, or (with `--watch`) one JSON object per line per second | `--watch` requires `--json`. Exit 1 when stopped. Strong: `--json`, `--watch`. |
| `speedtest` / `speedtest campus` | `--route` (`auto`), `--json`, `--json-events` | Text result, one JSON result, or NDJSON events | `--json` and `--json-events` are mutually exclusive. |
| `speedtest probe` | `--route`, `--json` | `target`, `route`, `latency_ms` | |
| `speedtest last` | `--json` | Last saved result | |
| `speedtest component status` | `--json` | Component status | Exit 1 when not installed. |
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

- `setup --password-stdin` reads exactly one line, the password; `\n` or
  `\r\n` is stripped. An empty line exits 1 with `credential is empty`.
- `connect --verification-code-stdin` prints `Verification code: ` on stderr
  when the gateway asks for a code, then reads one line from stdin. Without
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
| Connect, EasyConnect | `connect --background --verification-code-stdin` | Exit code; `verification code` on stderr triggers the code field |
| Connect, aTrust | `connect --verification-code-stdin` | `backend: atrust` plus `authentication: accepted` or `authentication: resumed` on stdout marks the handoff; `verification code` on stderr |
| Turn off | SIGTERM to a foreground `connect`, then `disconnect` | Exit code |
| Speed test | `speedtest component status --json`, `speedtest component install --yes --json-events`, `speedtest probe --route auto --json`, `speedtest last --json`, `speedtest campus --route auto --json-events` | `ComponentStatus`, `CampusSpeedTestEvent`, `CampusProbeResult`, `CampusSpeedTestResult` |

The app currently classifies a rejected credential by matching stderr text
(`authentication rejected`, `password authentication`, `credential`) and a
cancelled Keychain prompt by `Keychain access was cancelled` or
`OSStatus -128`. Treat those substrings as strong contract until the app
switches to a stable token.

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

A running runtime serves a Unix socket at
`$TMPDIR/soundconnect-runtime-<euid>/<sha256(config dir)[:12]>.sock`. Both the
directory (`0700`) and the socket (`0600`) must be owned by the current user.

- A client that sends nothing receives one `status --json` snapshot.
- A client that sends `{"command":"disconnect"}` receives `{"ok":true}` and
  the runtime stops.

`status` and `disconnect` treat a missing or dead socket as "not running".

## Tests and fixtures

- `cmd/soundconnect/clitest_test.go`: the harness. It runs the real dispatcher
  against an isolated `SOUNDCONNECT_CONFIG_DIR`, with file-backed secret
  stores and a fake aTrust core. Fake EasyConnect gateways and status sockets
  live next to the tests that use them.
- `cmd/soundconnect/testdata/cli/*.golden`: the arguments, exit code, stdout
  and stderr of each case. The config directory appears as `$CONFIG_DIR`.
- `testdata/contract/*`: JSON and NDJSON produced by real CLI runs and decoded
  by `macos/Tests/SoundConnectUITests/CLIContractFixtureTests.swift`.
  Wall-clock times, latency and the CPU architecture are normalized to fixed
  values that still decode.

To regenerate both after an intended change, run the command below, review
`git diff`, and update the Swift decoders in the same PR if a fixture changed:

```bash
go test ./cmd/soundconnect -update
```
