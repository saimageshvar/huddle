import SwiftUI

final class PrefsObject: ObservableObject {
    static let shared = PrefsObject()
    @Published var model = Prefs.model { didSet { Prefs.model = model } }
    @Published var effort = Prefs.effort { didSet { Prefs.effort = effort } }
}

struct ChatPane: View {
    @ObservedObject var call: CallModel
    let i: Int
    @ObservedObject var prefs = PrefsObject.shared

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
        HStack(spacing: 8) {
            Image(systemName: "sparkle")
            Text("Ask about it").fontWeight(.semibold).lineLimit(1).fixedSize()
            Spacer()
            Menu {
                ForEach(Prefs.models, id: \.id) { m in
                    Button { prefs.model = m.id } label: {
                        Text((prefs.model == m.id ? "✓ " : "    ") + m.name + " — " + m.note)
                    }
                }
                Divider()
                Button("Reset to default") { prefs.model = "sonnet" }
            } label: { Text("Model · \(Prefs.modelName(prefs.model))").padding(.horizontal, 8).padding(.vertical, 4) }
                .menuStyle(.borderlessButton).fixedSize()
            Menu {
                ForEach(Prefs.efforts, id: \.self) { e in
                    Button { prefs.effort = e } label: { Text((prefs.effort == e ? "✓ " : "    ") + e.capitalized) }
                }
                Divider()
                Button("Reset to default") { prefs.effort = "medium" }
            } label: { Text("Effort · \(prefs.effort.capitalized)").padding(.horizontal, 8).padding(.vertical, 4) }
                .menuStyle(.borderlessButton).fixedSize()
        }.padding(.horizontal, 16).padding(.vertical, 10)
    }

    var messages: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    if thread.isEmpty { chips }
                    ForEach(thread) { m in MsgView(call: call, i: i, msg: m) }
                    Color.clear.frame(height: 1).id("end")
                }.padding(16)
            }
            .onChange(of: thread.last?.text) { _, _ in proxy.scrollTo("end") }
            .onChange(of: thread.count) { _, _ in proxy.scrollTo("end") }
        }
    }

    var chips: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Ask the assistant about this question. It has read the conversation.").foregroundColor(T.muted)
            ForEach(["Explain the options in plain words", "What would you pick, and why?"], id: \.self) { s in
                Button { call.sendChat(i, s) } label: {
                    Text(s).padding(.horizontal, 12).padding(.vertical, 6)
                        .overlay(Capsule().stroke(T.border))
                }.buttonStyle(.plain)
            }
        }
    }

    var composer: some View {
        AttachArea(call: call, key: "q\(i).chat", text: $call.q[i].input,
                   placeholder: "Ask about this question · Enter to send, Shift+Enter for a new line · paste an image",
                   minHeight: 56, submitOnEnter: true) { call.sendChat(i, call.q[i].input) }
            .padding(12)
            .disabled(!call.live)
    }
}

func hhmm(_ d: Date) -> String {
    let f = DateFormatter()
    f.dateFormat = "HH:mm"
    return f.string(from: d)
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
                    Text(msg.text).textSelection(.enabled)
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
        VStack(alignment: .leading, spacing: 4) {
            Text(msg.live ? "Assistant · writing…" : "Assistant · \(hhmm(msg.time))").font(.caption).foregroundColor(T.muted)
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "sparkle").frame(width: 28, height: 28).overlay(Circle().stroke(T.border))
                VStack(alignment: .leading, spacing: 6) {
                    bubble
                    if !msg.live && !msg.failed && !msg.text.isEmpty {
                        HStack(spacing: 8) {
                            small("Copy", "doc.on.doc") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(msg.text, forType: .string)
                            }
                            if call.live {
                                small("Add to note", "square.and.pencil") {
                                    let n = call.q[i].note
                                    call.q[i].note = n.isEmpty ? msg.text : n + "\n" + msg.text
                                    call.noteTyped(i)
                                }
                            }
                        }
                    }
                }
                Spacer(minLength: 20)
            }
        }
    }

    @ViewBuilder var bubble: some View {
        Group {
            if msg.text.isEmpty {
                Text(msg.tool ?? "Thinking…").italic().foregroundColor(T.muted)
            } else {
                if msg.failed { Text(msg.text).foregroundColor(.red) } else { MarkdownView(text: msg.text) }
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
