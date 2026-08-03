# soundconnect architecture and release readiness

This is the single detailed product architecture and release-readiness document. \`README.md\` remains the one-line product narrative. The files under \`research/\` record external evidence only; they are not product runtime or release authority.

The audit started from \`7e86436\` and covers the native userspace core that has already been validated against the live gateway. The first macOS UI phase adds a design-only SwiftUI shell under \`macos/\`; it does not yet connect the UI to the Go backend. It also does not change gateway wire bytes, start a vendor service, install a VPN, or change routes/DNS/PF.

## Ownership and data flow

\`\`\`mermaid
flowchart TD
    cfg["internal/config\nsole Config authority"] --> auth["internal/gatewayauth\nHTTPS auth and bootstrap"]
    cred["internal/credential\nKeychain, file, or hidden prompt"] --> auth
    auth --> state["SessionState + Bootstrap"]
    state --> plan["internal/core\nDataplanePlan"]
    auth --> token["NativeGatewayToken\ntyped gateway boundary"]
    plan --> app["internal/nativeapp\nstable application adapter"]
    token --> app
    app --> session["runtime.NativeSession\nresource and lifecycle owner"]
    session --> owner["runtime.Owner\nreadiness and watchdog"]
    owner --> command["Command runner"]
    owner --> cohort["RX + TX cohort"]
    owner --> socks["SOCKS runner"]
    command --> identity["CommandIdentity\nassigned IPv4 + heartbeat"]
    identity --> userspace["gVisor userspace stack\nchannel link only"]
    userspace --> cohort
    userspace --> socks
    cohort --> profile["ProtocolProfile\nversion-sensitive wire boundary"]
    socks --> loopback["numeric loopback listener\napplication-facing"]
    session --> observer["sanitized serialized observer"]
    observer --> app
\`\`\`

The CLI owns orchestration outside the core: it loads the one \`Config\`, reads a credential, authenticates, obtains \`SessionState\`, validates \`Bootstrap\`, builds one \`DataplanePlan\`, and passes the resulting typed token and settings to \`nativeapp\`. \`nativeapp\` selects a \`ProtocolProfile\` and exposes the stable \`Session\`, \`Observer\`, failure-stage, traffic, and profile metadata API intended for a future macOS UI or other host application.

\`runtime.NativeSession\` owns the token copy, userspace stack, stream cohort, SOCKS listener, and their shutdown. The command runner must establish a \`CommandIdentity\` before userspace resources are initialized. That identity is the only handoff for the assigned IPv4 address and heartbeat LAN address. The userspace stack is a channel-backed gVisor stack; it does not create a kernel TUN, install routes, alter DNS, or alter PF.

\`ProtocolProfile\` owns version-sensitive command and data framing. It does not own readiness, SOCKS, traffic accounting, or reconnection. The live community profile still reads the complete first TLS application reply into the established 1500-byte buffer. The EasyConnect 7.6.7 profile still uses its fixed evidence-backed preface. Heartbeat construction and gateway-token derivation remain unchanged.

## Readiness and cancellation

\`runtime.Owner\` is the single runtime state authority. Its readiness set is exactly \`Command\`, \`RX\`, \`TX\`, and \`SOCKS\`:

\`\`\`text
command ready + RX ready + TX ready + SOCKS ready -> connected
any component drops after connected              -> reconnecting
initial or partial readiness                      -> connecting
reconnecting without recovery before watchdog    -> renewal_required
\`\`\`

The RX/TX cohort is one failure domain. On the first worker failure it reports both streams not ready, cancels the generation, closes both stream connections, waits for both worker results, and only then allows the next generation to open. \`NativeSession.Close\` cancels the session and waits for the owner and all owned workers. The SOCKS runner closes its listener, waits for accepted connection handlers, and joins its cancellation monitor. Renewal is a boundary outcome: authentication renewal is performed by the host layer after the old session has completely joined.

The observer is a projection, not another state authority. \`nativeapp\` maps the allowlisted runtime states and failure stages into a stable, secret-safe API and serializes callbacks. It never exports gateway addresses, cookies, tokens, wire bytes, or raw error details.

The two token types remain deliberately incompatible:

* \`sessiontoken.NativeGatewayToken\` crosses only the native gateway runtime boundary and is copied for calls and cleared after use.
* \`sessiontoken.LocalControlToken\` exists only inside the research-only ECAgent \`NotStartService\` probe. The production-facing client exposes no \`StartService\` operation.

## Simplification decisions

The following redundant paths were removed in small, separately reviewable commits:

| Removed | Why it was redundant | Result |
| --- | --- | --- |
| Credential \`Backend\`/\`Store\`/\`Options\` factory and parser | Only one file backend and one prompt path existed; the factory added a second configuration authority. | \`FileStore\` and \`PromptStore\` are explicit APIs. |
| \`PromptStore.Set\` | Prompt storage is intentionally non-persistent. | The prompt API only reads and clears transient bytes. |
| Production \`TCPDialFunc\` test adapter | The production dialer already has the \`TCPDialer\` contract; this alias only existed to inject test behavior. | The helper is test-only. |
| \`dial.ContextFunc\` | It wrapped a function signature without adding policy. | Callers use the function type directly. |
| IPv4 decoder pending-tail clone | A bounded header tail already owned by the decoder did not need to be copied on every feed. | One allocation and 16 bytes per benchmark operation are removed without changing framing. |

The \`Runner\` seam, \`ProtocolProfile\`, \`nativeapp.Observer\`, and token types remain. They are used by production ownership boundaries as well as tests; removing them would collapse cancellation, version, or application-safety contracts rather than remove duplication. Runtime errors have one authority in \`internal/runtime\`; \`nativeapp\` only performs a typed allowlisted projection.

## Performance evidence

The benchmark was added before the IPv4 change, and the hot path was profiled before editing. The reproducible command for the focused comparison was:

\`\`\`sh
go test ./internal/runtime -run '^$' -bench '^BenchmarkIPv4DecoderFeed$' -benchmem -benchtime=2s -count=5
\`\`\`

The baseline was run from \`bfdbe08\` in a detached temporary worktree; the optimized run was \`a1d694e\` on the same Linux amd64 host. Medians across five samples were:

| Benchmark | Before | After | Change |
| --- | ---: | ---: | ---: |
| \`BenchmarkIPv4DecoderFeed\` ns/op | 8,786 | 7,480 | -14.9% indicative median |
| bytes/op | 11,792 | 11,776 | -16 bytes |
| allocs/op | 6 | 5 | -1 allocation |

CPU time varied with host scheduling, so the allocation reduction is the stable conclusion; the wall-time result is a measured signal, not a promise. The baseline CPU profile attributed about 36.86% cumulative CPU to \`IPv4Decoder.Feed\` and its copy/growth path. The baseline allocation profile attributed 99.78% of allocation space to that method. The after profile still identifies the framer as hot, but the bounded-tail clone is gone. No heartbeat, SOCKS relay, DNS, gVisor channel, or observer serialization change was made without a comparative signal.

The current repeatable benchmark set also covers heartbeat construction, SOCKS payload accounting, and userspace initialization:

\`\`\`sh
make bench BENCHTIME=250ms BENCHCOUNT=5
make leak-check LEAKCOUNT=10
\`\`\`

The 250 ms sample on Linux amd64 measured \`BuildICMPHeartbeat\` at 239–272 ns, 80 bytes, one allocation; \`SOCKSPayloadAccounting\` at 1,503–1,536 ns, 1,528 bytes, four allocations; and \`NewUserspace\` at 478–503 µs, approximately 76.8–77.3 KiB, 521–522 allocations. These are baselines for a later, evidence-driven pass rather than optimization claims.

## Portable core and platform integration

The portable core is the Go userspace path in \`internal/core\`, \`internal/gatewayauth\`, \`internal/runtime\`, \`internal/sessiontoken\`, \`internal/traffic\`, and \`internal/nativeapp\`. Packaging, credentials, process supervision, UI, certificate policy, and OS service integration are separate host concerns.

| Capability | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Compile-only matrix (\`CGO_ENABLED=0\`) | amd64, arm64 | amd64, arm64; Keychain fails closed, while release builds enable native cgo | amd64, arm64 |
| Owner/mode checks | Implemented through \`syscall.Stat_t\` and \`euid\` | Implemented through \`syscall.Stat_t\` and \`euid\` | Blocked: \`owner_other.go\` deliberately rejects ownership; ACL implementation is not present |
| Credential storage | Explicitly opted-in plaintext 0600 file or hidden prompt; no Secret Service adapter | Login Keychain generic-password item through the Security framework; explicit migration preserves the legacy owner-only file | No supported secure file ownership/ACL or credential adapter |
| Stop/signals | CLI handles interrupt and \`SIGTERM\` | CLI handles interrupt and \`SIGTERM\` | Binary compiles, but service stop semantics and a Windows service host are not implemented |
| SOCKS exposure | Numeric IPv4 loopback only | Numeric IPv4 loopback only | Numeric IPv4 loopback only after the storage blocker is resolved |
| Certificate storage | Go system roots; no installed certificate manager | Go system roots; no Keychain/trust-store integration | Go system roots at compile level; runtime packaging policy is unvalidated |
| UI/service/package | Not present | Not present | Not present |

The current build tags are intentionally narrow: \`owner_unix.go\` applies only to Linux and macOS, while \`owner_other.go\` fails closed on other systems. The macOS Keychain adapter requires a cgo-enabled release build; the cgo-disabled Darwin variant compiles but fails closed if invoked. This makes a Windows build useful for API work without falsely claiming runtime release readiness. \`make build-platforms\` performs the compile-only matrix; it does not run a service or network connection.

Release blockers are therefore explicit: a Windows ownership/ACL implementation, native credential adapters for any additional shipping platforms, host-specific certificate/trust policy, packaging and service lifecycle adapters, and a production UI host. None should be solved by adding a TUN, routes, DNS, PF, or a vendor service to this core.

The release CLI has no worktree override. \`setup\`, \`connect\`, \`dry-run\`,
and \`doctor\` use only the operating-system user
configuration directory. \`migrate --from PATH\` is the explicit bridge from
the pre-release worktree \`.config\` layout. It validates and copies
configuration, imports a missing credential into the platform store, never
overwrites an existing destination, and preserves the source for rollback.

## Attended authentication and background runtime

Authentication and MFA remain attended. Hidden verification-code input uses a
raw terminal reader that treats Ctrl-C as cancellation, restores terminal
state on every return path, clears partial input, and exits before a native
runtime is created. The long-lived password and MFA code are never transferred
to a background process.

On Linux and macOS, \`connect --background\` performs authentication and
bootstrap in the foreground, then starts a detached copy of soundconnect for
the native runtime. The parent transfers only \`Config\`, \`DataplanePlan\`, the
typed 48-byte \`NativeGatewayToken\`, and the selected profile through an
anonymous inherited pipe. Token material is not placed in argv, environment,
or a persistent file and is cleared on both sides of the handoff. The child
acknowledges full connected readiness before the parent returns.
Sanitized runtime output is appended to the owner-only \`runtime.log\` beside
the active configuration. The foreground and detached Unix hosts also expose
one read-only runtime-status socket in an owner-only temporary directory.
\`soundconnect status\` reads a versioned, allowlisted snapshot from that live
authority; socket ownership and permissions are validated, stale sockets are
replaced only by a new runtime, and neither PID files nor logs are treated as
liveness evidence.

This is still a detached CLI host, not the final cross-platform service
authority. Windows background hosting remains unsupported until the Windows
service and named-pipe control boundary exists. A later host-integration phase
must add stop control and platform-native supervision without creating a
second runtime-state authority.

## Dependency and release boundary

\`THIRD_PARTY_NOTICES\` is the checked-in dependency notice index. It is separate from the proprietary \`LICENSE\`: the proprietary license does not grant rights to third-party materials. The release packager must ship the exact pinned upstream license texts and any applicable attributions alongside the binary; the index records the versions and obligations and is not permission to omit those texts.

The linked binary dependency set is derived from \`go list -deps\` and \`go version -m\`, not merely from the module graph. \`golang.org/x/text\` appears in the module graph but is not linked by \`cmd/soundconnect\` at this revision. No files under \`research/upstream/\` or \`research/work/\` are tracked or part of the product build.

## Gate

Before a release candidate, run the following from a clean worktree and retain the output with the build artifact:

\`\`\`sh
gofmt -l $(find cmd internal -type f -name '*.go' -print)
go mod verify
go test ./...
go test -race ./...
go vet ./...
make fmt-check
make build
make build-platforms
git diff --check
\`\`\`

For standalone macOS CLI archives, \`make cli-release VERSION=vMAJOR.MINOR.PATCH\`
requires a clean worktree, builds cgo-enabled amd64 and arm64 binaries with the
version injected, includes the license, notice index, and linked dependency
license texts, and writes SHA-256 checksums. Signing, notarization, tag creation,
and publication remain separate release gates.

The commands are compile/test gates only. They do not authenticate, perform MFA, start a vendor service, use a VPN/TUN, alter routing/DNS/PF, push, or release.
