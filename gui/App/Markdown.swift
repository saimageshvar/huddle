import SwiftUI

/// Renders Markdown with Foundation's parser: it reports each run's block structure (headings, lists, tables,
/// code fences, quotes) and this file draws every block itself, so it matches the design and needs no package.
struct MDList { var items: [(depth: Int, marker: String, text: AttributedString)] = [] }
struct MDTable { var header: [AttributedString] = []; var rows: [[AttributedString]] = [] }

enum MDBlock: Identifiable {
    case paragraph(Int, AttributedString)
    case heading(Int, Int, AttributedString)
    case code(Int, String, String)
    case list(Int, MDList)
    case table(Int, MDTable)
    case quote(Int, AttributedString)

    var id: Int {
        switch self {
        case .paragraph(let i, _), .heading(let i, _, _), .code(let i, _, _), .list(let i, _), .table(let i, _), .quote(let i, _): return i
        }
    }
}

enum MD {
    /// A preview with no Markdown markup that is clearly a mockup or a diff keeps its exact spacing in a monospace box.
    static func isMockup(_ text: String) -> Bool {
        if text.contains("```") { return false }
        let lines = text.components(separatedBy: "\n")
        let markdown = lines.contains { l in
            let t = l.trimmingCharacters(in: .whitespaces)
            return t.hasPrefix("# ") || t.hasPrefix("## ") || t.hasPrefix("### ") || t.hasPrefix("- ") || t.hasPrefix("* ")
                || (t.first?.isNumber == true && t.contains(". ")) || t.contains("**") || t.contains("`")
        }
        if markdown { return false }
        let box = CharacterSet(charactersIn: "─│┌┐└┘├┤┬┴┼═║╔╗╚╝▀▄█▌▐░▒▓")
        if text.unicodeScalars.contains(where: { box.contains($0) }) { return true }
        if lines.contains(where: { $0.hasPrefix("@@") || $0.hasPrefix("+++ ") || $0.hasPrefix("--- ") }) { return true }
        if lines.contains(where: { $0.hasPrefix("+--") || $0.hasPrefix("|--") }) { return true }
        let indented = lines.filter { $0.hasPrefix("  ") || $0.hasPrefix("\t") }.count
        return lines.count >= 3 && indented * 2 >= lines.count
    }

    /// Chat replies use single newlines as line breaks; Markdown would fold them into one line.
    static func hardBreaks(_ text: String) -> String {
        let lines = text.components(separatedBy: "\n")
        var out: [String] = []
        var fenced = false
        for (i, l) in lines.enumerated() {
            if l.trimmingCharacters(in: .whitespaces).hasPrefix("```") { fenced.toggle(); out.append(l); continue }
            let next = i + 1 < lines.count ? lines[i + 1] : ""
            let t = l.trimmingCharacters(in: .whitespaces)
            let nt = next.trimmingCharacters(in: .whitespaces)
            let joins = !fenced && !t.isEmpty && !nt.isEmpty && !t.hasPrefix("|") && !nt.hasPrefix("```")
            out.append(joins ? l + "  " : l)
        }
        return out.joined(separator: "\n")
    }

    static func parse(_ text: String) -> [MDBlock] {
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .full, failurePolicy: .returnPartiallyParsedIfPossible)
        guard let a = try? AttributedString(markdown: hardBreaks(text), options: opts) else { return [.paragraph(0, AttributedString(text))] }
        var blocks: [MDBlock] = []
        var key = -1
        var cur: MDBlock?
        var list = MDList()
        var table = MDTable()
        var itemKey = -1
        var row = -1
        var cells: [AttributedString] = []
        var isHeader = false

        func flush() {
            if let c = cur { blocks.append(c); cur = nil }
            if !list.items.isEmpty { blocks.append(.list(key, list)); list = MDList() }
            if !table.header.isEmpty || !table.rows.isEmpty {
                if !cells.isEmpty { if isHeader { table.header = cells } else { table.rows.append(cells) }; cells = [] }
                blocks.append(.table(key, table)); table = MDTable()
            }
        }

        for run in a.runs {
            var piece = AttributedString(a[run.range])
            if run.inlinePresentationIntent?.contains(.code) == true {
                piece.font = .system(.body, design: .monospaced)
                piece.backgroundColor = Color.primary.opacity(0.08)
            }
            guard let pi = run.presentationIntent else { var p = piece; p.foregroundColor = nil; if case .paragraph(let i, var t)? = cur { t += p; cur = .paragraph(i, t) } else { flush(); cur = .paragraph(0, p) }; continue }
            let comps = pi.components
            guard let top = comps.last else { continue }
            if top.identity != key { flush(); key = top.identity; itemKey = -1; row = -1; isHeader = false }
            switch top.kind {
            case .header(let level):
                if case .heading(let i, let l, var t)? = cur { t += piece; cur = .heading(i, l, t) } else { cur = .heading(key, level, piece) }
            case .codeBlock(let lang):
                let s = String(a[run.range].characters)
                if case .code(let i, let l, let c)? = cur { cur = .code(i, l, c + s) } else { cur = .code(key, lang ?? "", s) }
            case .unorderedList, .orderedList:
                let items = comps.filter { if case .listItem = $0.kind { return true }; return false }
                guard let leaf = items.first, case .listItem(let ordinal) = leaf.kind else { break }
                let depth = items.count - 1
                if leaf.identity == itemKey, !list.items.isEmpty {
                    list.items[list.items.count - 1].text += piece
                } else {
                    itemKey = leaf.identity
                    var ordered = false
                    if let owner = comps.first(where: { if case .orderedList = $0.kind { return true }; return false }), owner.identity != 0 {
                        ordered = comps.first(where: { c in
                            if case .orderedList = c.kind { return true }; if case .unorderedList = c.kind { return true }; return false
                        }).map { if case .orderedList = $0.kind { return true }; return false } ?? false
                    }
                    list.items.append((depth, ordered ? "\(ordinal)." : "•", piece))
                }
            case .table:
                let rowComp = comps.first { if case .tableRow = $0.kind { return true }; if case .tableHeaderRow = $0.kind { return true }; return false }
                var rowIndex = 0
                var header = false
                if let rc = rowComp { if case .tableRow(let r) = rc.kind { rowIndex = r }; if case .tableHeaderRow = rc.kind { header = true } }
                var colIndex = 0
                if let cc = comps.first(where: { if case .tableCell = $0.kind { return true }; return false }), case .tableCell(let c) = cc.kind { colIndex = c }
                if (header ? -1 : rowIndex) != row && !cells.isEmpty {
                    if isHeader { table.header = cells } else { table.rows.append(cells) }
                    cells = []
                }
                row = header ? -1 : rowIndex
                isHeader = header
                if colIndex < cells.count { cells[colIndex] += piece } else { cells.append(piece) }
            case .blockQuote:
                if case .quote(let i, var t)? = cur { t += piece; cur = .quote(i, t) } else { cur = .quote(key, piece) }
            default:
                if case .paragraph(let i, var t)? = cur, i == key { t += piece; cur = .paragraph(i, t) } else { cur = .paragraph(key, piece) }
            }
        }
        flush()
        return blocks
    }
}

struct MarkdownView: View {
    let text: String
    var body: some View {
        let blocks = MD.parse(text)
        VStack(alignment: .leading, spacing: 10) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, b in view(b) }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .textSelection(.enabled)
    }

    @ViewBuilder func view(_ b: MDBlock) -> some View {
        switch b {
        case .paragraph(_, let t): Text(t).fixedSize(horizontal: false, vertical: true)
        case .quote(_, let t):
            HStack(spacing: 10) {
                Rectangle().fill(T.border).frame(width: 3)
                Text(t).foregroundColor(T.muted).fixedSize(horizontal: false, vertical: true)
            }
        case .heading(_, let level, let t):
            Text(t).font(level <= 1 ? .title2.bold() : level == 2 ? .title3.bold() : .headline).fixedSize(horizontal: false, vertical: true)
        case .code(_, _, let c): CodeBlock(code: c.trimmingCharacters(in: .newlines))
        case .list(_, let l):
            VStack(alignment: .leading, spacing: 4) {
                ForEach(Array(l.items.enumerated()), id: \.offset) { _, it in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(it.marker).foregroundColor(T.muted).frame(minWidth: 14, alignment: .trailing)
                        Text(it.text).fixedSize(horizontal: false, vertical: true)
                    }.padding(.leading, CGFloat(it.depth) * 18)
                }
            }
        case .table(_, let t): TableBlock(table: t)
        }
    }
}

struct CodeBlock: View {
    let code: String
    var body: some View {
        ZStack(alignment: .topTrailing) {
            ScrollView(.horizontal, showsIndicators: false) {
                Text(code).font(.system(.callout, design: .monospaced)).fixedSize()
                    .padding(.horizontal, 12).padding(.vertical, 10).padding(.trailing, 56)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(code, forType: .string)
            } label: {
                HStack(spacing: 4) { Image(systemName: "doc.on.doc"); Text("Copy") }
                    .font(.caption).padding(.horizontal, 8).padding(.vertical, 3)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .textBackgroundColor)))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(T.border))
            }.buttonStyle(.plain).padding(6)
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.07)))
    }
}

struct TableBlock: View {
    let table: MDTable
    var body: some View {
        let cols = max(table.header.count, table.rows.map { $0.count }.max() ?? 0)
        ScrollView(.horizontal, showsIndicators: false) {
            Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
                GridRow {
                    ForEach(0..<cols, id: \.self) { c in
                        cell(c < table.header.count ? table.header[c] : AttributedString(""), bold: true).background(T.faint)
                    }
                }
                ForEach(Array(table.rows.enumerated()), id: \.offset) { _, r in
                    GridRow {
                        ForEach(0..<cols, id: \.self) { c in cell(c < r.count ? r[c] : AttributedString(""), bold: false) }
                    }
                }
            }
            .overlay(Rectangle().stroke(T.border))
        }
    }

    func cell(_ t: AttributedString, bold: Bool) -> some View {
        Text(t).fontWeight(bold ? .semibold : .regular).fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, 10).padding(.vertical, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
            .overlay(Rectangle().stroke(T.border, lineWidth: 0.5))
    }
}

/// Mockups and diffs: shown verbatim so the alignment survives.
struct VerbatimBlock: View {
    let text: String
    var body: some View {
        ScrollView([.horizontal, .vertical]) {
            Text(text).font(.system(.callout, design: .monospaced)).fixedSize().textSelection(.enabled)
                .padding(12).frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.07)))
    }
}
