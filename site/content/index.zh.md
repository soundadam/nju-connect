---
title: nju-connect
aliases:
  - /projects/soundconnect/
excerpt: 南京大学校园 VPN 客户端：不动系统路由，只在本机开一个 SOCKS5 端口。
weight: 20
presentation:
  category: 校园网络 / CLI + macOS
  label: 用 Homebrew 安装
  command: brew install --cask soundadam/tap/nju-connect
  note: 不改路由表，不改 DNS，不装常驻服务。
registry:
  github: soundadam/nju-connect
  docs: https://github.com/soundadam/nju-connect#readme
release:
  version: "1.1.1"
  license: AGPL-3.0
modules:
  - type: promo
    description: 连上南大校园 VPN，但系统网络保持原样：连接之后只多出一个本机 SOCKS5 端口 `127.0.0.1:1081`，哪个应用要走校园网，就让哪个应用指向它。
    line: "{title} {version} 以 {license}-or-later 开源，同时支持 EasyConnect 与 aTrust 两种网关。macOS 有菜单栏应用加命令行，Linux 与 Windows 有命令行。"
    actions:
      - label: 安装说明
        url: "{docs}"
      - label: GitHub
        url: "https://github.com/{github}"

  - type: snapshot
    title: 为什么不用官方客户端
    body: 深信服的 EasyConnect / aTrust 客户端连上之后会接管整机网络：改写系统路由表和 DNS，并在后台常驻服务。整台机器的流量走向跟着 VPN 变，容易和代理软件、Tailscale、Docker 网络冲突。{title} 反过来做：它只在本机回环地址上开一个稳定的 SOCKS5 端口，系统路由、DNS 和防火墙一律不碰，退出即结束。
    note: 与南京大学、深信服均无隶属关系。macOS 应用为 ad-hoc 签名，未经 Apple 公证。
    metrics:
      - value: SOCKS5
        label: "只开一个端口：`127.0.0.1:1081`"
      - value: "0"
        label: 系统路由改动 · DNS 改动 · 系统服务
      - value: "2"
        label: 网关：EasyConnect 与 aTrust
      - value: "{version}"
        label: "当前版本 · {license}"

  - type: section
    title: 只有你指定的流量走校园网
    index: 1/3
    lede: 浏览器、终端、下载工具，谁要访问图书馆数据库或校内服务，就给谁配上 `127.0.0.1:1081`；其余流量照旧直连，已有的代理和组网工具不受影响。菜单栏里能看到连接状态、上下行流量，一键跑校园测速。

  - type: media
    image: menubar.svg
    alt: nju-connect macOS 菜单栏面板：校园 VPN 已连接，显示 SOCKS5 端点、校园测速结果与最近 30 秒上下行流量
    caption: 菜单栏应用：连接开关、SOCKS5 端点、`speed.nju.edu.cn` 校园测速与实时流量。

  - type: section
    title: 命令行和菜单栏是同一个程序
    index: 2/3
    lede: "`nju-connect setup` 引导填写网关、账号和密码；`nju-connect connect` 连接，EasyConnect 可以 `--background` 转入后台。`status --json` 给脚本稳定的机读输出，`speedtest campus` 直测校园链路。菜单栏应用调用的就是这个命令行，没有第二套逻辑。"

  - type: media
    image: terminal.svg
    alt: 终端里的 nju-connect：status 显示已连接与流量计数，speedtest campus 输出校园测速结果
    caption: "`nju-connect status` 与 `nju-connect speedtest campus`。"

  - type: section
    title: 密码和会话只在本机
    index: 3/3
    lede: 长期密码和 aTrust 会话存在配置目录里仅本人可读（`0600`）的文件中，不写进命令行参数、环境变量或日志；短信验证码只在当次登录使用。状态查询走仅本人可访问的本地 socket，输出先脱敏再显示。
---
