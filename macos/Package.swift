// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "soundconnect_macos",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .executable(name: "soundconnect-menu", targets: ["soundconnect_ui"]),
        .executable(name: "soundconnect-atrust-oauth-helper", targets: ["atrust_oauth_helper"]),
    ],
    targets: [
        .executableTarget(
            name: "soundconnect_ui",
            path: "Sources/SoundConnectUI"
        ),
        .testTarget(
            name: "SoundConnectUITests",
            dependencies: ["soundconnect_ui"],
            path: "Tests/SoundConnectUITests"
        ),
        .executableTarget(
            name: "atrust_oauth_helper",
            path: "Sources/ATrustOAuthHelper"
        ),
    ]
)
