# sing-box integration

`campus.json` is a native sing-box configuration fragment that routes campus
TCP traffic to SoundConnect's SOCKS5 listener at `127.0.0.1:1081`.

Merge this fragment into a configuration that already defines:

- a TUN inbound tagged `desktop-tun`;
- an outbound tagged `direct`; and
- DNS handling compatible with the `hijack-dns` rule.

The `campus` selector defaults to `direct`. Select `campus-local-socks` after
SoundConnect is connected and listening on port 1081. The VPN gateway
`vpn.nju.edu.cn` remains direct to prevent the tunnel from routing through
itself.

This fragment supports TCP only because SoundConnect exposes a SOCKS5 TCP
CONNECT listener. It does not require or define an additional port.

The matching policy is mirrored from the public campus module maintained in
the `soundadam-upload/sing-box-config` repository. Keep both copies aligned
when the campus address list or integration contract changes.
