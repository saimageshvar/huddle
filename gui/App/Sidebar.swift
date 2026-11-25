import SwiftUI

struct RootView: View {
    @ObservedObject var store: Store
    @ObservedObject var layout = LayoutState.shared
    var body: some View {
        ZStack {
            HStack(spacing: 0) {
                if layout.zoomed { Rail(store: store) } else { Sidebar(store: store).frame(width: 250) }
                Divider()
                if let c = store.current {
                    DetailView(store: store, call: c).id(c.id)
                } else {
                    Text("Listening in the menu bar").foregroundColor(T.muted).frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            if let c = store.current, c.sendBackOpen { SendBackSheet(store: store, call: c) }
        }
    }
}

struct Sidebar: View {
    @ObservedObject var store: Store
    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "tray")
                Text("INBOX").fontWeight(.medium)
                Spacer()
                Text("\(store.waiting)").font(.callout)
                    .padding(.horizontal, 8).padding(.vertical, 2)
                    .overlay(Capsule().stroke(T.border))
            }.padding(16)
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(store.calls) { c in InboxRow(store: store, call: c) }
                }
            }
            Divider()
            Text("Listening in the menu bar").font(.callout).foregroundColor(T.muted)
                .frame(maxWidth: .infinity, alignment: .leading).padding(16)
        }.background(T.faint)
    }
}

struct InboxRow: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel
    var body: some View {
        let selected = store.selected == call.id
        let dead = !call.live
        HStack(spacing: 0) {
            Rectangle().fill(selected ? T.ink : Color.clear).frame(width: 3)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline) {
                    Text(call.sessionName).fontWeight(.semibold).lineLimit(1)
                        .foregroundColor(dead ? T.muted : T.ink)
                }
                Text(call.request.branch.isEmpty ? call.repo : "\(call.request.branch) · \(call.repo)")
                    .font(.callout).foregroundColor(T.muted).lineLimit(1)
                HStack(spacing: 4) {
                    Text(dead ? call.shortId + (call.state == .cancelled ? " · agent gave up" : " · read-only")
                              : "\(call.shortId) · \(call.answeredCount) of \(call.q.count) answered")
                        .font(.callout).foregroundColor(T.muted)
                    if dead {
                        Text("·").foregroundColor(T.muted)
                        LinkButton(title: "Dismiss") { store.dismiss(call) }
                    }
                    Spacer(minLength: 4)
                    trailing
                }
            }.padding(.horizontal, 12).padding(.vertical, 10)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(selected ? T.ink.opacity(0.09) : Color.clear)
        .contentShape(Rectangle())
        .onTapGesture { store.selected = call.id }
    }

    @ViewBuilder var trailing: some View {
        switch call.state {
        case .expired: Text("Expired").font(.callout).foregroundColor(T.muted)
        case .cancelled: Text("Cancelled").font(.callout).foregroundColor(T.muted)
        default:
            let left = store.remaining(call)
            HStack(spacing: 4) {
                Image(systemName: left < 300 ? "exclamationmark.triangle.fill" : "clock").font(.caption)
                Text(clock(left)).font(.callout.monospacedDigit())
            }
        }
    }
}
