import Foundation

final class SocketServer {
    private let store: Store
    init(store: Store) { self.store = store }

    /// False when another Huddle already owns the socket.
    func start() -> Bool {
        try? FileManager.default.createDirectory(atPath: huddleHome, withIntermediateDirectories: true)
        if let probe = connectToApp() { close(probe); return false }
        unlink(socketPath)
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0, withSockAddr(socketPath, { bind(fd, $0, $1) }) == 0, listen(fd, 16) == 0 else { return false }
        chmod(socketPath, 0o600)
        Thread.detachNewThread { [store] in
            while true {
                let client = accept(fd, nil, nil)
                if client < 0 { continue }
                Thread.detachNewThread { SocketServer.serve(client, store) }
            }
        }
        return true
    }

    private static func serve(_ fd: Int32, _ store: Store) {
        var one: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
        guard let line = readLine(fd), let req = try? JSONDecoder().decode(Request.self, from: line) else { close(fd); return }
        DispatchQueue.main.async { store.add(req, fd: fd) }
        var b: UInt8 = 0
        while read(fd, &b, 1) > 0 {}
        DispatchQueue.main.sync { store.cancel(fd: fd) }
        close(fd)
    }
}
