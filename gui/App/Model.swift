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

    init(request: Request, fd: Int32) {
        self.request = request
        self.fd = fd
        deadline = Date(timeIntervalSince1970: TimeInterval(request.deadline))
        q = Array(repeating: QState(), count: request.questions.count)
    }

    var sessionName: String { request.title.isEmpty ? URL(fileURLWithPath: request.cwd).lastPathComponent : request.title }
    var repo: String { URL(fileURLWithPath: request.cwd).lastPathComponent }
    var shortId: String { "#" + request.sessionId.prefix(6) }
    var live: Bool { state == .pending }

    func otherIndex(_ i: Int) -> Int { request.questions[i].options.count }

    func answerText(_ i: Int) -> String {
        let opts = request.questions[i].options
        var parts = q[i].picked.filter { $0 < opts.count }.sorted().map { opts[$0].label }
        let other = q[i].other.trimmingCharacters(in: .whitespacesAndNewlines)
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
            let n = q[i].note.trimmingCharacters(in: .whitespacesAndNewlines)
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
        onChange?()
    }

    func quitAll() {
        for c in calls where c.live { shutdown(c.fd, SHUT_RDWR) }
    }

    private func tick() {
        now = Date()
        for c in calls where c.live && now >= c.deadline { c.state = .expired }
        onChange?()
    }

    func remaining(_ c: CallModel) -> TimeInterval { max(0, c.deadline.timeIntervalSince(now)) }
}

func clock(_ t: TimeInterval) -> String {
    let s = Int(t)
    return String(format: "%d:%02d", s / 60, s % 60)
}
