import SwiftUI
import Combine

let skippedAnswer = "(skipped: the user chose not to answer this question)"

struct QState {
    var picked = Set<Int>()
    var other = ""
    var note = ""
    var previewOpen: Int? = nil
    var flagged = false
    var reason = ""
    var input = ""
    var drafting = false
    var draftedAt = -1
    var noteEdited = false
    var drafted = false
    var noteBackup: String? = nil
}

struct ChatMsg: Identifiable {
    let id = UUID()
    let mine: Bool
    var text: String
    let time = Date()
    var live = false
    var failed = false
    var tool: String? = nil
    var images: [String] = []
}

enum CallState { case pending, answered, expired, cancelled }

final class CallModel: ObservableObject, Identifiable {
    let id = UUID()
    let request: Request
    let arrival = Date()
    let deadline: Date
    let fd: Int32
    @Published var state = CallState.pending
    @Published var q: [QState]
    @Published var tab = 0
    @Published var reviewEditing = Set<Int>()
    @Published var sendBackOpen = false
    @Published var threads: [[ChatMsg]]
    @Published var flash: String? {
        didSet {
            guard let f = flash else { return }
            DispatchQueue.main.asyncAfter(deadline: .now() + 4) { [weak self] in if self?.flash == f { self?.flash = nil } }
        }
    }
    @Published var attachments: [String: [Attachment]] = [:]
    @Published var bigImage: String?
    private var dirPath: String?
    private var agent: SideAgent?
    private var sessionId = ""
    private var nextReq = 0
    private var reqs: [Int: (q: Int, kind: String)] = [:]
    private var buffers: [Int: String] = [:]

    init(request: Request, fd: Int32) {
        self.request = request
        self.fd = fd
        deadline = Date(timeIntervalSince1970: TimeInterval(request.deadline))
        q = Array(repeating: QState(), count: request.questions.count)
        threads = Array(repeating: [], count: request.questions.count)
    }

    // MARK: pasted images

    private var callDir: String {
        if let d = dirPath { return d }
        let f = DateFormatter()
        f.dateFormat = "yyyyMMdd-HHmmss"
        let d = "\(huddleHome)/calls/\(f.string(from: arrival))-\(request.sessionId.prefix(8))"
        try? FileManager.default.createDirectory(atPath: d, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        dirPath = d
        return d
    }

    func addImage(_ data: Data, _ key: String) {
        guard let img = NSImage(data: data) else { return }
        let n = (attachments[key]?.count ?? 0) + 1
        var path = "\(callDir)/\(key.replacingOccurrences(of: ".", with: "-"))-\(n).png"
        var k = n
        while FileManager.default.fileExists(atPath: path) { k += 1; path = "\(callDir)/\(key.replacingOccurrences(of: ".", with: "-"))-\(k).png" }
        guard (try? data.write(to: URL(fileURLWithPath: path))) != nil else { return }
        attachments[key, default: []].append(Attachment(path: path, image: img))
    }

    func removeImage(_ key: String, _ id: UUID) {
        if let a = attachments[key]?.first(where: { $0.id == id }) { try? FileManager.default.removeItem(atPath: a.path) }
        attachments[key]?.removeAll { $0.id == id }
    }

    func imagePaths(_ key: String) -> [String] { (attachments[key] ?? []).map { $0.path } }

    /// The text plus one `[image: path]` line per pasted image, which is how the agent gets at them.
    func withImages(_ key: String, _ text: String) -> String {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let lines = imagePaths(key).map { "[image: \($0)]" }
        return ([t].filter { !$0.isEmpty } + lines).joined(separator: "\n")
    }

    // MARK: side agent

    private func ask(_ qi: Int, _ kind: String, _ text: String, images: [String] = []) -> Bool {
        if let a = agent, a.model != Prefs.model || a.effort != Prefs.effort {
            sessionId = a.sessionId
            a.stop()
            agent = nil
        }
        if agent == nil {
            agent = SideAgent(request: request, model: Prefs.model, effort: Prefs.effort, resume: sessionId)
            agent?.onEvent = { [weak self] r, k, t in self?.handle(r, k, t) }
        }
        guard let a = agent else { return false }
        let id = nextReq
        nextReq += 1
        reqs[id] = (qi, kind)
        a.send(id, text, images: images)
        return true
    }

    func sendChat(_ qi: Int, _ text: String) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard live, !t.isEmpty || !(attachments["q\(qi).chat"] ?? []).isEmpty else { return }
        let paths = imagePaths("q\(qi).chat")
        var m = ChatMsg(mine: true, text: t)
        m.images = paths
        threads[qi].append(m)
        threads[qi].append(ChatMsg(mine: false, text: "", live: true))
        q[qi].input = ""
        attachments["q\(qi).chat"] = nil
        if !ask(qi, "chat", "[Q\(qi + 1): \(header(qi))] \(t)", images: paths) { fail(qi, "chat", "Could not start the assistant. Is the claude command installed?") }
    }

    func draftNote(_ qi: Int) {
        guard live, !q[qi].drafting, !threads[qi].isEmpty, threads[qi].last?.live != true else { return }
        q[qi].drafting = true
        q[qi].draftedAt = threads[qi].count
        let ans = answerText(qi)
        if !ask(qi, "note", "[DRAFT NOTE Q\(qi + 1): \(header(qi))] (user's current answer: \(ans.isEmpty ? "none yet" : ans)) Write the note.") {
            fail(qi, "note", "Could not start the assistant.")
        }
    }

    func draftReason(_ qi: Int) {
        guard live, !q[qi].drafting, !threads[qi].isEmpty else { return }
        q[qi].drafting = true
        if !ask(qi, "reason", "[DRAFT REASON Q\(qi + 1): \(header(qi))] Write why question \(qi + 1) should be asked differently.") {
            fail(qi, "reason", "Could not start the assistant.")
        }
    }

    func undoDraft(_ qi: Int) {
        guard let old = q[qi].noteBackup else { return }
        q[qi].note = old
        q[qi].noteBackup = nil
        q[qi].drafted = false
    }

    func noteTyped(_ qi: Int) { q[qi].noteEdited = true; q[qi].drafted = false }

    private func fail(_ qi: Int, _ kind: String, _ text: String) {
        if kind == "chat", var last = threads[qi].last, last.live {
            last.text = text; last.live = false; last.failed = true
            threads[qi][threads[qi].count - 1] = last
        } else {
            flash = text
        }
        q[qi].drafting = false
    }

    private func handle(_ r: Int, _ kind: SideAgent.Kind, _ text: String) {
        guard let (qi, rk) = reqs[r] else { return }
        switch (rk, kind) {
        case ("chat", .delta):
            if var last = threads[qi].last, last.live { last.text += text; last.tool = nil; threads[qi][threads[qi].count - 1] = last }
        case ("chat", .tool):
            if var last = threads[qi].last, last.live { last.tool = text; threads[qi][threads[qi].count - 1] = last }
        case ("chat", .done):
            if var last = threads[qi].last, last.live { last.live = false; last.tool = nil; threads[qi][threads[qi].count - 1] = last }
            reqs[r] = nil
        case (_, .delta):
            buffers[r, default: ""] += text
        case ("note", .done), ("reason", .done):
            finishDraft(r, qi, rk)
        case (_, .error):
            fail(qi, rk, text)
            reqs[r] = nil
        default: break
        }
    }

    private func finishDraft(_ r: Int, _ qi: Int, _ kind: String) {
        let out = (buffers[r] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        buffers[r] = nil
        reqs[r] = nil
        q[qi].drafting = false
        let lastLine = out.split(separator: "\n").last.map(String.init) ?? ""
        if out.isEmpty || lastLine.trimmingCharacters(in: CharacterSet(charactersIn: "`.*_ ")) == "NONE" {
            flash = kind == "note" ? "Nothing to pass on from this discussion yet." : "Nothing in the discussion says what is wrong with the question."
            return
        }
        if kind == "note" {
            q[qi].noteBackup = q[qi].note
            q[qi].note = out
            q[qi].noteEdited = false
            q[qi].drafted = true
        } else {
            q[qi].reason = out
        }
    }

    func stopAgent() { agent?.stop(); agent = nil }

    var sessionName: String { request.title.isEmpty ? URL(fileURLWithPath: request.cwd).lastPathComponent : request.title }
    var repo: String { URL(fileURLWithPath: request.cwd).lastPathComponent }
    var shortId: String { "#" + request.sessionId.prefix(6) }
    var live: Bool { state == .pending }

    func otherIndex(_ i: Int) -> Int { request.questions[i].options.count }

    func answerText(_ i: Int) -> String {
        let opts = request.questions[i].options
        var parts = q[i].picked.filter { $0 < opts.count }.sorted().map { opts[$0].label }
        let other = withImages("q\(i).other", q[i].other)
        if q[i].picked.contains(otherIndex(i)) && !other.isEmpty { parts.append(other) }
        return parts.joined(separator: ", ")
    }

    func answered(_ i: Int) -> Bool { !answerText(i).isEmpty }
    var answeredCount: Int { (0..<q.count).filter(answered).count }
    var skippedCount: Int { q.count - answeredCount }

    func header(_ i: Int) -> String {
        let h = request.questions[i].header ?? ""
        return h.isEmpty ? "Question \(i + 1)" : h
    }

    func select(_ i: Int, _ oi: Int) {
        let multi = request.questions[i].multiSelect ?? false
        if multi {
            if !q[i].picked.insert(oi).inserted { q[i].picked.remove(oi) }
        } else {
            q[i].picked = [oi]
            q[i].previewOpen = request.questions[i].options.indices.contains(oi) && request.questions[i].options[oi].preview != nil ? oi : nil
        }
    }

    func clear(_ i: Int) { q[i].picked = []; q[i].other = "" }

    func answerResponse() -> Response {
        var answers: [String: String] = [:]
        var notes: [String: [String: String]] = [:]
        for (i, question) in request.questions.enumerated() {
            let a = answerText(i)
            answers[question.question] = a.isEmpty ? skippedAnswer : a
            let n = withImages("q\(i).note", q[i].note)
            if !n.isEmpty { notes[question.question] = ["notes": n] }
        }
        return Response(kind: "answer", answers: answers, annotations: notes, reason: nil)
    }

    func sendBackResponse() -> Response? {
        var b = ""
        var kept: [String] = []
        for (i, question) in request.questions.enumerated() {
            let reason = q[i].reason.trimmingCharacters(in: .whitespacesAndNewlines)
            if q[i].flagged && !reason.isEmpty {
                b += "Question: \(question.question)\nWhat is wrong with it: \(reason)\n\n"
            } else if answered(i) {
                kept.append("- \(question.question) → \(answerText(i))")
            }
        }
        if b.isEmpty { return nil }
        if !kept.isEmpty { b += "The user's answers to the other questions, to keep when you ask again:\n" + kept.joined(separator: "\n") + "\n" }
        return Response(kind: "reframe", answers: nil, annotations: nil, reason: b)
    }
}

final class Store: ObservableObject {
    @Published var calls: [CallModel] = []
    @Published var selected: UUID?
    @Published var now = Date()
    var onChange: (() -> Void)?
    var onArrival: (() -> Void)?
    var onEmpty: (() -> Void)?
    private var timer: Timer?

    init() {
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in self?.tick() }
    }

    var current: CallModel? { calls.first { $0.id == selected } }
    var waiting: Int { calls.filter { $0.live }.count }

    func add(_ request: Request, fd: Int32) {
        let c = CallModel(request: request, fd: fd)
        calls.append(c)
        if selected == nil { selected = c.id }
        onArrival?()
        onChange?()
    }

    func respond(_ c: CallModel, _ r: Response) {
        guard c.live, var data = try? JSONEncoder().encode(r) else { return }
        data.append(10)
        _ = writeAll(c.fd, data)
        shutdown(c.fd, SHUT_RDWR)
        c.state = .answered
        c.stopAgent()
        remove(c)
    }

    func dismiss(_ c: CallModel) { remove(c) }

    func remove(_ c: CallModel) {
        let wasSelected = selected == c.id
        calls.removeAll { $0.id == c.id }
        if wasSelected { selected = calls.first(where: { $0.live })?.id ?? calls.first?.id }
        onChange?()
        if calls.isEmpty { onEmpty?() }
    }

    func cancel(fd: Int32) {
        guard let c = calls.first(where: { $0.fd == fd }), c.live else { return }
        c.state = .cancelled
        c.stopAgent()
        onChange?()
    }

    func quitAll() {
        for c in calls { c.stopAgent(); if c.live { shutdown(c.fd, SHUT_RDWR) } }
    }

    private func tick() {
        now = Date()
        for c in calls where c.live && now >= c.deadline { c.state = .expired; c.stopAgent() }
        onChange?()
    }

    func remaining(_ c: CallModel) -> TimeInterval { max(0, c.deadline.timeIntervalSince(now)) }
}

func clock(_ t: TimeInterval) -> String {
    let s = Int(t)
    return String(format: "%d:%02d", s / 60, s % 60)
}
