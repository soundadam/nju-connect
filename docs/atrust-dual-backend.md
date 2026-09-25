# aTrust dual-backend plan

SoundConnect supports two mutually exclusive campus VPN backends behind one
application: EasyConnect (implemented) and aTrust (selectable; protocol core
pending). This document describes the backend boundary on `main` and the
clean-room plan for the aTrust protocol core. The information barriers that
govern who may author that core are defined in `docs/atrust-cleanroom.md`.

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
- A placeholder core (`NewCore`) whose operations fail with
  `ErrProtocolNotImplemented`. `connect` with the aTrust backend therefore
  exits 1 with "aTrust protocol support is not available in this build".
- The macOS backend switch and the `soundconnect-atrust-oauth-helper` WebKit
  login helper.

## Clean-room plan

1. **Capture.** A capture author, who has not read zju-connect source, records
   the aTrust client's observable behavior against the NJU gateway and writes
   `docs/protocol/atrust.md` with redacted fixtures (see
   `docs/atrust-cleanroom.md` for the method and requirements).
2. **Implementation.** An implementer who has read only the specification,
   the fixtures, and `main` writes a `Core` in `internal/backend/atrust` and
   replaces `NewCore`. It must:
   - verify the gateway certificate against `Endpoint.Host` while dialing
     `Endpoint.DialHost()` through the supplied `DialFunc`;
   - request every interactive factor through the `Prompter`;
   - return `ErrSessionExpired` from `Resume` when saved client data is
     rejected, so the lifecycle falls back to an interactive login;
   - translate gateway resources into `Resources` and pass `Validate`;
   - be tested against the redacted fixtures and a local fake gateway.
3. **Acceptance.** An attended NJU test covering OAuth, password plus SMS,
   resume, resource routing through the shared SOCKS listener, DNS overrides,
   reconnect, logout, and shutdown.

## Open gates

- Background hosting for aTrust (the CLI currently refuses
  `connect --background` for aTrust and the macOS host runs it in the
  foreground).
- Graphical captcha presentation in the CLI and GUI (the CLI `Prompter`
  reports the factor as unavailable).
- Gateway-side logout from `soundconnect logout` (it currently clears local
  client data and the OAuth helper profile only).
- UDP/L3 and IPv6 resource paths.
- Release signing of the OAuth helper and a callback acceptance test.
