import SwiftUI

/// Pane layout shared by every call: the split is remembered for the app, not per call (mock 8).
final class LayoutState: ObservableObject {
    static let shared = LayoutState()
    static let defaultRatio = 0.56
    static let questionMin: CGFloat = 320
    static let chatMin: CGFloat = 300
    static let strip: CGFloat = 52

    @Published var ratio: Double { didSet { UserDefaults.standard.set(ratio, forKey: "split") } }
    @Published var chatCollapsed = false
    @Published var questionCollapsed = false
    @Published var zoomed = false

    init() {
        let r = UserDefaults.standard.double(forKey: "split")
        ratio = r > 0 ? r : Self.defaultRatio
    }

    func reset() { ratio = Self.defaultRatio; chatCollapsed = false; questionCollapsed = false }
}

/// Small flag object: this SDK's @State is a macro the Command Line Tools cannot expand.
final class Flag: ObservableObject {
    @Published var on = false
    init(_ on: Bool = false) { self.on = on }
}

/// The draggable divider between the question and the chat (mock 8): drag, arrow keys, double-click to reset.
struct SplitHandle: View {
    @ObservedObject var layout = LayoutState.shared
    let total: CGFloat
    @ObservedObject var hover = Flag()

    var body: some View {
        ZStack {
            Rectangle().fill(T.border).frame(width: 1)
            Capsule().fill(hover.on ? T.ink.opacity(0.5) : T.border).frame(width: 4, height: 36)
            if hover.on {
                Text("↔ Drag to resize · double-click resets").font(.caption).lineLimit(1).fixedSize()
                    .padding(.horizontal, 10).padding(.vertical, 6)
                    .foregroundColor(T.inkText).background(RoundedRectangle(cornerRadius: 8).fill(T.ink))
                    .offset(y: 40).allowsHitTesting(false)
            }
        }
        .frame(width: 12)
        .contentShape(Rectangle())
        .onHover { hover.on = $0 }
        .onTapGesture(count: 2) { layout.reset() }
        .gesture(DragGesture(minimumDistance: 2, coordinateSpace: .named("split")).onChanged { v in drag(to: v.location.x) })
        .focusable()
        .focusEffectDisabled()
        .onKeyPress(.leftArrow) { nudge(-0.02); return .handled }
        .onKeyPress(.rightArrow) { nudge(0.02); return .handled }
    }

    func drag(to x: CGFloat) {
        let q = LayoutState.questionMin, c = LayoutState.chatMin
        if x < q - 40 { layout.questionCollapsed = true; layout.chatCollapsed = false; return }
        if total - x < c - 40 { layout.chatCollapsed = true; layout.questionCollapsed = false; return }
        layout.ratio = Double(min(max(x, q), total - c) / total)
    }

    func nudge(_ d: Double) {
        let lo = Double(LayoutState.questionMin / total), hi = Double(1 - LayoutState.chatMin / total)
        layout.ratio = min(max(layout.ratio + d, lo), hi)
    }
}

/// A pane squeezed below its minimum snaps to this strip; clicking it brings the pane back.
struct PaneStrip: View {
    let icon: String
    let badge: String?
    let caption: String?
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            VStack(spacing: 10) {
                Image(systemName: icon).font(.title3)
                if let c = caption { Text(c).font(.callout) }
                if let b = badge {
                    Text(b).font(.caption).padding(.horizontal, 7).padding(.vertical, 2)
                        .overlay(Capsule().stroke(T.border))
                }
                Spacer()
            }.padding(.top, 16).frame(width: LayoutState.strip).frame(maxHeight: .infinity)
        }.buttonStyle(.plain).background(T.faint)
    }
}
