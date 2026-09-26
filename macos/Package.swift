// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "nju_connect_macos",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .executable(name: "nju-connect-menu", targets: ["nju_connect_ui"]),
        .executable(name: "nju-connect-atrust-oauth-helper", targets: ["atrust_oauth_helper"]),
    ],
    targets: [
        .executableTarget(
            name: "nju_connect_ui",
            path: "Sources/NJUConnectUI"
        ),
        .testTarget(
            name: "NJUConnectUITests",
            dependencies: ["nju_connect_ui"],
            path: "Tests/NJUConnectUITests"
        ),
        .executableTarget(
            name: "atrust_oauth_helper",
            path: "Sources/ATrustOAuthHelper"
        ),
    ]
)
