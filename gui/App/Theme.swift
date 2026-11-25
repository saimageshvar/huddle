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

struct TextArea: View {
    @Binding var text: String
    var placeholder = ""
    var minHeight: CGFloat = 60
    var body: some View {
        ZStack(alignment: .topLeading) {
            TextEditor(text: $text)
                .font(.body).scrollContentBackground(.hidden)
                .frame(minHeight: minHeight, maxHeight: 180)
                .padding(4)
            if text.isEmpty {
                Text(placeholder).foregroundColor(T.muted).padding(.horizontal, 9).padding(.vertical, 12).allowsHitTesting(false)
            }
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .textBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(T.border))
    }
}
