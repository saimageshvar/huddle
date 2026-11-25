import SwiftUI

struct Option: Codable { var label: String; var description: String? }
struct Question: Codable { var question: String; var header: String?; var multiSelect: Bool?; var options: [Option] }
struct Request: Codable { var title: String?; var branch: String?; var cwd: String?; var questions: [Question] }
struct Response: Codable { var kind = "answer"; var answers: [String: String]; var annotations: [String: [String: String]] }

let dir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : ""
let request: Request = {
    let data = (try? Data(contentsOf: URL(fileURLWithPath: dir + "/request.json"))) ?? Data()
    return (try? JSONDecoder().decode(Request.self, from: data)) ?? Request(questions: [])
}()

// ObservableObject instead of @State: the Command Line Tools ship no SwiftUIMacros plugin.
final class Form: ObservableObject {
    @Published var picked: [Int: Set<Int>] = [:]
    @Published var notes: [Int: String] = [:]
}

struct ContentView: View {
    @StateObject private var form = Form()
    private var picked: [Int: Set<Int>] { form.picked }
    private var notes: [Int: String] { form.notes }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("\(request.title ?? "huddle") · \(request.branch ?? "")").font(.headline)
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    ForEach(Array(request.questions.enumerated()), id: \.offset) { qi, q in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(q.question).font(.title3.bold())
                            ForEach(Array(q.options.enumerated()), id: \.offset) { oi, o in
                                Button { toggle(qi, oi, multi: q.multiSelect ?? false) } label: {
                                    HStack(alignment: .top) {
                                        Image(systemName: picked[qi]?.contains(oi) == true ? "largecircle.fill.circle" : "circle")
                                        VStack(alignment: .leading) {
                                            Text(o.label)
                                            if let d = o.description { Text(d).font(.caption).foregroundStyle(.secondary) }
                                        }
                                    }
                                }.buttonStyle(.plain)
                            }
                            TextField("Notes", text: Binding(get: { form.notes[qi] ?? "" }, set: { form.notes[qi] = $0 }), axis: .vertical)
                                .textFieldStyle(.roundedBorder).lineLimit(2...5)
                        }
                    }
                }
            }
            HStack { Spacer(); Button("Submit", action: submit).keyboardShortcut(.defaultAction) }
        }
        .padding(20).frame(minWidth: 520, minHeight: 420)
    }

    func toggle(_ qi: Int, _ oi: Int, multi: Bool) {
        var s = picked[qi] ?? []
        if multi { if !s.insert(oi).inserted { s.remove(oi) } } else { s = [oi] }
        form.picked[qi] = s
    }

    func submit() {
        var answers: [String: String] = [:], ann: [String: [String: String]] = [:]
        for (qi, q) in request.questions.enumerated() {
            answers[q.question] = (picked[qi] ?? []).sorted().map { q.options[$0].label }.joined(separator: ", ")
            if let n = notes[qi], !n.isEmpty { ann[q.question] = ["notes": n] }
        }
        let out = try? JSONEncoder().encode(Response(answers: answers, annotations: ann))
        try? out?.write(to: URL(fileURLWithPath: dir + "/response.json"))
        NSApp.terminate(nil)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    func applicationDidFinishLaunching(_ n: Notification) {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 560, height: 480),
                          styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        window.title = "Huddle"
        window.contentView = NSHostingView(rootView: ContentView())
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }
}

@main enum Main {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        app.run()
    }
}
