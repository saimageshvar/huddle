import SwiftUI

/// Pane layout shared by every call: the split is remembered for the app, not per call (mock 8).
final class LayoutState: ObservableObject {
    static let shared = LayoutState()
    static let defaultRatio = 0.56
    static let questionMin: CGFloat = 320
    static let chatMin: CGFloat = 300
    static let strip: CGFloat = 52
    /// How far past a pane's minimum the divider must be dragged, and released, before that pane collapses.
    static let collapseSlack: CGFloat = 120

    @Published var ratio: Double
    /// Which pane would collapse if the drag ended now: -1 question, 1 chat, 0 none.
    @Published var pendingCollapse = 0
    /// Divider x when the current drag began; unpublished so it never triggers a redraw.
    var dragStart: CGFloat?
    @Published var chatCollapsed = false
    @Published var questionCollapsed = false
    @Published var zoomed = false

    init() {
        let r = UserDefaults.standard.double(forKey: "split")
        ratio = r > 0 ? r : Self.defaultRatio
    }

    func save() { UserDefaults.standard.set(ratio, forKey: "split") }
    func reset() { ratio = Self.defaultRatio; chatCollapsed = false; questionCollapsed = false; save() }
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
        .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .global)
            .onChanged { v in drag(v.translation.width) }
            .onEnded { _ in end() })
        .focusable()
        .focusEffectDisabled()
        .onKeyPress(.leftArrow) { nudge(-0.02); return .handled }
        .onKeyPress(.rightArrow) { nudge(0.02); return .handled }
    }

    /// Anchored to where the drag began, so the moving handle never feeds back into its own position.
    func drag(_ dx: CGFloat) {
        let lo = LayoutState.questionMin, hi = total - LayoutState.chatMin, slack = LayoutState.collapseSlack
        if layout.dragStart == nil { layout.dragStart = min(max(CGFloat(layout.ratio) * total, lo), hi) }
        let x = layout.dragStart! + dx
        var t = Transaction(); t.disablesAnimations = true
        withTransaction(t) {
            layout.ratio = Double(min(max(x, lo), hi) / total)
            let p = x < lo - slack ? -1 : x > hi + slack ? 1 : 0
            if p != layout.pendingCollapse { layout.pendingCollapse = p }
        }
    }

    func end() {
        layout.dragStart = nil
        switch layout.pendingCollapse {
        case -1: layout.questionCollapsed = true
        case 1: layout.chatCollapsed = true
        default: break
        }
        layout.pendingCollapse = 0
        layout.save()
    }

    func nudge(_ d: Double) {
        let lo = Double(LayoutState.questionMin / total), hi = Double(1 - LayoutState.chatMin / total)
        layout.ratio = min(max(layout.ratio + d, lo), hi)
        layout.save()
    }
}

/// A collapsed pane's strip: click it, or drag it toward the pane's side, to bring the pane back.
struct PaneStrip: View {
    let icon: String
    let badge: String?
    let caption: String?
    /// +1 when dragging right reopens the pane (question strip on the left), -1 for the chat strip on the right.
    let opens: CGFloat
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
            .contentShape(Rectangle())
        }.buttonStyle(.plain).background(T.faint)
        .help(opens > 0 ? "Click or drag right to bring the question back" : "Click or drag left to bring the chat back")
        .simultaneousGesture(DragGesture(minimumDistance: 8).onEnded { v in
            if v.translation.width * opens > 20 { action() }
        })
    }
}
