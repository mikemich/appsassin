// Renders an ANSI terminal capture (tmux capture-pane -e -p) as a PNG of a
// macOS terminal window. Used to make the README screenshots.
//
//   swift scripts/shot.swift capture.ansi out.png "window title"
import AppKit

let args = CommandLine.arguments
guard args.count >= 3 else {
    FileHandle.standardError.write("usage: shot.swift in.ansi out.png [title]\n".data(using: .utf8)!)
    exit(2)
}
let input = try! String(contentsOfFile: args[1], encoding: .utf8)
let title = args.count > 3 ? args[3] : "appsassin"

struct RGB { var r, g, b: CGFloat }
struct Cell { var ch: String; var fg: RGB?; var bg: RGB?; var bold: Bool }

func rgb(_ r: Int, _ g: Int, _ b: Int) -> RGB { RGB(r: CGFloat(r) / 255, g: CGFloat(g) / 255, b: CGFloat(b) / 255) }
let ansi16: [RGB] = [
    rgb(40, 42, 54), rgb(224, 36, 94), rgb(95, 215, 135), rgb(229, 192, 123),
    rgb(97, 175, 239), rgb(198, 120, 221), rgb(86, 182, 194), rgb(220, 220, 220),
    rgb(108, 108, 108), rgb(255, 85, 120), rgb(120, 230, 160), rgb(240, 210, 140),
    rgb(130, 195, 255), rgb(215, 150, 235), rgb(110, 200, 210), rgb(255, 255, 255),
]
func xterm256(_ n: Int) -> RGB {
    if n < 16 { return ansi16[n] }
    if n >= 232 { let v = 8 + (n - 232) * 10; return rgb(v, v, v) }
    let i = n - 16
    let lv = [0, 95, 135, 175, 215, 255]
    return rgb(lv[i / 36], lv[(i / 6) % 6], lv[i % 6])
}

// Parse ANSI into a grid of cells.
var grid: [[Cell]] = []
var fg: RGB? = nil, bg: RGB? = nil, bold = false // SGR state carries across lines
for line in input.split(separator: "\n", omittingEmptySubsequences: false) {
    var row: [Cell] = []
    var it = Array(line.unicodeScalars)[...]
    var text = String.UnicodeScalarView()
    func flush() {
        for ch in String(text) { row.append(Cell(ch: String(ch), fg: fg, bg: bg, bold: bold)) }
        text = String.UnicodeScalarView()
    }
    while let c = it.first {
        it = it.dropFirst()
        if c == "\u{1B}", it.first == "[" {
            it = it.dropFirst()
            var params = ""
            while let p = it.first, !("@"..."~").contains(p) { params.unicodeScalars.append(p); it = it.dropFirst() }
            let final = it.first; it = it.dropFirst()
            guard final == "m" else { continue }
            flush()
            var codes = params.split(separator: ";").map { Int($0) ?? 0 }
            if codes.isEmpty { codes = [0] }
            var i = 0
            while i < codes.count {
                let k = codes[i]
                switch k {
                case 0: fg = nil; bg = nil; bold = false
                case 1: bold = true
                case 22: bold = false
                case 30...37: fg = ansi16[k - 30]
                case 90...97: fg = ansi16[k - 90 + 8]
                case 40...47: bg = ansi16[k - 40]
                case 100...107: bg = ansi16[k - 100 + 8]
                case 39: fg = nil
                case 49: bg = nil
                case 38, 48:
                    var col: RGB? = nil
                    if i + 1 < codes.count, codes[i + 1] == 2, i + 4 < codes.count {
                        col = rgb(codes[i + 2], codes[i + 3], codes[i + 4]); i += 4
                    } else if i + 1 < codes.count, codes[i + 1] == 5, i + 2 < codes.count {
                        col = xterm256(codes[i + 2]); i += 2
                    }
                    if k == 38 { fg = col } else { bg = col }
                default: break
                }
                i += 1
            }
        } else {
            text.append(c)
        }
    }
    flush()
    grid.append(row)
}
while let last = grid.last, last.allSatisfy({ $0.ch == " " && $0.bg == nil }) { grid.removeLast() }

// Geometry.
let scale: CGFloat = 2
let fontSize: CGFloat = 13.5
let regular = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
let boldFont = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)
let cellW = ("M" as NSString).size(withAttributes: [.font: regular]).width
let cellH = ceil(fontSize * 1.42)
let cols = max(grid.map(\.count).max() ?? 80, 40)
let rows = grid.count
let pad: CGFloat = 18, titleH: CGFloat = 34, margin: CGFloat = 46
let winW = CGFloat(cols) * cellW + pad * 2
let winH = CGFloat(rows) * cellH + pad * 2 + titleH
let W = winW + margin * 2, H = winH + margin * 2

let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(W * scale), pixelsHigh: Int(H * scale),
                           bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                           colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
rep.size = NSSize(width: W, height: H)
NSGraphicsContext.saveGraphicsState()
let cg = NSGraphicsContext(bitmapImageRep: rep)!.cgContext
cg.translateBy(x: 0, y: H); cg.scaleBy(x: 1, y: -1) // top-left origin, like a terminal (rep.size already applies @2x)
NSGraphicsContext.current = NSGraphicsContext(cgContext: cg, flipped: true)

func color(_ c: RGB, _ a: CGFloat = 1) -> NSColor { NSColor(deviceRed: c.r, green: c.g, blue: c.b, alpha: a) }

// Backdrop.
NSGradient(colors: [color(rgb(58, 12, 30)), color(rgb(18, 10, 24)), color(rgb(10, 12, 22))])!
    .draw(in: NSRect(x: 0, y: 0, width: W, height: H), angle: 60)

// Window with shadow.
let win = NSRect(x: margin, y: margin, width: winW, height: winH)
let shape = NSBezierPath(roundedRect: win, xRadius: 12, yRadius: 12)
NSGraphicsContext.saveGraphicsState()
let shadow = NSShadow()
shadow.shadowColor = NSColor.black.withAlphaComponent(0.55)
shadow.shadowBlurRadius = 34
shadow.shadowOffset = NSSize(width: 0, height: 14)
shadow.set()
color(rgb(21, 20, 26)).setFill(); shape.fill()
NSGraphicsContext.restoreGraphicsState()
color(rgb(21, 20, 26)).setFill(); shape.fill()

// Title bar.
NSGraphicsContext.saveGraphicsState()
shape.addClip()
color(rgb(32, 30, 38)).setFill()
NSRect(x: win.minX, y: win.minY, width: win.width, height: titleH).fill()
color(rgb(48, 44, 54)).setFill()
NSRect(x: win.minX, y: win.minY + titleH - 1, width: win.width, height: 1).fill()
NSGraphicsContext.restoreGraphicsState()
for (i, c) in [rgb(255, 95, 87), rgb(254, 188, 46), rgb(40, 200, 64)].enumerated() {
    color(c).setFill()
    NSBezierPath(ovalIn: NSRect(x: win.minX + 14 + CGFloat(i) * 20, y: win.minY + titleH / 2 - 6, width: 12, height: 12)).fill()
}
let tAttrs: [NSAttributedString.Key: Any] = [
    .font: NSFont.systemFont(ofSize: 12.5, weight: .medium), .foregroundColor: color(rgb(150, 145, 160)),
]
let tSize = (title as NSString).size(withAttributes: tAttrs)
(title as NSString).draw(at: NSPoint(x: win.midX - tSize.width / 2, y: win.minY + (titleH - tSize.height) / 2), withAttributes: tAttrs)

// Cells.
let ox = win.minX + pad, oy = win.minY + titleH + pad
let defaultFG = rgb(228, 228, 228)
for (r, row) in grid.enumerated() {
    for (c, cell) in row.enumerated() where cell.bg != nil {
        color(cell.bg!).setFill()
        NSRect(x: ox + CGFloat(c) * cellW, y: oy + CGFloat(r) * cellH, width: cellW + 0.6, height: cellH).fill()
    }
}
let para = NSMutableParagraphStyle()
para.lineBreakMode = .byClipping
// Box-drawing glyphs are drawn as paths so borders join across tall lines.
func boxPath(_ ch: String, _ rect: NSRect) -> NSBezierPath? {
    let p = NSBezierPath()
    p.lineWidth = 1.2
    let cx = rect.midX, cy = rect.midY, rad: CGFloat = min(rect.width, rect.height) / 2
    switch ch {
    case "─": p.move(to: NSPoint(x: rect.minX, y: cy)); p.line(to: NSPoint(x: rect.maxX, y: cy))
    case "│": p.move(to: NSPoint(x: cx, y: rect.minY)); p.line(to: NSPoint(x: cx, y: rect.maxY))
    case "╭":
        p.move(to: NSPoint(x: rect.maxX, y: cy)); p.line(to: NSPoint(x: cx + rad, y: cy))
        p.appendArc(withCenter: NSPoint(x: cx + rad, y: cy + rad), radius: rad, startAngle: 270, endAngle: 180, clockwise: true)
        p.line(to: NSPoint(x: cx, y: rect.maxY))
    case "╮":
        p.move(to: NSPoint(x: rect.minX, y: cy)); p.line(to: NSPoint(x: cx - rad, y: cy))
        p.appendArc(withCenter: NSPoint(x: cx - rad, y: cy + rad), radius: rad, startAngle: 270, endAngle: 0, clockwise: false)
        p.line(to: NSPoint(x: cx, y: rect.maxY))
    case "╰":
        p.move(to: NSPoint(x: cx, y: rect.minY)); p.line(to: NSPoint(x: cx, y: cy - rad))
        p.appendArc(withCenter: NSPoint(x: cx + rad, y: cy - rad), radius: rad, startAngle: 180, endAngle: 90, clockwise: true)
        p.line(to: NSPoint(x: rect.maxX, y: cy))
    case "╯":
        p.move(to: NSPoint(x: cx, y: rect.minY)); p.line(to: NSPoint(x: cx, y: cy - rad))
        p.appendArc(withCenter: NSPoint(x: cx - rad, y: cy - rad), radius: rad, startAngle: 0, endAngle: 90, clockwise: false)
        p.line(to: NSPoint(x: rect.minX, y: cy))
    default: return nil
    }
    return p
}

for (r, row) in grid.enumerated() {
    for (c, cell) in row.enumerated() where cell.ch != " " {
        let rect = NSRect(x: ox + CGFloat(c) * cellW, y: oy + CGFloat(r) * cellH, width: cellW, height: cellH)
        if let path = boxPath(cell.ch, rect) {
            color(cell.fg ?? defaultFG).setStroke(); path.stroke(); continue
        }
        var ch = cell.ch
        if ["⚔", "☠"].contains(ch) { ch += "\u{FE0E}" } // text, not emoji, presentation
        let attrs: [NSAttributedString.Key: Any] = [
            .font: cell.bold ? boldFont : regular, .foregroundColor: color(cell.fg ?? defaultFG), .paragraphStyle: para,
        ]
        let s = NSAttributedString(string: ch, attributes: attrs)
        let gw = s.size().width
        // Center fallback glyphs in their cell so the grid stays aligned.
        let x = ox + CGFloat(c) * cellW + (gw > cellW * 1.05 ? 0 : (cellW - gw) / 2)
        s.draw(at: NSPoint(x: x, y: oy + CGFloat(r) * cellH + (cellH - regular.ascender + regular.descender) / 2 - 1))
    }
}

NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: args[2]))
