import SwiftUI
import AppKit
import Combine

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let store = Store()
    var window: NSWindow!
    var server: SocketServer!
    var status: NSStatusItem!
    var bag = Set<AnyCancellable>()
    var keyMonitor: Any?

    /// Up/Down/Space drive the choices and Tab flips between the question pane and the chat box, with no click needed first.
    func mainlineKey(_ e: NSEvent) -> Bool {
        guard e.window === window, window.isKeyWindow, window.attachedSheet == nil,
              e.modifierFlags.intersection([.command, .option, .control]).isEmpty,
              let call = store.current, !call.sendBackOpen else { return false }
        let field = window.firstResponder as? PlaceholderTextView
        if window.firstResponder is NSTextView && field == nil { return false }
        func chat() -> PlaceholderTextView? { Self.field("q\(call.tab).chat", in: window.contentView) }
        if e.keyCode == 123 || e.keyCode == 124 {
            if window.firstResponder is NSTextView { return false }
            let t = min(max(call.tab + (e.keyCode == 124 ? 1 : -1), 0), call.q.count)
            if t != call.tab { animate { call.tab = t } }
            return true
        }
        guard call.tab < call.q.count else { return false }
        switch e.keyCode {
        case 48:
            if let f = field {
                if f.key.hasSuffix(".chat") { window.makeFirstResponder(window.contentView); return true }
                if f.key.hasPrefix("q"), let c = chat() { window.makeFirstResponder(c); return true }
                return false
            }
            guard let c = chat() else { return false }
            window.makeFirstResponder(c)
            return true
        case 126, 125, 49:
            if let f = field {
                guard f.key.hasSuffix(".note"), e.keyCode == 126, f.caretOnFirstLine else { return false }
                window.makeFirstResponder(window.contentView)
                return call.keyHandlers[call.tab]?("last") ?? false
            }
            let k = e.keyCode == 126 ? "up" : e.keyCode == 125 ? "down" : "space"
            if call.keyHandlers[call.tab]?(k) == true { return true }
            if k == "down" {
                if let n = Self.field("q\(call.tab).note", in: window.contentView) { window.makeFirstResponder(n); return true }
                if call.live {
                    call.noteOpen.insert(call.tab)
                    let tab = call.tab
                    DispatchQueue.main.async { [window] in if let w = window, let n = Self.field("q\(tab).note", in: w.contentView) { w.makeFirstResponder(n) } }
                    return true
                }
            }
            return false
        default: return false
        }
    }

    static func field(_ key: String, in v: NSView?) -> PlaceholderTextView? {
        guard let v else { return nil }
        if let t = v as? PlaceholderTextView, t.key == key { return t }
        for s in v.subviews { if let t = field(key, in: s) { return t } }
        return nil
    }

    func applicationDidFinishLaunching(_ n: Notification) {
        signal(SIGPIPE, SIG_IGN)
        if let u = Bundle.main.url(forResource: "AppIcon", withExtension: "icns"), let img = NSImage(contentsOf: u) { NSApp.applicationIconImage = img }
        switch ProcessInfo.processInfo.environment["HUDDLE_APPEARANCE"] {
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        default: break
        }
        DispatchQueue.global().async { SideAgent.reapStale() }
        server = SocketServer(store: store)
        guard server.start() else { NSApp.terminate(nil); return }

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 760),
                          styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "Huddle"
        window.minSize = NSSize(width: 1000, height: 560)
        window.delegate = self
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: RootView(store: store))
        window.center()

        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        store.onChange = { [weak self] in self?.refreshStatus() }
        store.onArrival = { [weak self] in
            NSSound(named: "Glass")?.play()
            NSApp.setActivationPolicy(.regular)
            self?.window.orderFrontRegardless()
        }
        store.onEmpty = { [weak self] in self?.window.orderOut(nil); NSApp.setActivationPolicy(.accessory) }
        LayoutState.shared.$zoomed.sink { [weak self] z in self?.window.title = z ? "Huddle · chat zoomed" : "Huddle" }.store(in: &bag)
        refreshStatus()
        if !CommandLine.arguments.contains("--quiet") { DispatchQueue.main.async { [weak self] in self?.openWindow() } }
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] e in (self?.mainlineKey(e) ?? false) ? nil : e }
        installEditMenu()
        pruneCalls()
    }

    /// An accessory app has no main menu, and without an Edit menu macOS never delivers ⌘C, ⌘V or ⌘Z to text fields.
    func installEditMenu() {
        let main = NSMenu()
        let editItem = NSMenuItem()
        main.addItem(editItem)
        let edit = NSMenu(title: "Edit")
        func add(_ title: String, _ sel: Selector, _ key: String, shift: Bool = false) {
            let it = NSMenuItem(title: title, action: sel, keyEquivalent: key)
            if shift { it.keyEquivalentModifierMask = [.command, .shift] }
            edit.addItem(it)
        }
        add("Undo", Selector(("undo:")), "z")
        add("Redo", Selector(("redo:")), "z", shift: true)
        edit.addItem(.separator())
        add("Cut", #selector(NSText.cut(_:)), "x")
        add("Copy", #selector(NSText.copy(_:)), "c")
        add("Paste", #selector(NSText.paste(_:)), "v")
        add("Select All", #selector(NSText.selectAll(_:)), "a")
        editItem.submenu = edit
        NSApp.mainMenu = main
    }

    func pruneCalls() {
        let root = huddleHome + "/calls"
        for name in (try? FileManager.default.contentsOfDirectory(atPath: root)) ?? [] {
            let p = root + "/" + name
            if let d = (try? FileManager.default.attributesOfItem(atPath: p))?[.modificationDate] as? Date, Date().timeIntervalSince(d) > 7 * 86400 {
                try? FileManager.default.removeItem(atPath: p)
            }
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        openWindow()
        return true
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { sender.orderOut(nil); NSApp.setActivationPolicy(.accessory); return false }

    func applicationWillTerminate(_ n: Notification) { store.quitAll(); unlink(socketPath) }

    /// The menu bar mark from mock 12: the letter H and the waiting count inside a rounded outline.
    func statusImage(_ n: Int) -> NSImage {
        let text = n > 0 ? "H \(n)" : "H"
        let attrs: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: 12, weight: .semibold), .foregroundColor: NSColor.black]
        let size = (text as NSString).size(withAttributes: attrs)
        let img = NSImage(size: NSSize(width: ceil(size.width) + 14, height: 18), flipped: false) { r in
            let path = NSBezierPath(roundedRect: r.insetBy(dx: 1, dy: 1.5), xRadius: 5, yRadius: 5)
            path.lineWidth = 1.3
            NSColor.black.setStroke()
            path.stroke()
            (text as NSString).draw(at: NSPoint(x: 7, y: (r.height - size.height) / 2), withAttributes: attrs)
            return true
        }
        img.isTemplate = true
        return img
    }

    func rowTitle(_ left: String, _ right: String, bold: Bool = false, dimRight: Bool = true) -> NSAttributedString {
        let para = NSMutableParagraphStyle()
        para.tabStops = [NSTextTab(textAlignment: .right, location: 270)]
        let s = NSMutableAttributedString(string: left, attributes: [.font: NSFont.systemFont(ofSize: 13, weight: bold ? .semibold : .regular), .paragraphStyle: para])
        if right.isEmpty { return s }
        s.append(NSAttributedString(string: "\t" + right, attributes: [.font: NSFont.systemFont(ofSize: 13),
                                                                       .foregroundColor: dimRight ? NSColor.secondaryLabelColor : NSColor.labelColor, .paragraphStyle: para]))
        return s
    }

    func refreshStatus() {
        status.button?.image = statusImage(store.waiting)
        status.button?.title = ""
        let menu = NSMenu()
        let head = NSMenuItem()
        head.attributedTitle = rowTitle(store.waiting == 0 ? "Nothing waiting" : "\(store.waiting) waiting", "", bold: true)
        head.isEnabled = false
        menu.addItem(head)
        for c in store.calls {
            let left = store.remaining(c)
            let t: String
            switch c.state {
            case .expired: t = "Expired"
            case .cancelled: t = "Cancelled"
            default: t = (left < 300 ? "▲ " : "") + clock(left)
            }
            let item = NSMenuItem(title: c.sessionName, action: #selector(pick(_:)), keyEquivalent: "")
            item.attributedTitle = rowTitle(c.sessionName, t, dimRight: false)
            item.target = self
            item.representedObject = c.id
            menu.addItem(item)
        }
        menu.addItem(.separator())
        let open = NSMenuItem(title: "Open Huddle", action: #selector(openWindow), keyEquivalent: "")
        open.target = self
        menu.addItem(open)
        let quit = NSMenuItem(title: "Quit", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        quit.attributedTitle = rowTitle("Quit", "")
        menu.addItem(quit)
        status.menu = menu
    }

    @objc func pick(_ s: NSMenuItem) {
        store.selected = s.representedObject as? UUID
        openWindow()
    }

    @objc func openWindow() {
        NSApp.setActivationPolicy(.regular)
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
