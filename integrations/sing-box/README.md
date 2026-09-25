# sing-box integration

`campus.json` is a native sing-box configuration fragment that routes campus
TCP traffic to SoundConnect's shared loopback SOCKS5 listener.

Merge this fragment into a configuration that already defines:

- a TUN inbound tagged `desktop-tun`;
- an outbound tagged `direct`; and
- DNS handling compatible with the `hijack-dns` rule.

The `campus` selector defaults to `direct`. Select `campus-local-socks` after
either backend is connected and listening on the configured port, which
defaults to 1081. Both VPN gateways, `vpn.nju.edu.cn` and `ztna.nju.edu.cn`,
remain direct to prevent either backend from routing through the campus SOCKS
outbound while its native session is being established. aTrust tunnel nodes on
TCP port 441 within `219.219.112.0/20` also remain direct because their addresses
are discovered after authentication and may be used as bare IPs.

This fragment supports TCP only because SoundConnect exposes SOCKS5 TCP CONNECT
listeners. Both mutually exclusive backends share the CLI-owned listener
setting; only select it while a runtime is active.

## Smoke test before SFM

SFM does not need to be involved while validating the aTrust listener. After
`soundconnect connect` reports `socks_listen: 127.0.0.1:1081`, verify the
listener and an allowed SSH resource directly:

```sh
lsof -nP -iTCP:1081 -sTCP:LISTEN
ssh -o ControlMaster=no -o ControlPath=none \
  -o ProxyCommand='nc -x 127.0.0.1:1081 -X 5 %h %p' \
  elon true
```

The SSH command must exit successfully; a listening socket alone is not proof
that the selected backend's resource tunnel works. If port 1081 is absent, the
runtime has stopped, so do not select the campus outbound in SFM yet. The SFM
outbound does not change when SoundConnect switches backend.

The matching policy is mirrored from the public campus module maintained in
the `soundadam-upload/sing-box-config` repository. Keep both copies aligned
when the campus address list or integration contract changes.
