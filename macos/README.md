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
   one; both share the CLI's SOCKS5 listener and saved password.
3. **speed.nju.edu.cn** — always one fixed-height row, in every state, so the
   sections below never move it. Its dot reports whether speed.nju.edu.cn
   answers over the path in use: green, amber when slow, red when
   unreachable, a hollow ring before the first probe, and a spinner while a
   fresh verdict is pending. Clicking it opens the speed-test inspector, which
   uses the panel's own header, section and figure styles.
4. **Current task** — first-run account and password, a verification-code
   field, a retry or credential-reset row, or nothing.
5. **Live traffic** — while connected or reconnecting: a 30-second chart
   (download filled, upload as a line) over three equal columns — download
   and upload rates with session totals, and active connections.

The menu-bar item is one SF Symbol template image whose shape follows the
state: a checkmark shield when connected, a half-filled shield that pulses
while connecting (macOS 14+), an exclamation shield when something needs
attention, and a slashed shield when off. Sections below the speed row fade
in and out; the switch and the speed row never move.

The accent color is the project plum (`Color.brand`, the page's `--plum`);
status colors are system green, orange and red.

### Speed test

Latency probes follow the VPN. While it is connected they use
`--route nju-connect`, so a tunnel whose session has ended turns the dot red
even when the direct path would still answer; otherwise they use
`--route auto`, which tries the direct campus path first. A bandwidth run
always uses `--route auto`, so a failed VPN sign-in does not disable the speed
test on campus.

A probe round is three probes 400 ms apart. One runs when the panel opens and
the last round is older than 10 seconds, every 30 seconds while it stays open,
and at once whenever the VPN connects or stops being connected; that last one
discards the old verdict first. The dot reflects the median of the latest
round only (the slow threshold and its reason sit next to
`SpeedTestController.slowLatencyMs`). The inspector shows the last ten latency
samples and the last bandwidth run, and keeps them in the app's preferences
across launches; the verdict itself is never restored. The client's public IP
is never stored.

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
