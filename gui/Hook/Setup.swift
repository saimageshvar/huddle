// `huddle-gui setup | uninstall | status`: registers the helper as Claude Code's AskUserQuestion hook.
import Foundation

private let fm = FileManager.default
private let env = ProcessInfo.processInfo.environment
private let home = env["HOME"] ?? NSHomeDirectory()
private let hookLink = huddleHome + "/gui-hook"
private let binDir = env["HUDDLE_BIN_DIR"] ?? home + "/.local/bin"
private let cliLink = binDir + "/huddle-gui"
private let settingsPath = (env["CLAUDE_CONFIG_DIR"] ?? home + "/.claude") + "/settings.json"

private func say(_ s: String) { print(s) }
private func fail(_ s: String) -> Never { FileHandle.standardError.write(Data(("huddle-gui: " + s + "\n").utf8)); exit(1) }

/// The path to keep in links: the symlink it was invoked through (Homebrew repoints it on upgrade), else the real file.
private func helperPath() -> String {
    var p = CommandLine.arguments[0]
    if !p.hasPrefix("/") { p = fm.currentDirectoryPath + "/" + p }
    p = URL(fileURLWithPath: p).standardized.path
    if (try? fm.destinationOfSymbolicLink(atPath: p)) != nil { return p }
    return URL(fileURLWithPath: p).resolvingSymlinksInPath().path
}

private func link(_ path: String, to target: String) {
    try? fm.createDirectory(atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
    if (try? fm.destinationOfSymbolicLink(atPath: path)) != nil || fm.fileExists(atPath: path) { try? fm.removeItem(atPath: path) }
    do { try fm.createSymbolicLink(atPath: path, withDestinationPath: target) } catch { fail("cannot link \(path): \(error.localizedDescription)") }
}

private func loadSettings() -> [String: Any] {
    guard let d = fm.contents(atPath: settingsPath) else { return [:] }
    guard let o = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any] else { fail("\(settingsPath) is not valid JSON; fix it and run setup again") }
    return o
}

private func saveSettings(_ o: [String: Any]) {
    try? fm.createDirectory(atPath: (settingsPath as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
    let backup = settingsPath + ".huddle-bak"
    if fm.fileExists(atPath: settingsPath), !fm.fileExists(atPath: backup) { try? fm.copyItem(atPath: settingsPath, toPath: backup) }
    guard let d = try? JSONSerialization.data(withJSONObject: o, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]) else { fail("cannot encode settings") }
    do { try (d + Data("\n".utf8)).write(to: URL(fileURLWithPath: settingsPath), options: .atomic) } catch { fail("cannot write \(settingsPath): \(error.localizedDescription)") }
}

private func isOurs(_ group: Any) -> Bool {
    guard let g = group as? [String: Any], let hs = g["hooks"] as? [[String: Any]] else { return false }
    return hs.contains { ($0["command"] as? String)?.hasSuffix("/.huddle/gui-hook") == true || $0["command"] as? String == hookLink }
}

private func isTUI(_ group: Any) -> Bool {
    guard let g = group as? [String: Any], g["matcher"] as? String == "AskUserQuestion", let hs = g["hooks"] as? [[String: Any]] else { return false }
    return hs.contains { ($0["command"] as? String)?.hasSuffix("huddle hook") == true }
}

private func setup() {
    let me = helperPath()
    link(hookLink, to: me)
    link(cliLink, to: me)
    if !fm.fileExists(atPath: huddleHome + "/ui") { try? "gui\n".write(toFile: huddleHome + "/ui", atomically: true, encoding: .utf8) }

    var s = loadSettings()
    var hooks = s["hooks"] as? [String: Any] ?? [:]
    var pre = hooks["PreToolUse"] as? [Any] ?? []
    if pre.contains(where: isOurs) {
        say("Hook already registered in \(settingsPath)")
    } else if pre.contains(where: isTUI) {
        say("Your settings already call the huddle TUI hook; it hands over to the GUI because \(huddleHome)/ui says gui.")
    } else {
        pre.append(["matcher": "AskUserQuestion", "hooks": [["type": "command", "command": hookLink, "timeout": 3600,
                                                                "statusMessage": "Waiting for your answer in Huddle"]]])
        hooks["PreToolUse"] = pre
        s["hooks"] = hooks
        saveSettings(s)
        say("Registered the AskUserQuestion hook in \(settingsPath) (backup: settings.json.huddle-bak)")
    }
    say("Linked \(hookLink) and \(cliLink)")
    if !(env["PATH"] ?? "").split(separator: ":").contains(Substring(binDir)) { say("Note: \(binDir) is not on your PATH, so add it to run huddle-gui by name.") }
    say("Done. Restart running Claude Code sessions (or open /hooks) so they pick the hook up; the next AskUserQuestion opens Huddle.")
}

private func uninstall() {
    var s = loadSettings()
    if var hooks = s["hooks"] as? [String: Any], let pre = hooks["PreToolUse"] as? [Any] {
        let kept = pre.filter { !isOurs($0) }
        if kept.count != pre.count {
            if kept.isEmpty { hooks.removeValue(forKey: "PreToolUse") } else { hooks["PreToolUse"] = kept }
            if hooks.isEmpty { s.removeValue(forKey: "hooks") } else { s["hooks"] = hooks }
            saveSettings(s)
            say("Removed the hook from \(settingsPath)")
        }
    }
    for p in [hookLink, cliLink] where (try? fm.destinationOfSymbolicLink(atPath: p)) != nil { try? fm.removeItem(atPath: p) }
    say("Removed the links. Delete Huddle.app (and \(huddleHome) if you like) to finish.")
}

private func status() {
    let s = loadSettings()
    let pre = (s["hooks"] as? [String: Any])?["PreToolUse"] as? [Any] ?? []
    say("hook in settings : " + (pre.contains(where: isOurs) ? "yes" : pre.contains(where: isTUI) ? "TUI hook (hands over to GUI)" : "no") + "  (\(settingsPath))")
    say("hook link        : " + ((try? fm.destinationOfSymbolicLink(atPath: hookLink)) ?? "missing"))
    say("ui               : " + ((try? String(contentsOfFile: huddleHome + "/ui", encoding: .utf8))?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "gui (default)"))
    let claude = (["/.local/bin/claude", "/.claude/local/claude"].map { home + $0 } + ["/opt/homebrew/bin/claude", "/usr/local/bin/claude"]).first(where: fm.isExecutableFile)
    say("claude CLI       : " + (claude ?? "not found (the chat side agent needs it)"))
}

func runCLI(_ cmd: String) -> Never {
    switch cmd {
    case "setup": setup()
    case "uninstall": uninstall()
    case "status": status()
    default: say("usage: huddle-gui setup | uninstall | status")
    }
    exit(0)
}
