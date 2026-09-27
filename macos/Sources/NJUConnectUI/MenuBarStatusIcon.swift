import SwiftUI

/// The menu-bar item: one SF Symbol, drawn as a template image so macOS tints
/// it for the menu bar, with a different shape for each state so it reads
/// without color.
struct MenuBarStatusIcon: View {
    let state: MenuBarIconState

    var body: some View {
        let image = Image(systemName: state.symbolName)
        if #available(macOS 14.0, *) {
            image.symbolEffect(.pulse, isActive: state == .inProgress)
        } else {
            image
        }
    }
}

extension MenuBarIconState {
    var symbolName: String {
        switch self {
        case .connected: return "checkmark.shield.fill"
        case .inProgress: return "shield.lefthalf.filled"
        case .needsAttention: return "exclamationmark.shield.fill"
        case .inactive: return "shield.slash"
        }
    }
}
