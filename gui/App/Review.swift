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
        }.frame(maxWidth: .infinity)
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
                Text("\(i + 1)").frame(width: 26, height: 26).overlay(Circle().stroke(T.border))
                VStack(alignment: .leading, spacing: 3) {
                    HStack { Text(call.header(i)).fontWeight(.semibold); Chip(text: multi ? "Choose any" : "Choose one") }
                    if call.answered(i) {
                        Text(call.answerText(i)).fixedSize(horizontal: false, vertical: true)
                    } else {
                        Text("⚠ Skipped. The agent will see this as unanswered.").foregroundColor(T.muted)
                    }
                }
                Spacer()
                OutlineButton(title: editing ? "Done" : (call.answered(i) ? "Edit" : "Answer"), icon: call.answered(i) || editing ? "pencil" : nil) { toggle(i) }
            }
            if editing {
                Divider()
                ForEach(0..<q.options.count, id: \.self) { oi in
                    let on = call.q[i].picked.contains(oi)
                    Button { call.select(i, oi) } label: {
                        HStack { Marker(on: on, multi: multi); Text(cleanLabel(q.options[oi].label).0); Spacer() }
                            .padding(10).overlay(RoundedRectangle(cornerRadius: 8).stroke(on ? T.ink : T.border, lineWidth: on ? 2 : 1))
                    }.buttonStyle(.plain)
                }
                OtherCard(call: call, i: i)
                HStack {
                    LinkButton(title: "Clear answer") { call.clear(i) }
                    LinkButton(title: "↗ Open question for previews and chat") { call.tab = i }
                }
            }
            if editing || !call.q[i].note.isEmpty {
                NoteBox(call: call, i: i)
            } else {
                HStack(spacing: 12) {
                    LinkButton(title: "Add note") { call.reviewEditing.insert(i) }
                    if !call.threads[i].isEmpty {
                        LinkButton(title: "✦ Draft from discussion") { call.draftNote(i) }
                        Text("Asks before replacing your note, and you can undo.").font(.callout).foregroundColor(T.muted)
                    }
                }
            }
        }
        .padding(14)
        .overlay(RoundedRectangle(cornerRadius: 10)
            .stroke(editing ? T.ink : T.border, style: StrokeStyle(lineWidth: editing ? 2 : 1, dash: call.answered(i) ? [] : [5, 3])))
    }

    func toggle(_ i: Int) {
        if call.reviewEditing.contains(i) { call.reviewEditing.remove(i) } else { call.reviewEditing.insert(i) }
    }
}

struct SendBackSheet: View {
    @ObservedObject var store: Store
    @ObservedObject var call: CallModel

    var sheetRows: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(0..<call.q.count, id: \.self) { i in
                Toggle(isOn: $call.q[i].flagged) {
                    HStack(spacing: 6) {
                        Text(call.header(i)).fontWeight(.semibold)
                        Text(call.request.questions[i].question.replacingOccurrences(of: "\n", with: " ")).lineLimit(1).foregroundColor(T.muted)
                    }
                }
                if call.q[i].flagged {
                    BoxedTextArea(text: $call.q[i].reason, placeholder: "What is wrong with the question?", minHeight: 70)
                    if call.q[i].drafting { Text("drafting…").foregroundColor(T.muted) }
                    else if !call.threads[i].isEmpty { LinkButton(title: "✦ Draft from my discussion") { call.draftReason(i) } }
                }
            }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }

    var body: some View {
        ZStack {
            Color.black.opacity(0.3).onTapGesture { }
            VStack(alignment: .leading, spacing: 12) {
                HStack { Image(systemName: "arrow.uturn.left"); Text("Send questions back").font(.title3.bold()) }
                Text("Tick the questions that are wrong and say why. The agent will ask again.").foregroundColor(T.muted)
                ViewThatFits(in: .vertical) {
                    sheetRows
                    ScrollView { sheetRows }
                }.frame(maxHeight: 420)
                Text("Your answers to the other questions go back too, so the agent can keep them.").font(.callout).foregroundColor(T.muted)
                HStack {
                    Spacer()
                    OutlineButton(title: "Cancel") { call.sendBackOpen = false }
                    PrimaryButton(title: "Send back", enabled: call.sendBackResponse() != nil) {
                        if let r = call.sendBackResponse() { store.respond(call, r) }
                    }
                }
            }
            .padding(24).frame(width: 640)
            .background(RoundedRectangle(cornerRadius: 14).fill(Color(nsColor: .windowBackgroundColor)))
            .shadow(radius: 20)
        }
    }
}
