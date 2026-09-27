# nju-connect macOS app

A SwiftUI menu-bar extra over the bundled `nju-connect` CLI. The app owns
presentation only: every credential, protocol request and runtime lives in the
CLI, which the app runs as child processes (see
[`docs/architecture.md`](../docs/architecture.md) and
[`docs/cli-contract.md`](../docs/cli-contract.md)).

- `NJUConnectUI` — the menu-bar panel (`nju-connect-menu`).
- `ATrustOAuthHelper` — an isolated, persistent WebKit window for aTrust
  browser sign-in. It intercepts the gateway callback before the page loads and
  hands only the authorization code to the CLI through a local pipe;
  `nju-connect logout` runs it with `--clear-data`.

## Panel

The panel is 292pt wide and stacks fixed sections top to bottom:

1. **Status** — a status dot, the product name, the connection state, and the
   local SOCKS5 port while connected (its tooltip names the gateway).
2. **VPN** — a native segmented control: Off, EasyConnect, aTrust. Choosing a
   backend while another runs stops the runtime first, then starts the new
   one; both share the CLI's SOCKS5 listener and Keychain password.
3. **speed.nju.edu.cn** — always one fixed-height row, in every state, so the
   sections below never move it. Its icon reports campus reachability and
   latency, not VPN state. Clicking it opens the speed-test inspector.
4. **Current task** — first-run account and password, a verification-code
   field, a retry or credential-reset row, or nothing.
5. **Live traffic** — while connected or reconnecting: a 30-second chart
   (download filled, upload as a line), current rates, session totals and
   active connections.

The accent color is the project plum (`Color.brand`, the page's `--plum`);
status colors are system green, orange and red.

### Speed test

`--route auto` probes the direct campus path first and falls back to the
nju-connect SOCKS5 path only when the direct path fails, so a failed VPN
sign-in does not disable the speed test on campus. Each time the panel opens
and the last sample is older than 10 seconds, the app takes three latency
probes 400 ms apart; 200 ms or more shows the amber "slow" icon. The inspector
shows the last ten latency samples and the last bandwidth run, and keeps them
in the app's preferences across launches. The client's public IP is never
stored.

## Develop

```sh
make macos-preview   # panel in a window with simulated states
make macos-dev       # signed build against the fresh CLI
swift test --package-path macos
```

The preview window's State and Speed test menus switch between every panel
state without a VPN. The UI is English only.

## Packaging

`scripts/package_macos_release.zsh` builds a Cask ZIP with the app and a
universal CLI. The LibreSpeed helper is a separate Homebrew Formula that the
app neither embeds nor downloads. The app is ad-hoc signed and not notarized;
the Cask and release notes state this.
