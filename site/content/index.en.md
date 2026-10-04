---
title: nju-connect
aliases:
  - /projects/soundconnect/
excerpt: A Nanjing University campus VPN client that leaves system routing alone and opens one local SOCKS5 port.
weight: 20
presentation:
  category: Campus network / CLI + macOS
  label: Install with Homebrew
  command: brew install --cask soundadam/tap/nju-connect
  note: No route changes. No DNS changes. No background service.
registry:
  github: soundadam/nju-connect
  docs: https://github.com/soundadam/nju-connect#readme
release:
  version: "1.1.1"
  license: AGPL-3.0
modules:
  - type: promo
    description: >-
      Connect to the NJU campus VPN and keep the system network as it was: the only thing a connection adds is a local SOCKS5 port, `127.0.0.1:1081`. Point an app at it when that app needs the campus network.
    line: >-
      {title} {version} is open source under {license}-or-later and speaks both EasyConnect and aTrust gateways. macOS gets a menu bar app and a command line; Linux and Windows get the command line.
    actions:
      - label: Install guide (Chinese)
        url: "{docs}"
      - label: GitHub
        url: "https://github.com/{github}"

  - type: snapshot
    title: Why not the official client
    body: >-
      Sangfor's EasyConnect and aTrust clients take over the whole machine once connected: they rewrite the routing table and DNS and leave a service running in the background. Every connection on the machine follows the VPN, which tends to collide with proxies, Tailscale and Docker networks. {title} does the opposite: it opens one stable SOCKS5 port on the loopback address, never touches routes, DNS or the firewall, and is gone when it exits.
    note: >-
      Not affiliated with Nanjing University or Sangfor. The macOS app is ad-hoc signed and not notarized by Apple.
    metrics:
      - value: SOCKS5
        label: "One port: `127.0.0.1:1081`"
      - value: "0"
        label: Route changes · DNS changes · system services
      - value: "2"
        label: "Gateways: EasyConnect and aTrust"
      - value: "{version}"
        label: "Current release · {license}"

  - type: section
    title: Only the traffic you choose goes to campus
    index: 1/3
    lede: >-
      Whichever browser, terminal or download tool needs the library databases or a campus service gets `127.0.0.1:1081`; everything else stays direct, and existing proxies and overlay networks are unaffected. The menu bar shows connection state and live traffic, and runs a campus speed test in one click.

  - type: media
    image: menubar.svg
    alt: The nju-connect macOS menu bar panel, connected, showing the SOCKS5 endpoint, a campus speed test result and the last 30 seconds of traffic
    caption: "The menu bar app: connect switch, SOCKS5 endpoint, `speed.nju.edu.cn` campus speed test and live traffic."

  - type: section
    title: The command line and the menu bar are one program
    index: 2/3
    lede: >-
      `nju-connect setup` walks through gateway, account and password; `nju-connect connect` connects, and EasyConnect can move to the background with `--background`. `status --json` gives scripts stable machine-readable output, and `speedtest campus` measures the campus link directly. The menu bar app calls this same command line; there is no second implementation.

  - type: media
    image: terminal.svg
    alt: nju-connect in a terminal, with status showing a connection and traffic counters and speedtest campus printing a campus speed test result
    caption: "`nju-connect status` and `nju-connect speedtest campus`."

  - type: section
    title: Passwords and sessions stay on the machine
    index: 3/3
    lede: >-
      The long-term password and the aTrust session live in owner-only (`0600`) files in the config directory, never in command-line arguments, environment variables or logs; an SMS code is used for that one login only. Status queries go over a local socket only you can reach, and output is redacted before it is shown.
---
