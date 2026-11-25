import SwiftUI

struct DetailView: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                header
                Divider()
                tabs
                Divider()
                if !call.live { banner }
                content
                Divider()
                footer
            }
            if call.sendBackOpen { SendBackSheet(store: store, call: call) }
            if let big = call.bigImage, let img = NSImage(contentsOfFile: big) {
                ZStack {
                    Color.black.opacity(0.5).onTapGesture { call.bigImage = nil }
                    VStack(spacing: 10) {
                        Image(nsImage: img).resizable().scaledToFit().frame(maxWidth: 760, maxHeight: 520)
                        PrimaryButton(title: "Close") { call.bigImage = nil }
                    }.padding(20).background(RoundedRectangle(cornerRadius: 12).fill(Color(nsColor: .windowBackgroundColor)))
                }
            }
            if let f = call.flash {
                VStack { Spacer(); Text(f).padding(.horizontal, 14).padding(.vertical, 8)
                    .foregroundColor(T.inkText).background(RoundedRectangle(cornerRadius: 8).fill(T.ink)).padding(.bottom, 70) }
            }
        }
    }

    var header: some View {
        HStack {
            Text(call.sessionName).font(.headline)
            Text("· \(call.request.branch) · \(call.shortId)").foregroundColor(T.muted)
            Spacer()
            if call.live {
                Text("\(max(1, Int(store.remaining(call) / 60))) min left").foregroundColor(T.muted)
                OutlineButton(title: "Send back…") { openSendBack() }
            }
        }.padding(.horizontal, 20).padding(.vertical, 14)
    }

    func openSendBack() {
        if call.tab < call.q.count { call.q[call.tab].flagged = true }
        call.sendBackOpen = true
    }

    var tabs: some View {
        HStack(spacing: 4) {
            ForEach(0..<call.q.count, id: \.self) { i in
                TabButton(selected: call.tab == i, label: call.header(i),
                          icon: call.answered(i) ? "checkmark.circle" : "circle",
                          badge: !call.q[i].note.isEmpty, chat: !call.threads[i].isEmpty) { call.tab = i }
            }
            TabButton(selected: call.tab == call.q.count, label: "Review", icon: nil, badge: false, chat: false) { call.tab = call.q.count }
            Spacer()
        }.padding(.horizontal, 20).padding(.top, 8)
    }

    var banner: some View {
        Text(call.state == .cancelled ? "The agent closed this question. Read-only; dismiss it from the inbox when you are done."
                                      : "This question expired. Read-only; dismiss it from the inbox when you are done.")
            .font(.callout).foregroundColor(T.muted)
            .frame(maxWidth: .infinity, alignment: .leading).padding(12).background(T.faint)
    }

    @ViewBuilder var content: some View {
        if call.tab < call.q.count {
            GeometryReader { geo in
                HStack(spacing: 0) {
                    QuestionView(call: call, i: call.tab).disabled(!call.live)
                        .frame(width: max(320, geo.size.width * 0.56))
                    Divider()
                    ChatPane(call: call, i: call.tab).id(call.tab)
                }
            }
        } else {
            ReviewView(call: call).disabled(!call.live)
        }
    }

    var footer: some View {
        HStack {
            if call.tab == call.q.count && call.skippedCount > 0 {
                Text("⚠ \(call.skippedCount) question\(call.skippedCount == 1 ? "" : "s") skipped. You can still send.").foregroundColor(T.muted)
            } else {
                Text("\(call.answeredCount) of \(call.q.count) answered").foregroundColor(T.muted)
            }
            Spacer()
            if call.tab < call.q.count {
                PrimaryButton(title: call.tab + 1 < call.q.count ? "Next →" : "Review →") { call.tab += 1 }
            } else {
                OutlineButton(title: "← Back") { call.tab = call.q.count - 1 }
                PrimaryButton(title: call.skippedCount > 0 ? "Send answers (\(call.skippedCount) skipped) →" : "Send answers →", enabled: call.live) {
                    store.respond(call, call.answerResponse())
                }
            }
        }.padding(.horizontal, 20).padding(.vertical, 14)
    }
}

struct TabButton: View {
    let selected: Bool
    let label: String
    let icon: String?
    let badge: Bool
    let chat: Bool
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                if let icon = icon { Image(systemName: icon) }
                Text(label).fontWeight(selected ? .semibold : .regular)
                if badge { Image(systemName: "square.and.pencil").font(.caption) }
                if chat { Image(systemName: "bubble.left").font(.caption) }
            }
            .padding(.horizontal, 14).padding(.vertical, 8)
            .background(selected ? T.faint : Color.clear)
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }.buttonStyle(.plain)
    }
}

struct QuestionView: View {
    @ObservedObject var call: CallModel
    let i: Int

    var question: Question { call.request.questions[i] }
    var multi: Bool { question.multiSelect ?? false }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack {
                    Text("Question \(i + 1) of \(call.q.count)").foregroundColor(T.muted)
                    Spacer()
                    LinkButton(title: "Clear answer") { call.clear(i) }
                    Chip(text: multi ? "Choose any · \(call.q[i].picked.count) selected" : "Choose one")
                }
                let parts = question.question.split(separator: "\n", maxSplits: 1).map(String.init)
                Text(parts.first ?? "").font(.title3.bold()).fixedSize(horizontal: false, vertical: true)
                if parts.count > 1 {
                    Text(parts[1].trimmingCharacters(in: .whitespacesAndNewlines)).foregroundColor(T.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }
                ForEach(0..<question.options.count, id: \.self) { oi in
                    OptionCard(call: call, i: i, oi: oi)
                }
                OtherCard(call: call, i: i)
                NoteBox(call: call, i: i)
            }.padding(20)
        }
    }
}

func cleanLabel(_ s: String) -> (String, Bool) {
    for tag in [" (Recommended)", " (recommended)"] where s.hasSuffix(tag) {
        return (String(s.dropLast(tag.count)), true)
    }
    return (s, false)
}

struct Marker: View {
    let on: Bool
    let multi: Bool
    var body: some View {
        Image(systemName: multi ? (on ? "checkmark.square.fill" : "square") : (on ? "largecircle.fill.circle" : "circle"))
            .foregroundColor(on ? .accentColor : T.muted).font(.title3)
    }
}

struct OptionCard: View {
    @ObservedObject var call: CallModel
    let i: Int
    let oi: Int

    var body: some View {
        let o = call.request.questions[i].options[oi]
        let multi = call.request.questions[i].multiSelect ?? false
        let on = call.q[i].picked.contains(oi)
        let open = call.q[i].previewOpen == oi
        let (label, recommended) = cleanLabel(o.label)
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top, spacing: 10) {
                Button { call.select(i, oi) } label: { Marker(on: on, multi: multi) }.buttonStyle(.plain)
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Text(label).fontWeight(.semibold)
                        if recommended { Chip(text: "Recommended") }
                    }
                    if let d = o.description, !d.isEmpty { Text(d).font(.callout).foregroundColor(T.muted) }
                }
                Spacer()
                if o.preview != nil {
                    Button { call.q[i].previewOpen = open ? nil : oi } label: {
                        HStack(spacing: 4) { Image(systemName: "eye"); Text("Preview"); Image(systemName: open ? "chevron.up" : "chevron.down") }
                            .font(.callout).padding(.horizontal, 8).padding(.vertical, 4)
                            .overlay(RoundedRectangle(cornerRadius: 6).stroke(T.border))
                    }.buttonStyle(.plain)
                }
            }.padding(12)
            if open, let p = o.preview {
                Divider()
                ScrollView { MarkdownView(text: p).padding(12) }
                    .frame(maxHeight: 280)
            }
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(on ? T.faint : Color.clear))
        .overlay(RoundedRectangle(cornerRadius: 10)
            .stroke(on ? T.ink : T.border, style: StrokeStyle(lineWidth: on ? 2 : 1, dash: open && !on ? [5, 3] : [])))
        .contentShape(Rectangle())
        .onTapGesture {
            if multi { call.q[i].previewOpen = open ? nil : (o.preview != nil ? oi : nil) } else { call.select(i, oi) }
        }
    }
}

struct OtherCard: View {
    @ObservedObject var call: CallModel
    let i: Int
    var body: some View {
        let oi = call.otherIndex(i)
        let multi = call.request.questions[i].multiSelect ?? false
        let on = call.q[i].picked.contains(oi)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 10) {
                Marker(on: on, multi: multi)
                Text("Something else…").fontWeight(.semibold)
                Spacer()
            }
            if on { AttachArea(call: call, key: "q\(i).other", text: $call.q[i].other, placeholder: "Your own answer · paste an image with ⌘V", minHeight: 70) }
        }
        .padding(12)
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(on ? T.ink : T.border, lineWidth: on ? 2 : 1))
        .contentShape(Rectangle())
        .onTapGesture { if !on { call.select(i, oi) } else if multi { call.select(i, oi) } }
    }
}

struct NoteBox: View {
    @ObservedObject var call: CallModel
    let i: Int
    var body: some View {
        let st = call.q[i]
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: "square.and.pencil")
                Text("Note for the agent").fontWeight(.semibold)
                Text("optional").foregroundColor(T.muted)
            }
            AttachArea(call: call, key: "q\(i).note", text: Binding(get: { call.q[i].note }, set: { call.q[i].note = $0; call.noteTyped(i) }),
                       placeholder: "Anything the agent should know about this answer…", minHeight: 44)
            Text("Paste an image with ⌘V").font(.caption).foregroundColor(T.muted)
            HStack(spacing: 10) {
                if st.drafting {
                    ProgressView().controlSize(.small)
                    Text("drafting…").foregroundColor(T.muted)
                } else if !call.threads[i].isEmpty {
                    if st.drafted {
                        Chip(text: "✦ Drafted from your chat")
                        if st.noteBackup != nil { LinkButton(title: "Undo") { call.undoDraft(i) } }
                        LinkButton(title: "Draft again") { draft() }
                    } else {
                        LinkButton(title: "✦ Draft from discussion") { draft() }
                    }
                }
                Spacer()
            }
            Text("The agent reads the note together with the answer, so it can add what a choice cannot say.")
                .font(.callout).foregroundColor(T.muted)
        }
        .padding(12)
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(T.border))
    }

    func draft() {
        if call.q[i].note.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !call.q[i].noteEdited || confirmReplaceNote() {
            call.draftNote(i)
        }
    }
}
