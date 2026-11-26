// PreToolUse hook for AskUserQuestion. Foundation only, so it starts in a few ms. Printing nothing leaves
// Claude Code's built-in dialog in charge.
import Foundation

setvbuf(stdout, nil, _IOLBF, 0)
let stdinData = FileHandle.standardInput.readDataToEndOfFile()

func fileText(_ path: String) -> String? { try? String(contentsOfFile: path, encoding: .utf8) }
func exists(_ p: String) -> Bool { FileManager.default.fileExists(atPath: p) }

guard let input = (try? JSONSerialization.jsonObject(with: stdinData)) as? [String: Any],
      input["tool_name"] as? String == "AskUserQuestion" else { exit(0) }
let sid = input["session_id"] as? String ?? ""

func delegateToTUI() -> Never {
    let bin = ProcessInfo.processInfo.environment["HUDDLE_TUI_BIN"] ?? NSHomeDirectory() + "/.local/bin/huddle"
    let p = Process()
    p.executableURL = URL(fileURLWithPath: bin)
    p.arguments = ["hook"]
    let pipe = Pipe()
    p.standardInput = pipe
    do { try p.run() } catch { exit(0) }
    pipe.fileHandleForWriting.write(stdinData)
    try? pipe.fileHandleForWriting.close()
    p.waitUntilExit()
    exit(p.terminationStatus)
}

let ui = (ProcessInfo.processInfo.environment["HUDDLE_UI"] ?? fileText(huddleHome + "/ui") ?? "tui").trimmingCharacters(in: .whitespacesAndNewlines)
if ui != "gui" { delegateToTUI() }

func stepAside() -> Bool {
    let home = NSHomeDirectory()
    let away = home + "/.claude/away/state"
    if exists(away + "/active.json") || (!sid.isEmpty && exists(away + "/sessions/\(sid).json")) { return true }
    let hap = home + "/.local/state/hap"
    let data = (try? Data(contentsOf: URL(fileURLWithPath: hap + "/sessions/\(sid).json")))
        ?? (try? Data(contentsOf: URL(fileURLWithPath: hap + "/global.json")))
    guard let d = data, let m = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any] else { return false }
    return m["mode"] as? String == "full" && (m["dry"] as? Bool) != true
}
if stepAside() { exit(0) }

guard let toolInput = input["tool_input"] as? [String: Any],
      let qData = try? JSONSerialization.data(withJSONObject: toolInput["questions"] ?? []),
      let questions = try? JSONDecoder().decode([Question].self, from: qData), !questions.isEmpty else { exit(0) }

func sessionTitle(_ path: String) -> String {
    guard let text = fileText(path) else { return "" }
    var title = ""
    for line in text.split(separator: "\n") where line.contains("-title") {
        guard let o = (try? JSONSerialization.jsonObject(with: Data(line.utf8))) as? [String: Any] else { continue }
        if o["type"] as? String == "custom-title", let t = o["customTitle"] as? String, !t.isEmpty { title = t }
        else if o["type"] as? String == "ai-title", let t = o["aiTitle"] as? String, !t.isEmpty { title = t }
    }
    return title
}

/// Reads .git/HEAD instead of running git: spawning a process through Foundation costs tens of ms.
func gitBranch(_ dir: String) -> String {
    var url = URL(fileURLWithPath: dir)
    for _ in 0..<12 {
        let dot = url.appendingPathComponent(".git").path
        var isDir: ObjCBool = false
        if FileManager.default.fileExists(atPath: dot, isDirectory: &isDir) {
            var gitDir = dot
            if !isDir.boolValue, let t = fileText(dot), t.hasPrefix("gitdir:") {
                gitDir = String(t.dropFirst(7)).trimmingCharacters(in: .whitespacesAndNewlines)
            }
            let head = (fileText(gitDir + "/HEAD") ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            return head.hasPrefix("ref: refs/heads/") ? String(head.dropFirst(16)) : ""
        }
        let up = url.deletingLastPathComponent()
        if up.path == url.path { break }
        url = up
    }
    return ""
}

let cwd = input["cwd"] as? String ?? ""
let transcript = input["transcript_path"] as? String ?? ""
let request = Request(sessionId: sid, cwd: cwd, transcriptPath: transcript, title: sessionTitle(transcript),
                      branch: gitBranch(cwd), questions: questions,
                      deadline: Int64(Date().timeIntervalSince1970) + 3600 - 20, hookPid: getpid())

func launchApp() {
    let exe = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath()
    let app = exe.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    let p = Process()
    p.executableURL = URL(fileURLWithPath: "/usr/bin/open")
    p.arguments = ["-g", app.path, "--args", "--quiet"]
    try? p.run()
}

func connectWithLaunch() -> Int32? {
    if let fd = connectToApp() { return fd }
    launchApp()
    let end = Date().addingTimeInterval(2)
    while Date() < end {
        usleep(50_000)
        if let fd = connectToApp() { return fd }
    }
    return nil
}

guard let fd = connectWithLaunch(),
      var line = try? JSONEncoder().encode(request) else { exit(0) }
line.append(10)
guard writeAll(fd, line), let reply = readLine(fd),
      let resp = try? JSONDecoder().decode(Response.self, from: reply) else { exit(0) }

var out: [String: Any] = ["hookEventName": "PreToolUse"]
switch resp.kind {
case "reframe":
    out["permissionDecision"] = "deny"
    out["permissionDecisionReason"] = "The user sent this question back instead of answering it:\n\n" + (resp.reason ?? "")
case "answer":
    var updated = toolInput
    updated["answers"] = resp.answers ?? [:]
    if let a = resp.annotations, !a.isEmpty { updated["annotations"] = a }
    out["permissionDecision"] = "allow"
    out["updatedInput"] = updated
default:
    exit(0)
}
if let data = try? JSONSerialization.data(withJSONObject: ["hookSpecificOutput": out]) {
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data("\n".utf8))
}
