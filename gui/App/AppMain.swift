import SwiftUI
import AppKit

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let store = Store()
    var window: NSWindow!
    var server: SocketServer!
    var status: NSStatusItem!

    func applicationDidFinishLaunching(_ n: Notification) {
        signal(SIGPIPE, SIG_IGN)
        server = SocketServer(store: store)
        guard server.start() else { NSApp.terminate(nil); return }

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 760),
                          styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "Huddle"
        window.minSize = NSSize(width: 760, height: 520)
        window.delegate = self
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: RootView(store: store))
        window.center()

        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        store.onChange = { [weak self] in self?.refreshStatus() }
        store.onArrival = { [weak self] in
            NSSound(named: "Glass")?.play()
            self?.window.orderFrontRegardless()
        }
        store.onEmpty = { [weak self] in self?.window.orderOut(nil) }
        refreshStatus()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { sender.orderOut(nil); return false }

    func applicationWillTerminate(_ n: Notification) { store.quitAll(); unlink(socketPath) }

    func refreshStatus() {
        status.button?.title = store.waiting > 0 ? "H \(store.waiting)" : "H"
        let menu = NSMenu()
        let head = NSMenuItem(title: store.waiting == 0 ? "Nothing waiting" : "\(store.waiting) waiting", action: nil, keyEquivalent: "")
        head.isEnabled = false
        menu.addItem(head)
        for c in store.calls {
            let t: String
            switch c.state {
            case .expired: t = "Expired"
            case .cancelled: t = "Cancelled"
            default: t = clock(store.remaining(c))
            }
            let item = NSMenuItem(title: "\(c.sessionName)   \(t)", action: #selector(pick(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = c.id
            menu.addItem(item)
        }
        menu.addItem(.separator())
        let open = NSMenuItem(title: "Open Huddle", action: #selector(openWindow), keyEquivalent: "")
        open.target = self
        menu.addItem(open)
        let quit = NSMenuItem(title: "Quit (calls fall back to the agent)", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        menu.addItem(quit)
        status.menu = menu
    }

    @objc func pick(_ s: NSMenuItem) {
        store.selected = s.representedObject as? UUID
        openWindow()
    }

    @objc func openWindow() {
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
}

@main enum HuddleMain {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.accessory)
        app.run()
    }
}
