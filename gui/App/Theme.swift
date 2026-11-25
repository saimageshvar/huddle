import SwiftUI

enum T {
    static let border = Color.primary.opacity(0.18)
    static let faint = Color.primary.opacity(0.06)
    static let muted = Color.secondary
    static let ink = Color.primary
    static let inkText = Color(nsColor: .windowBackgroundColor)
}

struct Chip: View {
    let text: String
    var body: some View {
        Text(text).font(.caption)
            .padding(.horizontal, 6).padding(.vertical, 1)
            .overlay(RoundedRectangle(cornerRadius: 4).stroke(T.border))
    }
}

struct PrimaryButton: View {
    let title: String
    var enabled = true
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            Text(title).fontWeight(.semibold)
                .padding(.horizontal, 16).padding(.vertical, 8)
                .foregroundColor(T.inkText)
                .background(RoundedRectangle(cornerRadius: 8).fill(T.ink.opacity(enabled ? 1 : 0.35)))
        }.buttonStyle(.plain).disabled(!enabled)
    }
}

struct OutlineButton: View {
    let title: String
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            Text(title).padding(.horizontal, 12).padding(.vertical, 7)
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
        }.buttonStyle(.plain)
    }
}

struct LinkButton: View {
    let title: String
    let action: () -> Void
    var body: some View {
        Button(action: action) { Text(title).underline().font(.callout) }.buttonStyle(.plain)
    }
}

/// NSTextView draws the placeholder at the same inset as the text and cursor, so they cannot drift apart.
struct Attachment: Identifiable {
    let id = UUID()
    let path: String
    let image: NSImage
}

final class PlaceholderTextView: NSTextView {
    var placeholder = ""
    var onPasteImage: ((Data) -> Void)?

    /// An image on the pasteboard becomes an attachment; anything with text in it pastes as text.
    override func paste(_ sender: Any?) {
        let pb = NSPasteboard.general
        if onPasteImage != nil, pb.string(forType: .string) == nil, let data = Self.pngData(pb) {
            onPasteImage?(data)
            return
        }
        super.paste(sender)
    }

    static func pngData(_ pb: NSPasteboard) -> Data? {
        func png(_ d: Data) -> Data? { NSBitmapImageRep(data: d)?.representation(using: .png, properties: [:]) }
        if let d = pb.data(forType: .png) { return d }
        if let d = pb.data(forType: .tiff) { return png(d) }
        let opts: [NSPasteboard.ReadingOptionKey: Any] = [.urlReadingFileURLsOnly: true, .urlReadingContentsConformToTypes: ["public.image"]]
        if let u = (pb.readObjects(forClasses: [NSURL.self], options: opts) as? [URL])?.first, let d = try? Data(contentsOf: u) { return png(d) }
        return nil
    }
    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        guard string.isEmpty else { return }
        let x = textContainerInset.width + (textContainer?.lineFragmentPadding ?? 0)
        let rect = NSRect(x: x, y: textContainerInset.height, width: max(1, bounds.width - x - textContainerInset.width), height: bounds.height)
        (placeholder as NSString).draw(with: rect, options: [.usesLineFragmentOrigin], attributes: [.font: font ?? NSFont.systemFont(ofSize: 13), .foregroundColor: NSColor.placeholderTextColor])
    }
    override func didChangeText() { super.didChangeText(); needsDisplay = true }
}

struct TextArea: NSViewRepresentable {
    @Binding var text: String
    var placeholder = ""
    var minHeight: CGFloat = 60
    var maxHeight: CGFloat = 180
    var submitOnEnter = false
    var onSubmit: (() -> Void)? = nil
    var onPasteImage: ((Data) -> Void)? = nil
    static let inset = NSSize(width: 10, height: 9)

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let tv = PlaceholderTextView()
        tv.delegate = context.coordinator
        tv.isRichText = false
        tv.allowsUndo = true
        tv.drawsBackground = false
        tv.font = NSFont.preferredFont(forTextStyle: .body)
        tv.textColor = .labelColor
        tv.insertionPointColor = .labelColor
        tv.textContainerInset = Self.inset
        tv.textContainer?.lineFragmentPadding = 0
        tv.textContainer?.widthTracksTextView = true
        tv.isVerticallyResizable = true
        tv.autoresizingMask = [.width]
        tv.placeholder = placeholder
        tv.onPasteImage = onPasteImage
        tv.string = text
        let sv = NSScrollView()
        sv.drawsBackground = false
        sv.hasVerticalScroller = false
        sv.borderType = .noBorder
        sv.documentView = tv
        return sv
    }

    func updateNSView(_ sv: NSScrollView, context: Context) {
        context.coordinator.parent = self
        guard let tv = sv.documentView as? PlaceholderTextView else { return }
        if tv.string != text { tv.string = text }
        tv.placeholder = placeholder
        tv.onPasteImage = onPasteImage
        tv.needsDisplay = true
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView sv: NSScrollView, context: Context) -> CGSize? {
        guard let tv = sv.documentView as? NSTextView, let lm = tv.layoutManager, let tc = tv.textContainer else { return nil }
        let w = proposal.width ?? 300
        tc.containerSize = NSSize(width: max(1, w - 2 * Self.inset.width), height: .greatestFiniteMagnitude)
        lm.ensureLayout(for: tc)
        let used = lm.usedRect(for: tc).height + 2 * Self.inset.height
        sv.hasVerticalScroller = used > maxHeight
        return CGSize(width: w, height: min(max(used, minHeight), maxHeight))
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: TextArea
        init(_ p: TextArea) { parent = p }
        func textDidChange(_ n: Notification) {
            guard let tv = n.object as? NSTextView else { return }
            parent.text = tv.string
        }
        func textView(_ tv: NSTextView, doCommandBy sel: Selector) -> Bool {
            guard sel == #selector(NSResponder.insertNewline(_:)), parent.submitOnEnter,
                  !(NSApp.currentEvent?.modifierFlags.contains(.shift) ?? false) else { return false }
            parent.onSubmit?()
            return true
        }
    }
}

/// Boxed text area: the field plus its border, the look every text box in the app shares.
struct BoxedTextArea: View {
    @Binding var text: String
    var placeholder = ""
    var minHeight: CGFloat = 60
    var submitOnEnter = false
    var onSubmit: (() -> Void)? = nil
    var body: some View {
        TextArea(text: $text, placeholder: placeholder, minHeight: minHeight, submitOnEnter: submitOnEnter, onSubmit: onSubmit)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .textBackgroundColor)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
    }
}

/// The text box every answer, note and chat message uses: thumbnails of pasted images on top, text below.
struct AttachArea: View {
    @ObservedObject var call: CallModel
    let key: String
    @Binding var text: String
    var placeholder = ""
    var minHeight: CGFloat = 60
    var submitOnEnter = false
    var onSubmit: (() -> Void)? = nil

    var body: some View {
        let imgs = call.attachments[key] ?? []
        VStack(alignment: .leading, spacing: 0) {
            if !imgs.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(imgs) { a in
                            ZStack(alignment: .topTrailing) {
                                Image(nsImage: a.image).resizable().scaledToFill()
                                    .frame(width: 64, height: 64).clipShape(RoundedRectangle(cornerRadius: 8))
                                    .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
                                    .onTapGesture { call.bigImage = a.path }
                                Button { call.removeImage(key, a.id) } label: {
                                    Image(systemName: "xmark.circle.fill").foregroundColor(T.ink)
                                        .background(Circle().fill(Color(nsColor: .windowBackgroundColor)))
                                }.buttonStyle(.plain).offset(x: 6, y: -6)
                            }
                        }
                    }.padding(.horizontal, 10).padding(.top, 12).padding(.bottom, 2)
                }
            }
            TextArea(text: $text, placeholder: placeholder, minHeight: minHeight, submitOnEnter: submitOnEnter, onSubmit: onSubmit,
                     onPasteImage: { call.addImage($0, key) })
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .textBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
    }
}
