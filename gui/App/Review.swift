import SwiftUI

struct ReviewView: View {
    @ObservedObject var call: CallModel
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .firstTextBaseline) {
                    Text("Review your answers").font(.title2.bold())
                    Spacer()
                    Text("\(call.answeredCount) answered · \(call.skippedCount) skipped").foregroundColor(T.muted)
                }
                Text("Notes are drafted from your chats. Edit anything before sending.").foregroundColor(T.muted)
                ForEach(0..<call.q.count, id: \.self) { i in ReviewRow(call: call, i: i) }
            }.padding(20).frame(maxWidth: 820, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct ReviewRow: View {
    @ObservedObject var call: CallModel
    let i: Int

    var body: some View {
        let editing = call.reviewEditing.contains(i)
        let q = call.request.questions[i]
        let multi = q.multiSelect ?? false
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top) {
                Text("\(i + 1)").foregroundColor(T.muted).frame(width: 26, height: 26).background(Circle().fill(T.ink.opacity(0.06)))
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) { Text(call.header(i)).fontWeight(.semibold); Text("· " + (multi ? "Choose any" : "Choose one")).font(.callout).foregroundColor(T.muted) }
                    if call.answered(i) {
                        Text(call.answerText(i)).fixedSize(horizontal: false, vertical: true)
                    } else {
                        Text("⚠ Skipped. The agent will see this as unanswered.").foregroundColor(T.muted)
                    }
                }
                Spacer()
                Button { animate { toggle(i) } } label: {
                    HStack(spacing: 6) {
                        if call.answered(i) || editing { Image(systemName: "pencil") }
                        Text(editing ? "Done" : (call.answered(i) ? "Edit" : "Answer"))
                    }.foregroundColor(T.muted).padding(.horizontal, 10).padding(.vertical, 6).hoverTint()
                }.buttonStyle(.plain)
            }
            if editing {
                Divider()
                ForEach(0..<q.options.count, id: \.self) { oi in
                    let on = call.q[i].picked.contains(oi)
                    Button { animate { call.select(i, oi) } } label: {
                        HStack { Marker(on: on, multi: multi); Text(cleanLabel(q.options[oi].label).0); Spacer() }
                            .padding(10)
                            .background(RoundedRectangle(cornerRadius: 8).fill(on ? Color.accentColor.opacity(0.08) : T.ink.opacity(0.04)))
                            .overlay { if on { RoundedRectangle(cornerRadius: 8).stroke(Color.accentColor, lineWidth: 1.5) } }
                    }.buttonStyle(.plain)
                }
                OtherCard(call: call, i: i)
                HStack {
                    LinkButton(title: "Clear answer") { call.clear(i) }
                    LinkButton(title: "↗ Open question for previews and chat") { animate { call.tab = i } }
                }
            }
            if editing || !call.q[i].note.isEmpty {
                NoteBox(call: call, i: i)
            } else {
                HStack(spacing: 12) {
                    LinkButton(title: "Add note") { call.reviewEditing.insert(i) }
                    DraftLink(call: call, i: i) { call.draftNote(i) }
                }
            }
        }
        .padding(14)
        .overlay(RoundedRectangle(cornerRadius: 10)
            .stroke(editing ? Color.accentColor : T.border, style: StrokeStyle(lineWidth: editing ? 1.5 : 1, dash: call.answered(i) ? [] : [5, 3])))
    }

    func toggle(_ i: Int) {
        if call.reviewEditing.contains(i) { call.reviewEditing.remove(i) } else { call.reviewEditing.insert(i) }
    }
}

/// Observes the call so the sheet can fade and scale in and out (RootView only observes the store).
struct SendBackLayer: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel
    var body: some View {
        ZStack {
            if call.sendBackOpen {
                Color.black.opacity(0.3).onTapGesture { }.transition(.opacity)
                SendBackSheet(store: store, call: call).transition(.opacity.combined(with: .scale(scale: 0.97)))
            }
        }.allowsHitTesting(call.sendBackOpen)
    }
}

/// Mock 11: a compact dialog, one line per question, a reason box under each ticked one.
struct SendBackSheet: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 8) { Image(systemName: "arrow.uturn.left"); Text("Send questions back").font(.title3.bold()) }
                Text("Tick the questions that are wrong and say why. The agent will ask again.").foregroundColor(T.muted)
            }
            VStack(alignment: .leading, spacing: 10) {
                ForEach(0..<call.q.count, id: \.self) { i in row(i) }
            }
            Text("Your answers to the other questions go back too, so the agent can keep them.").foregroundColor(T.muted)
            let response = call.sendBackResponse()
            HStack(spacing: 10) {
                if response == nil { Text("Add a reason to send back").foregroundColor(T.muted) }
                Spacer()
                OutlineButton(title: "Cancel", shortcut: .cancelAction) { animate { call.sendBackOpen = false } }
                PrimaryButton(title: "Send back", icon: "arrow.uturn.left", enabled: response != nil, shortcut: .init(.return, modifiers: .command)) {
                    if let r = response { store.respond(call, r) }
                }
            }
        }
        .padding(24).frame(width: 600)
        .fixedSize(horizontal: false, vertical: true)
        .background(RoundedRectangle(cornerRadius: 14).fill(Color(nsColor: .windowBackgroundColor))
            .shadow(color: .black.opacity(0.2), radius: 18, y: 8))
        .onExitCommand { animate { call.sendBackOpen = false } }
    }

    @ViewBuilder func row(_ i: Int) -> some View {
        let on = call.q[i].flagged
        let question = call.request.questions[i].question.split(separator: "\n").first.map(String.init) ?? ""
        Button { animate { call.q[i].flagged.toggle() } } label: {
            HStack(spacing: 10) {
                Marker(on: on, multi: true)
                Text(call.header(i)).fontWeight(.semibold).fixedSize()
                Text(question).foregroundColor(T.muted).lineLimit(1).truncationMode(.tail)
                Spacer(minLength: 0)
            }.contentShape(Rectangle())
        }.buttonStyle(.plain)
        .accessibilityLabel("Send back \(call.header(i)): \(question)")
        .accessibilityAddTraits(on ? .isSelected : [])
        if on {
            VStack(alignment: .leading, spacing: 6) {
                BoxedTextArea(text: $call.q[i].reason, placeholder: "What is wrong with the question?", minHeight: 64, autofocus: true)
                if call.q[i].drafting { Text("drafting…").foregroundColor(T.muted) }
                else if !call.threads[i].isEmpty { LinkButton(title: "✦ Draft from my discussion") { call.draftReason(i) } }
            }.padding(.leading, 32).transition(.opacity)
        }
    }
}
