import AppKit
import SwiftUI

extension Color {
    /// The nju-connect plum, shared with the project page (docs/index.html
    /// `--plum`). Light and dark values follow the system appearance.
    static let brand = Color(nsColor: NSColor(name: nil) { appearance in
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(srgbRed: 0xC8 / 255, green: 0x94 / 255, blue: 0xD4 / 255, alpha: 1)
            : NSColor(srgbRed: 0x5E / 255, green: 0x1A / 255, blue: 0x6B / 255, alpha: 1)
    })
}

extension DesignModel {
    /// The one status color the header dot, status symbols and menu share.
    var statusTint: Color {
        if showsCredentialSetup {
            return .orange
        }
        switch phase {
        case .connected: return .green
        case .waitingMFA, .connecting, .reconnecting: return .orange
        case .degraded: return .red
        case .stopped: return .secondary
        }
    }
}
