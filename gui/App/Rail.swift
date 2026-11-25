import SwiftUI

/// Zoomed chat: the inbox shrinks to a rail of one tile per call (mock 7).
struct Rail: View {
    @ObservedObject var store: Store
    @ObservedObject var layout = LayoutState.shared
    var body: some View {
        VStack(spacing: 10) {
            Button { layout.zoomed = false } label: {
                Image(systemName: "tray").frame(width: 44, height: 40)
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(T.border))
            }.buttonStyle(.plain).help("Back to the inbox")
            ForEach(store.calls) { c in Tile(store: store, call: c) }
            Spacer()
        }.padding(.top, 14).frame(width: 64).background(T.faint)
    }
}

struct Tile: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel
    @ObservedObject var hover = Flag()
    var body: some View {
        let selected = store.selected == call.id
        let left = store.remaining(call)
        let sub: String = call.state == .expired ? "Expired" : call.state == .cancelled ? "Cancelled" : (left < 300 ? "▲ " : "") + clock(left)
        VStack(spacing: 2) {
            Text(String(call.sessionName.prefix(1)).uppercased()).font(.title3.bold())
            Text(sub).font(.system(size: 10).monospacedDigit())
        }
        .frame(width: 48, height: 52)
        .foregroundColor(call.live ? T.ink : T.muted)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .textBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10)
            .stroke(selected ? T.ink : T.border, style: StrokeStyle(lineWidth: selected ? 2 : 1, dash: call.live ? [] : [4, 3])))
        .contentShape(Rectangle())
        .onTapGesture { store.selected = call.id }
        .onHover { hover.on = $0 }
        .overlay(alignment: .topLeading) {
            if hover.on {
                VStack(alignment: .leading, spacing: 2) {
                    Text(call.sessionName).fontWeight(.semibold)
                    Text("\(call.repo) · \(call.request.branch) · \(call.shortId)")
                    Text("Hover or focus a tile for its identity.").foregroundColor(.white.opacity(0.6))
                }
                .font(.callout).padding(10).fixedSize()
                .foregroundColor(.white).background(RoundedRectangle(cornerRadius: 8).fill(Color.black.opacity(0.88)))
                .offset(x: 56, y: 0).allowsHitTesting(false).zIndex(10)
            }
        }
    }
}
