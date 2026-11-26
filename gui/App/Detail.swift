import SwiftUI

struct DetailView: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel
    @ObservedObject var layout = LayoutState.shared

    var inQuestion: Bool { call.tab < call.q.count }

    var body: some View {
        ZStack {
            if layout.zoomed && inQuestion { zoomBody } else { normalBody }
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

    var normalBody: some View {
        VStack(spacing: 0) {
            header
            Divider()
            tabs
            if !call.live { banner }
            content
            Divider()
            footer
        }
    }

    /// Mock 7: a narrow question column on the left, the chat taking the rest.
    var zoomBody: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                HStack {
                    Text("Question \(call.tab + 1) of \(call.q.count)").foregroundColor(T.muted)
                    Spacer()
                    if call.live { OutlineButton(title: "Send back…", icon: "arrow.uturn.left") { openSendBack() } }
                }.padding(.horizontal, 16).padding(.vertical, 12)
                Divider()
                dots
                Divider()
                if !call.live { banner }
                QuestionView(call: call, i: call.tab, compact: true).disabled(!call.live)
                Divider()
                footer
            }.frame(width: 340)
            Divider()
            ChatPane(call: call, i: call.tab, zoomed: true).id(call.tab)
        }
    }

    var dots: some View {
        HStack(spacing: 8) {
            ForEach(0..<call.q.count, id: \.self) { i in
                Button { call.tab = i } label: {
                    Image(systemName: call.answered(i) ? "checkmark.circle.fill" : (call.tab == i ? "circle.inset.filled" : "circle"))
                        .foregroundColor(call.tab == i ? T.ink : T.muted)
                }.buttonStyle(.plain).help(call.header(i))
            }
            Button { call.tab = call.q.count } label: { Text("Review").font(.callout) }.buttonStyle(.plain).foregroundColor(T.muted)
            Spacer()
        }.padding(.horizontal, 16).padding(.vertical, 10)
    }

    var header: some View {
        HStack {
            Text(call.sessionName).font(.headline)
            HStack(spacing: 4) {
                Text("·")
                if !call.request.branch.isEmpty { Image(systemName: "arrow.triangle.branch"); Text(call.request.branch); Text("·") }
                Text(call.shortId)
            }.foregroundColor(T.muted)
            Spacer()
            if call.live {
                ClockBadge(text: "\(max(1, Int(store.remaining(call) / 60))) min left", warn: store.remaining(call) < 300)
                OutlineButton(title: "Send back…", icon: "arrow.uturn.left") { openSendBack() }
            }
        }.padding(.horizontal, 20).padding(.vertical, 14)
    }

    func openSendBack() {
        if call.tab < call.q.count { call.q[call.tab].flagged = true }
        call.sendBackOpen = true
    }

    var tabs: some View {
        HStack(alignment: .bottom, spacing: 2) {
            ForEach(0..<call.q.count, id: \.self) { i in
                TabButton(selected: call.tab == i, label: call.header(i),
                          icon: call.answered(i) ? "checkmark.circle" : "circle",
                          badge: !call.q[i].note.isEmpty, chat: !call.threads[i].isEmpty) { call.tab = i }
            }
            TabButton(selected: call.tab == call.q.count, label: "Review", icon: nil, badge: false, chat: false) { call.tab = call.q.count }
            Spacer()
        }
        .padding(.horizontal, 20).padding(.top, 14)
        .background(alignment: .bottom) { Rectangle().fill(T.border).frame(height: 1) }
    }

    var banner: some View {
        Text(call.state == .cancelled ? "The agent closed this question. Read-only; dismiss it from the inbox when you are done."
                                      : "This question expired. Read-only; dismiss it from the inbox when you are done.")
            .font(.callout).foregroundColor(T.muted)
            .frame(maxWidth: .infinity, alignment: .leading).padding(12).background(T.faint)
    }

    @ViewBuilder var content: some View {
        if inQuestion {
            SplitPane(call: call, i: call.tab)
        } else {
            ReviewView(call: call).disabled(!call.live)
        }
    }

    var footer: some View {
        HStack {
            if call.tab == call.q.count && call.skippedCount > 0 {
                Label("\(call.skippedCount) question\(call.skippedCount == 1 ? "" : "s") skipped. You can still send.", systemImage: "exclamationmark.triangle")
                    .foregroundColor(T.muted)
            } else {
                Text("\(call.answeredCount) of \(call.q.count) answered").foregroundColor(T.muted)
            }
            Spacer()
            if call.tab < call.q.count {
                PrimaryButton(title: call.tab + 1 < call.q.count ? "Next →" : "Review →") { call.tab += 1 }
            } else {
                OutlineButton(title: "Back", icon: "arrow.uturn.left") { call.tab = call.q.count - 1 }
                PrimaryButton(title: call.skippedCount > 0 ? "Send answers (\(call.skippedCount) skipped) →" : "Send answers →", enabled: call.live) {
                    store.respond(call, call.answerResponse())
                }
            }
        }.padding(.horizontal, 20).padding(.vertical, 14)
    }
}

/// Question and chat side by side, with the draggable divider and the collapse strips (mock 8).
struct SplitPane: View {
    @ObservedObject var call: CallModel
    let i: Int
    @ObservedObject var layout = LayoutState.shared

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            HStack(spacing: 0) {
                if layout.questionCollapsed {
                    PaneStrip(icon: call.answered(i) ? "largecircle.fill.circle" : "circle", badge: call.answered(i) ? "◉ \(call.answeredCount)" : nil,
                              caption: "Q\(i + 1)", opens: 1) { layout.questionCollapsed = false }
                    Divider()
                    ChatPane(call: call, i: i, zoomed: false).id(i)
                } else if layout.chatCollapsed {
                    QuestionView(call: call, i: i, compact: false).disabled(!call.live)
                    Divider()
                    PaneStrip(icon: "bubble.left", badge: call.unread > 0 ? "\(call.unread) new" : nil, caption: nil, opens: -1) {
                        layout.chatCollapsed = false; call.unread = 0
                    }
                } else {
                    let q = min(max(w * CGFloat(layout.ratio), LayoutState.questionMin), w - LayoutState.chatMin)
                    QuestionView(call: call, i: i, compact: q < 420).disabled(!call.live).frame(width: q)
                        .opacity(layout.pendingCollapse == -1 ? 0.35 : 1)
                    SplitHandle(total: w)
                    ChatPane(call: call, i: i, zoomed: false).id(i)
                        .opacity(layout.pendingCollapse == 1 ? 0.35 : 1)
                }
            }.coordinateSpace(name: "split")
        }
    }
}

/// Folder tab (mocks 1, 2, 6): the selected one is outlined on three sides and covers the strip's bottom rule.
struct TabButton: View {
    let selected: Bool
    let label: String
    let icon: String?
    let badge: Bool
    let chat: Bool
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            HStack(spacing: 7) {
                if let icon = icon { Image(systemName: icon).font(.system(size: 11)).foregroundColor(selected ? T.ink : T.muted) }
                Text(label).fontWeight(selected ? .semibold : .regular).foregroundColor(selected ? T.ink : T.ink.opacity(0.8))
                if badge { Image(systemName: "square.and.pencil").font(.caption) }
                if chat { Image(systemName: "bubble.left").font(.caption) }
            }
            .padding(.horizontal, 18).padding(.vertical, 9)
            .background {
                if selected {
                    UnevenRoundedRectangle(topLeadingRadius: 8, topTrailingRadius: 8).fill(Color(nsColor: .windowBackgroundColor))
                    OpenBottomTab(radius: 8).stroke(T.border, lineWidth: 1)
                }
            }
            .contentShape(Rectangle())
        }.buttonStyle(.plain)
    }
}

/// The tab outline without its bottom edge, so the selected tab opens into the pane below.
struct OpenBottomTab: Shape {
    let radius: CGFloat
    func path(in r: CGRect) -> Path {
        var p = Path()
        p.move(to: CGPoint(x: r.minX, y: r.maxY + 1))
        p.addLine(to: CGPoint(x: r.minX, y: r.minY + radius))
        p.addArc(tangent1End: CGPoint(x: r.minX, y: r.minY), tangent2End: CGPoint(x: r.minX + radius, y: r.minY), radius: radius)
        p.addLine(to: CGPoint(x: r.maxX - radius, y: r.minY))
        p.addArc(tangent1End: CGPoint(x: r.maxX, y: r.minY), tangent2End: CGPoint(x: r.maxX, y: r.minY + radius), radius: radius)
        p.addLine(to: CGPoint(x: r.maxX, y: r.maxY + 1))
        return p
    }
}

struct QuestionView: View {
    @ObservedObject var call: CallModel
    let i: Int
    var compact = false

    var question: Question { call.request.questions[i] }
    var multi: Bool { question.multiSelect ?? false }

    var body: some View {
        GeometryReader { geo in
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack {
                        if !compact { Text("Question \(i + 1) of \(call.q.count)").foregroundColor(T.muted) }
                        Spacer()
                        LinkButton(title: "Clear answer") { call.clear(i) }
                        HStack(spacing: 5) {
                            Image(systemName: multi ? "checkmark.square" : "circle").font(.caption)
                            Text(multi ? "Choose any · \(call.q[i].picked.count) selected" : "Choose one").font(.caption)
                        }
                        .padding(.horizontal, 8).padding(.vertical, 3)
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(T.border))
                    }
                    let parts = question.question.split(separator: "\n", maxSplits: 1).map(String.init)
                    Text(parts.first ?? "").font(.title3.bold()).fixedSize(horizontal: false, vertical: true)
                    if parts.count > 1 {
                        Text(parts[1].trimmingCharacters(in: .whitespacesAndNewlines)).foregroundColor(T.muted)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    ForEach(0..<question.options.count, id: \.self) { oi in
                        OptionCard(call: call, i: i, oi: oi, compact: compact, maxPreview: geo.size.height * 0.4)
                    }
                    OtherCard(call: call, i: i)
                    NoteBox(call: call, i: i)
                }.padding(20)
            }
        }
        .focusable()
        .focusEffectDisabled()
        .onKeyPress(.downArrow) { move(1); return .handled }
        .onKeyPress(.upArrow) { move(-1); return .handled }
        .onKeyPress(.space) { toggleFocused() }
    }

    /// Single choice: arrows select and open the preview. Several: arrows move the focus, space ticks it (mock 3).
    func move(_ d: Int) {
        let n = question.options.count + 1
        let cur = multi ? (call.q[i].previewOpen ?? -1) : (call.q[i].picked.first ?? -1)
        let next = min(max(cur + d, 0), n - 1)
        if multi { call.q[i].previewOpen = next < question.options.count && question.options[next].preview != nil ? next : nil; call.kbFocus[i] = next }
        else { call.select(i, next); call.kbFocus[i] = next }
    }

    func toggleFocused() -> KeyPress.Result {
        guard multi, let f = call.kbFocus[i] else { return .ignored }
        call.select(i, f)
        return .handled
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
    var compact = false
    var maxPreview: CGFloat = 280

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
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(label).fontWeight(.semibold).fixedSize(horizontal: false, vertical: true)
                        if recommended { Chip(text: "Recommended").fixedSize() }
                    }
                    if let d = o.description, !d.isEmpty { Text(d).font(.callout).foregroundColor(T.muted) }
                }
                Spacer()
                if o.preview != nil {
                    Button { call.q[i].previewOpen = open ? nil : oi } label: {
                        HStack(spacing: 4) { Image(systemName: "eye"); if !compact { Text("Preview") }; Image(systemName: open ? "chevron.up" : "chevron.down") }
                            .font(.callout).padding(.horizontal, 8).padding(.vertical, 4)
                            .overlay(RoundedRectangle(cornerRadius: 6).stroke(T.border))
                    }.buttonStyle(.plain)
                }
            }.padding(12)
            if open, let p = o.preview {
                Divider()
                Group {
                    if MD.isMockup(p) { VerbatimBlock(text: p).padding(12) }
                    else { ScrollView { MarkdownView(text: p).padding(12) } }
                }.frame(maxHeight: max(120, maxPreview))
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
            if on {
                AttachArea(call: call, key: "q\(i).other", text: $call.q[i].other, placeholder: "Your own answer", minHeight: 70)
                Text("Paste an image with ⌘V. Click a thumbnail to enlarge.").font(.caption).foregroundColor(T.muted)
            }
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
