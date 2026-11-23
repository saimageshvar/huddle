package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Images use Kitty graphics with Unicode placeholders: the picture is sent to the terminal once,
// then drawn by ordinary placeholder characters, so Bubble Tea lays it out, clips and redraws it
// like any other text.

const placeholder = '\U0010EEEE'

// cellAspect is a cell's height over its width in pixels (Rex reports about 37x17).
const cellAspect = 2.2

type kitty struct {
	ids  map[string]int // path|cols|rows -> image id
	next int
	dims map[string]image.Point
}

func newKitty() *kitty { return &kitty{ids: map[string]int{}, next: 1, dims: map[string]image.Point{}} }

// size fits an image into at most maxCols x maxRows cells, keeping its shape.
func (k *kitty) size(path string, maxCols, maxRows int) (int, int) {
	d, ok := k.dims[path]
	if !ok {
		f, err := os.Open(path)
		if err != nil {
			return 0, 0
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			return 0, 0
		}
		d = image.Pt(cfg.Width, cfg.Height)
		k.dims[path] = d
	}
	maxRows = min(maxRows, len(diacritics))
	cols := maxCols
	rows := int(float64(cols) * float64(d.Y) / float64(d.X) / cellAspect)
	if rows > maxRows {
		rows = maxRows
		cols = int(float64(rows) * cellAspect * float64(d.X) / float64(d.Y))
	}
	return max(cols, 1), max(rows, 1)
}

// place returns the escape that sends the image (empty if already sent) and its placeholder block.
func (k *kitty) place(path string, cols, rows int) (send, block string) {
	key := fmt.Sprintf("%s|%d|%d", path, cols, rows)
	id, ok := k.ids[key]
	if !ok {
		if k.next > 255 {
			return "", ""
		}
		data, err := pngBytes(path)
		if err != nil {
			return "", ""
		}
		id = k.next
		k.next++
		k.ids[key] = id
		send = transmit(id, cols, rows, data)
	}
	return send, placeholders(id, cols, rows)
}

func pngBytes(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(raw, []byte("\x89PNG")) {
		return raw, nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	err = png.Encode(&b, img)
	return b.Bytes(), err
}

// transmit sends a PNG as a virtual placement (U=1) of cols x rows cells, in 4 KB chunks.
func transmit(id, cols, rows int, data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for i := 0; i < len(enc); i += 4096 {
		chunk := enc[i:min(i+4096, len(enc))]
		more := 0
		if i+4096 < len(enc) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,i=%d,U=1,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, chunk)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return b.String()
}

// placeholders is the text that shows image id: one placeholder per cell, each marked with its
// row and column, coloured with the id.
func placeholders(id, cols, rows int) string {
	lines := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		fmt.Fprintf(&b, "\x1b[38;5;%dm", id)
		for c := range min(cols, len(diacritics)) {
			b.WriteRune(placeholder)
			b.WriteRune(diacritics[r])
			b.WriteRune(diacritics[c])
		}
		b.WriteString("\x1b[39m")
		lines[r] = b.String()
	}
	return strings.Join(lines, "\n")
}

var imagePath = regexp.MustCompile(`(?i)(?:'([^']+\.(?:png|jpe?g|gif))'|"([^"]+\.(?:png|jpe?g|gif))"|((?:[~/]|[A-Za-z]:)(?:\\ |[^\s'"])+\.(?:png|jpe?g|gif)))`)

// imagesIn finds image files named in text, as pasted paths (quoted or with escaped spaces).
func imagesIn(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range imagePath.FindAllStringSubmatch(text, -1) {
		p := m[1] + m[2] + m[3]
		p = strings.ReplaceAll(p, `\ `, " ")
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(os.Getenv("HOME"), p[2:])
		}
		if !seen[p] && exists(p) {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// icon places a generated PNG (keyed by name) as cols x rows cells.
func (k *kitty) icon(name string, data func() []byte, cols, rows int) (send, block string) {
	key := "icon:" + name
	id, ok := k.ids[key]
	if !ok {
		if k.next > 255 {
			return "", ""
		}
		id = k.next
		k.next++
		k.ids[key] = id
		send = transmit(id, cols, rows, data())
	}
	return send, placeholders(id, cols, rows)
}

// markerPNG draws a radio button (circle) or checkbox (rounded square), 4x supersampled for
// smooth edges. A chosen radio gets a filled inner dot; a chosen checkbox fills with a tick.
func markerPNG(multi, on bool, line, accent, tick color.RGBA) []byte {
	const n, ss = 64, 4
	big := n * ss
	cover := func(x, y float64) color.RGBA { // the colour at a point, in 0..1 space
		cx, cy := x-0.5, y-0.5
		if !multi {
			d := math.Hypot(cx, cy)
			switch {
			case on && d <= 0.20:
				return accent
			case d <= 0.40 && d >= 0.33:
				if on {
					return accent
				}
				return line
			}
			return color.RGBA{}
		}
		// rounded square from 0.12 to 0.88, corner radius 0.14
		inside := func(inset float64) bool {
			lo, hi, r := 0.12+inset, 0.88-inset, 0.14-inset
			qx := math.Max(math.Max(lo+r-x, x-(hi-r)), 0)
			qy := math.Max(math.Max(lo+r-y, y-(hi-r)), 0)
			return x >= lo && x <= hi && y >= lo && y <= hi && math.Hypot(qx, qy) <= r
		}
		if on {
			if !inside(0) {
				return color.RGBA{}
			}
			if segDist(x, y, 0.28, 0.52, 0.44, 0.67) < 0.055 || segDist(x, y, 0.44, 0.67, 0.73, 0.35) < 0.055 {
				return tick
			}
			return accent
		}
		if inside(0) && !inside(0.065) {
			return line
		}
		return color.RGBA{}
	}
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for py := range n {
		for px := range n {
			var r, g, b, a int
			for sy := range ss {
				for sx := range ss {
					c := cover((float64(px*ss+sx)+0.5)/float64(big), (float64(py*ss+sy)+0.5)/float64(big))
					r, g, b, a = r+int(c.R)*int(c.A)/255, g+int(c.G)*int(c.A)/255, b+int(c.B)*int(c.A)/255, a+int(c.A)
				}
			}
			s := ss * ss
			img.Set(px, py, color.RGBA{uint8(r / s), uint8(g / s), uint8(b / s), uint8(a / s)})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}
