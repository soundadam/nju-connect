import SwiftUI

/// An original, unofficial placeholder for the menu bar.
///
/// The abstract shield and gate geometry deliberately avoids an institutional
/// emblem, wordmark, lettering, or official graphic details.
struct MenuBarStatusIcon: View {
    let state: MenuBarIconState

    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            UnofficialShieldGatePlaceholder()
                .stroke(
                    .primary,
                    style: StrokeStyle(lineWidth: 1.35, lineCap: .round, lineJoin: .round)
                )
                .frame(width: 15, height: 16)
                .frame(width: 17, height: 18, alignment: .leading)

            MenuBarStateBadge(state: state)
                .fill(.primary, style: FillStyle(eoFill: true))
                .frame(width: 4.25, height: 4.25)
                .padding(.trailing, 0.1)
                .padding(.bottom, 0.8)
        }
        .frame(width: 18, height: 18)
        .fixedSize()
        .accessibilityElement(children: .ignore)
    }
}

private struct UnofficialShieldGatePlaceholder: Shape {
    func path(in rect: CGRect) -> Path {
        func point(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
            CGPoint(x: rect.minX + rect.width * x, y: rect.minY + rect.height * y)
        }

        var path = Path()

        path.move(to: point(0.50, 0.03))
        path.addLine(to: point(0.93, 0.18))
        path.addLine(to: point(0.87, 0.62))
        path.addCurve(
            to: point(0.50, 0.97),
            control1: point(0.84, 0.79),
            control2: point(0.66, 0.91)
        )
        path.addCurve(
            to: point(0.13, 0.62),
            control1: point(0.34, 0.91),
            control2: point(0.16, 0.79)
        )
        path.addLine(to: point(0.07, 0.18))
        path.closeSubpath()

        path.move(to: point(0.24, 0.68))
        path.addLine(to: point(0.24, 0.43))
        path.move(to: point(0.76, 0.68))
        path.addLine(to: point(0.76, 0.43))
        path.move(to: point(0.36, 0.69))
        path.addLine(to: point(0.36, 0.49))
        path.addCurve(
            to: point(0.50, 0.35),
            control1: point(0.36, 0.41),
            control2: point(0.42, 0.35)
        )
        path.addCurve(
            to: point(0.64, 0.48),
            control1: point(0.58, 0.35),
            control2: point(0.64, 0.41)
        )
        path.addLine(to: point(0.64, 0.69))

        return path
    }
}

private struct MenuBarStateBadge: Shape {
    let state: MenuBarIconState

    func path(in rect: CGRect) -> Path {
        let outer = rect.insetBy(dx: rect.width * 0.10, dy: rect.height * 0.10)
        var path = Path()

        switch state {
        case .connected:
            path.addEllipse(in: outer)
        case .inProgress:
            path.addEllipse(in: outer)
            path.addEllipse(in: rect.insetBy(dx: rect.width * 0.32, dy: rect.height * 0.32))
        case .needsAttention:
            path.move(to: CGPoint(x: rect.midX, y: outer.minY))
            path.addLine(to: CGPoint(x: outer.maxX, y: rect.midY))
            path.addLine(to: CGPoint(x: rect.midX, y: outer.maxY))
            path.addLine(to: CGPoint(x: outer.minX, y: rect.midY))
            path.closeSubpath()
        case .inactive:
            path.addRoundedRect(
                in: CGRect(
                    x: outer.minX,
                    y: rect.midY - rect.height * 0.11,
                    width: outer.width,
                    height: rect.height * 0.22
                ),
                cornerSize: CGSize(width: rect.height * 0.11, height: rect.height * 0.11)
            )
        }

        return path
    }
}
