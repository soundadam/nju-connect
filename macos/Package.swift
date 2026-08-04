// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "soundconnect_macos",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .executable(name: "soundconnect-menu", targets: ["soundconnect_ui"]),
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
    ]
)
