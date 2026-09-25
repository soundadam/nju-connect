# aTrust dual backend

SoundConnect supports two mutually exclusive campus VPN backends behind one
application: EasyConnect (SoundConnect's own implementation) and aTrust
(through the pinned AGPL-3.0 `mythologyli/zju-connect` client). This document
describes the backend boundary on `main` and how the aTrust protocol core is
supplied. The earlier clean-room plan (`docs/atrust-cleanroom.md`) is
superseded; `docs/protocol/atrust.md` is kept as a maintenance reference.

## Boundary

```text
internal/backend
├── contract.go            -> backend names, catalog, Endpoint, Discoverer, Session
├── easyconnect/auth       -> EasyConnect authentication
├── easyconnect/session    -> EasyConnect native session
└── atrust                 -> SoundConnect-owned aTrust seam (no wire protocol)
```

Protocol-specific authentication material, resource formats, tunnel keys, and
wire state do not cross this boundary. The shared application surface is
lifecycle-only: `Run(context.Context)` and `Close()`.

`backend.ATrustEndpoint` keeps `vpn.nju.edu.cn` as the gateway identity (TLS
name and OAuth callback host) while dialing `219.219.118.20`, because public
DNS still resolves that name to the EasyConnect appliance. Saved profiles that
name the retired `ztna.nju.edu.cn` gateway are redirected.

## What `main` contains

- Backend selection in config (`backend`, `auth_type`, `login_domain`) with an
  EasyConnect default for existing profiles.
- CLI: `backends`, `configure`, `setup --backend`, `auth-info`, `logout`, and
  the aTrust `connect` path; sanitized `atrust-tcp` runtime status.
- The shared Keychain password (EasyConnect and aTrust `auth/psw`) and a
  separate Keychain item for opaque aTrust client data.
- The aTrust seam in `internal/backend/atrust`:
  - `Core` — `Discover`, `Authenticate(ctx, LoginRequest, Prompter)`, and
    `Resume(ctx, ResumeRequest)`;
  - `Session` — `ClientData`, `Resources`, `OpenTunnel`, `Logout`, `Close`;
  - `Tunnel` — `DialTCP(ctx, *net.TCPAddr)`, `Run`, `Close`;
  - `Prompter` — password, verification code, captcha image, and OAuth code
    callbacks implemented by the host;
  - `Resources` — IP ranges, domain rules, and DNS overrides with port ranges
    and protocol, plus matching;
  - `Router` and `Connect`/`Connection` — the shared SOCKS listener, domain
    and address resource routing, direct fallback for non-resources, DNS
    overrides, traffic counters, and lifecycle;
  - `ParseOAuthCallbackCode` — local callback validation.
- The protocol core (`NewCore`, `zjuconnect.go`), an adapter over
  `github.com/mythologyli/zju-connect` pinned in `go.mod`.
- The macOS backend switch and the `soundconnect-atrust-oauth-helper` WebKit
  login helper.

## Protocol core

`NewCore` adapts the upstream `client/atrust` package to `Core`:

- `Discover` calls the upstream public `authConfig` discovery against
  `Endpoint.DialHost()`, so the NJU gateway pin (`vpn.nju.edu.cn` dialed at
  `219.219.118.20`, see `backend.ATrustEndpoint`) applies to every request.
- `Authenticate` collects the password or the OAuth authorization code through
  the `Prompter` before calling the upstream `Setup`. The upstream client reads
  later factors (SMS code, captcha answer) with `fmt.Scanln` after logging a
  prompt; the adapter installs a scoped bridge that replaces `os.Stdin` with a
  pipe and watches the standard logger, answering each prompt through the
  `Prompter`. The bridge is process-wide, so upstream logins are serialized,
  and it stays installed until `Setup` returns even after cancellation.
- `Resume` runs `Setup` with the saved client data and no authentication type.
  Any rejection that is not a network or cancellation error is reported as
  `ErrSessionExpired`, and every prompt is refused.
- `Session.Resources` translates upstream IP, domain, and DNS resources into
  `Resources`, dropping individual entries that cannot be represented.
  Upstream domain resources always cover their subdomains, with a dot
  boundary.
- `Tunnel.DialTCP` passes the original SOCKS domain and its upstream domain
  resource to the upstream TCP tunnel so the node receives the name.

Known deviations from the `Core` contract, inherited from the upstream client:
it dials with its own interface-bound dialer (the `DialFunc`, and therefore a
configured upstream proxy, is ignored), it does not verify gateway or node
certificates, and it has no gateway logout.

Acceptance is an attended NJU test: password plus SMS, resume, resource
routing through the shared SOCKS listener to `172.21.0.1`, DNS overrides,
logout, and shutdown.

## Open gates

- Certificate verification and upstream-proxy support in the protocol core
  (both require changes to the pinned upstream client).
- Background hosting for aTrust (the CLI currently refuses
  `connect --background` for aTrust and the macOS host runs it in the
  foreground).
- Graphical captcha presentation in the CLI and GUI (the CLI `Prompter`
  reports the factor as unavailable).
- Gateway-side logout from `soundconnect logout` (it currently clears local
  client data and the OAuth helper profile only).
- UDP/L3 and IPv6 resource paths.
- Release signing of the OAuth helper and a callback acceptance test.
