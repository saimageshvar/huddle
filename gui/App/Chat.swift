import SwiftUI

final class PrefsObject: ObservableObject {
    static let shared = PrefsObject()
    @Published var model = Prefs.model { didSet { Prefs.model = model } }
    @Published var effort = Prefs.effort { didSet { Prefs.effort = effort } }
}

struct ChatPane: View {
    @ObservedObject var call: CallModel
    let i: Int
    var zoomed = false
    @ObservedObject var prefs = PrefsObject.shared
    @ObservedObject var layout = LayoutState.shared

    var thread: [ChatMsg] { call.threads[i] }
    var busy: Bool { thread.last?.live == true }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            messages
            Divider()
            composer
        }.background(T.faint.opacity(0.5))
    }

    var header: some View {
        VStack(spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: "sparkle")
                Text("Ask about it").fontWeight(.semibold).lineLimit(1).fixedSize()
                Spacer()
                if zoomed { pickers }
                Button { layout.zoomed.toggle() } label: {
                    HStack(spacing: 5) {
                        Image(systemName: zoomed ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right")
                        if zoomed { Text("Exit zoom") }
                    }.padding(.horizontal, 9).padding(.vertical, 6).overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
                }.buttonStyle(.plain).help(zoomed ? "Exit zoom" : "Zoom the chat")
            }
            if !zoomed { HStack(spacing: 8) { pickers; Spacer() } }
        }.padding(.horizontal, 16).padding(.vertical, 10)
    }

    var pickers: some View { ModelPicker() }

    var messages: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    if thread.isEmpty {
                        Text("Ask the assistant about this question. It has read the conversation.").foregroundColor(T.muted)
                    }
                    ForEach(thread) { m in MsgView(call: call, i: i, msg: m) }
                    if !busy && call.live { chips }
                    Color.clear.frame(height: 1).id("end")
                }.padding(16)
            }
            .onChange(of: thread.last?.text) { _, _ in proxy.scrollTo("end") }
            .onChange(of: thread.count) { _, _ in proxy.scrollTo("end") }
        }
    }

    var chips: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(["Explain the options in plain words", "What would you pick, and why?"], id: \.self) { s in
                Button { call.sendChat(i, s) } label: {
                    Text(s).padding(.horizontal, 12).padding(.vertical, 6).overlay(Capsule().stroke(T.border))
                }.buttonStyle(.plain)
            }
        }
    }

    var composer: some View {
        HStack(alignment: .bottom, spacing: 8) {
            AttachArea(call: call, key: "q\(i).chat", text: $call.q[i].input,
                       placeholder: "Ask about this question · Enter to send, Shift+Enter for a new line · paste an image",
                       minHeight: 56, maxHeight: 200, submitOnEnter: true) { call.sendChat(i, call.q[i].input) }
            if busy {
                Button { call.stopChat() } label: {
                    Image(systemName: "stop.fill").font(.system(size: 13)).frame(width: 38, height: 38)
                        .background(RoundedRectangle(cornerRadius: 10).fill(Color(NSColor.controlBackgroundColor)))
                        .overlay(RoundedRectangle(cornerRadius: 10).stroke(T.border))
                        .contentShape(RoundedRectangle(cornerRadius: 10))
                }.buttonStyle(.plain).help("Stop the answer")
            }
        }
        .padding(12)
        .disabled(!call.live)
    }
}

func hhmm(_ d: Date) -> String {
    let f = DateFormatter()
    f.dateFormat = "HH:mm"
    return f.string(from: d)
}

struct PasteChip: View {
    let paste: PastedText
    var onRemove: (() -> Void)? = nil
    @StateObject private var open = Flag()
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: "paperclip")
                Button { open.on.toggle() } label: {
                    HStack(spacing: 4) { Text(paste.label); Image(systemName: open.on ? "chevron.up" : "chevron.down") }
                }.buttonStyle(.plain)
                if let r = onRemove { Button(action: r) { Image(systemName: "xmark.circle.fill") }.buttonStyle(.plain) }
            }
            .font(.callout).padding(.horizontal, 8).padding(.vertical, 4)
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
            if open.on {
                ScrollView { Text(paste.text).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(8) }
                    .frame(maxHeight: 200).background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.07)))
            }
        }
    }
}

struct ChatPick { let q: Int; let options: [Int] }

/// Splits a trailing `PICK: Qn a,b` line off an assistant reply; the line is also hidden while it is still streaming in.
func splitPick(_ text: String, call: CallModel) -> (String, ChatPick?) {
    var lines = text.split(separator: "\n", omittingEmptySubsequences: false)
    while lines.last?.trimmingCharacters(in: .whitespaces).isEmpty == true { lines.removeLast() }
    guard let last = lines.last, last.uppercased().hasPrefix("PICK:") else { return (text, nil) }
    let body = lines.dropLast().joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
    let parts = last.dropFirst(5).split(separator: " ", omittingEmptySubsequences: true)
    guard parts.count == 2, parts[0].uppercased().hasPrefix("Q"), let n = Int(parts[0].dropFirst()), (1...call.q.count).contains(n) else { return (body, nil) }
    let count = call.request.questions[n - 1].options.count
    let idx = parts[1].split(separator: ",").compactMap { $0.lowercased().unicodeScalars.first.map { Int($0.value) - 97 } }.filter { (0..<count).contains($0) }
    return (body, idx.isEmpty ? nil : ChatPick(q: n - 1, options: idx))
}

struct MsgView: View {
    @ObservedObject var call: CallModel
    let i: Int
    let msg: ChatMsg

    var body: some View {
        if msg.mine { mine } else { theirs }
    }

    var mine: some View {
        VStack(alignment: .trailing, spacing: 4) {
            Text("You · \(hhmm(msg.time))").font(.caption).foregroundColor(T.muted)
            HStack(alignment: .top, spacing: 8) {
                Spacer(minLength: 40)
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(msg.images, id: \.self) { p in
                        if let img = NSImage(contentsOfFile: p) {
                            Image(nsImage: img).resizable().scaledToFit().frame(maxWidth: 220, maxHeight: 140)
                                .clipShape(RoundedRectangle(cornerRadius: 8))
                                .onTapGesture { call.bigImage = p }
                        }
                    }
                    if !msg.text.isEmpty { Text(msg.text).textSelection(.enabled) }
                    ForEach(msg.pasted) { p in PasteChip(paste: p).colorScheme(.dark) }
                }
                .padding(.horizontal, 14).padding(.vertical, 10)
                .foregroundColor(T.inkText)
                .background(RoundedRectangle(cornerRadius: 16).fill(T.ink))
                Image(systemName: "person.fill").frame(width: 28, height: 28)
                    .foregroundColor(T.inkText).background(Circle().fill(T.ink))
            }
        }.frame(maxWidth: .infinity, alignment: .trailing)
    }

    var theirs: some View {
        let (shown, pick) = splitPick(msg.text, call: call)
        return VStack(alignment: .leading, spacing: 4) {
            Text(msg.live ? "Assistant · writing…" : "Assistant · \(hhmm(msg.time))").font(.caption).foregroundColor(T.muted)
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "sparkle").frame(width: 28, height: 28).overlay(Circle().stroke(T.border))
                VStack(alignment: .leading, spacing: 6) {
                    bubble(shown)
                    if !msg.live && !msg.failed && !msg.text.isEmpty {
                        HStack(spacing: 8) {
                            small("Copy", "doc.on.doc") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(shown, forType: .string)
                            }
                            if call.live {
                                small("Add to note", "square.and.pencil") {
                                    let n = call.q[i].note
                                    call.q[i].note = n.isEmpty ? shown : n + "\n" + shown
                                    call.noteTyped(i)
                                }
                            }
                            if call.live, let p = pick { pickButton(p) }
                        }
                    }
                }
                Spacer(minLength: 20)
            }
        }
    }

    @ViewBuilder func pickButton(_ p: ChatPick) -> some View {
        let qs = call.request.questions[p.q]
        let names = p.options.map { cleanLabel(qs.options[$0].label).0 }.joined(separator: ", ")
        let done = Set(p.options).isSubset(of: call.q[p.q].picked)
        Button {
            call.kbFocus[p.q] = nil
            animate {
                for o in p.options where !call.q[p.q].picked.contains(o) { call.select(p.q, o) }
                if p.q != call.tab { call.tab = p.q }
            }
        } label: {
            HStack(spacing: 4) { Image(systemName: done ? "checkmark" : "checkmark.circle"); Text(done ? "Selected: \(names)" : "Select \(names)").lineLimit(1) }
                .font(.caption.weight(.semibold)).padding(.horizontal, 8).padding(.vertical, 3)
                .foregroundColor(.accentColor)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.accentColor.opacity(0.6)))
        }.buttonStyle(.plain).disabled(done)
    }

    @ViewBuilder func bubble(_ shown: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if shown.isEmpty {
                Text(msg.tool ?? "Thinking…").italic().foregroundColor(T.muted)
            } else if msg.failed {
                Text(msg.text).foregroundColor(.red)
            } else {
                MarkdownView(text: shown)
            }
            if msg.live && !shown.isEmpty {
                TimelineView(.periodic(from: .now, by: 0.5)) { ctx in
                    Text("▌").opacity(Int(ctx.date.timeIntervalSince1970 * 2) % 2 == 0 ? 1 : 0.1)
                }
            }
        }
        .fixedSize(horizontal: false, vertical: true)
        .padding(.horizontal, 14).padding(.vertical, 10)
        .background(RoundedRectangle(cornerRadius: 16).fill(Color(nsColor: .textBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 16).stroke(T.border))
    }

    func small(_ title: String, _ icon: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 4) { Image(systemName: icon); Text(title) }
                .font(.caption).padding(.horizontal, 8).padding(.vertical, 3)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(T.border))
        }.buttonStyle(.plain)
    }
}

func confirmReplaceNote() -> Bool {
    let a = NSAlert()
    a.messageText = "Replace your note?"
    a.informativeText = "The draft replaces what is in the note now. You can undo it."
    a.addButton(withTitle: "Replace")
    a.addButton(withTitle: "Cancel")
    return a.runModal() == .alertFirstButtonReturn
}

final class PickerState: ObservableObject {
    @Published var open = false
    @Published var hot = Prefs.model
}

/// One pill for model and effort. Its popover lists model names; the one under the pointer shows its efforts beside it.
struct ModelPicker: View {
    @ObservedObject var prefs = PrefsObject.shared
    @StateObject private var st = PickerState()

    var body: some View {
        Button { st.hot = prefs.model; st.open.toggle() } label: {
            HStack(spacing: 6) {
                Image(systemName: "cpu")
                Text("\(Prefs.modelName(prefs.model)) · \(prefs.effort.capitalized)").lineLimit(1)
                Image(systemName: "chevron.down").font(.caption)
            }
            .padding(.horizontal, 10).padding(.vertical, 6)
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
            .contentShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain).fixedSize()
        .accessibilityLabel("Model and effort: \(Prefs.modelName(prefs.model)), \(prefs.effort) effort")
        .help("Model and effort for the assistant")
        .popover(isPresented: Binding(get: { st.open }, set: { st.open = $0 }), arrowEdge: .bottom) { panel }
    }

    var panel: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 6) {
                VStack(spacing: 2) {
                    ForEach(Prefs.models, id: \.id) { m in
                        PickerRow(title: m.name, checked: prefs.model == m.id, lit: st.hot == m.id, chevron: true) {
                            prefs.model = m.id; st.open = false
                        } hover: { if $0 { st.hot = m.id } }
                    }
                }.frame(width: 150)
                Rectangle().fill(T.border).frame(width: 1)
                VStack(spacing: 2) {
                    ForEach(Prefs.efforts, id: \.self) { e in
                        PickerRow(title: e.capitalized, checked: prefs.model == st.hot && prefs.effort == e, lit: false, chevron: false) {
                            prefs.model = st.hot; prefs.effort = e; st.open = false
                        } hover: { _ in }
                    }
                }.frame(width: 118)
            }
            Text("Applies from your next message").font(.caption).foregroundColor(T.muted).padding(.horizontal, 10)
        }.padding(8).fixedSize()
    }
}

struct PickerRow: View {
    let title: String
    let checked: Bool
    let lit: Bool
    let chevron: Bool
    let action: () -> Void
    let hover: (Bool) -> Void
    @StateObject private var over = Flag()

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: "checkmark").font(.caption.weight(.semibold)).opacity(checked ? 1 : 0).frame(width: 12)
                Text(title).lineLimit(1).fixedSize()
                Spacer(minLength: 8)
                if chevron { Image(systemName: "chevron.right").font(.caption2).foregroundColor(T.muted) }
            }
            .padding(.horizontal, 8).padding(.vertical, 6)
            .background(RoundedRectangle(cornerRadius: 6).fill(over.on || lit ? T.faint : Color.clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { over.on = $0; hover($0) }
    }
}
