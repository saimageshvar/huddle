import Foundation

struct Option: Codable {
    var label: String
    var description: String?
    var preview: String?
}

struct Question: Codable {
    var question: String
    var header: String?
    var multiSelect: Bool?
    var options: [Option]
}

struct Request: Codable {
    var sessionId: String
    var cwd: String
    var transcriptPath: String
    var title: String
    var branch: String
    var questions: [Question]
    var deadline: Int64
    var hookPid: Int32

    enum CodingKeys: String, CodingKey {
        case sessionId = "session_id", cwd, transcriptPath = "transcript_path", title, branch, questions, deadline, hookPid = "hook_pid"
    }
}

struct Response: Codable {
    var kind: String
    var answers: [String: String]?
    var annotations: [String: [String: String]]?
    var reason: String?
}

let huddleHome = NSHomeDirectory() + "/.huddle"
let socketPath = huddleHome + "/gui.sock"

func withSockAddr<T>(_ path: String, _ body: (UnsafePointer<sockaddr>, socklen_t) -> T) -> T {
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    withUnsafeMutablePointer(to: &addr.sun_path) {
        $0.withMemoryRebound(to: CChar.self, capacity: 104) { _ = strlcpy($0, path, 104) }
    }
    return withUnsafePointer(to: &addr) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { body($0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
    }
}

func connectToApp() -> Int32? {
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { return nil }
    let rc = withSockAddr(socketPath) { connect(fd, $0, $1) }
    if rc == 0 { return fd }
    close(fd)
    return nil
}

func writeAll(_ fd: Int32, _ data: Data) -> Bool {
    var off = 0
    return data.withUnsafeBytes { buf in
        while off < data.count {
            let n = write(fd, buf.baseAddress! + off, data.count - off)
            if n <= 0 { return false }
            off += n
        }
        return true
    }
}

/// Reads up to the first newline; nil when the peer closed before sending one.
func readLine(_ fd: Int32) -> Data? {
    var out = Data()
    var byte: UInt8 = 0
    while true {
        let n = read(fd, &byte, 1)
        if n <= 0 { return nil }
        if byte == 10 { return out }
        out.append(byte)
    }
}
