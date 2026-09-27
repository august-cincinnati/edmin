package main

import (
	"strconv"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
)

const gutterPad = 8 // pixels on each side of the numbers

// Gutter draws line numbers in the left border window of an editor's TextView.
type Gutter struct {
	e        *Editor
	font     *pango.FontDescription
	lineH    int // line height the font was fitted to
	digitW   int
	width    int
	lastLine int // cursor line at the last redraw
}

func NewGutter(e *Editor) *Gutter {
	g := &Gutter{e: e, lastLine: -1}
	e.View.ConnectAfter("draw", g.draw)
	e.Buf.Connect("changed", g.updateWidth)
	e.Buf.Connect("mark-set", func(_ *gtk.TextBuffer, _ *gtk.TextIter, m *gtk.TextMark) {
		if m.Native() != e.Buf.GetInsert().Native() {
			return
		}
		if l := e.Buf.GetIterAtMark(m).GetLine(); l != g.lastLine {
			e.View.QueueDraw() // move the current-line emphasis
		}
	})
	e.View.SetBorderWindowSize(gtk.TEXT_WINDOW_LEFT, 2*gutterPad+20)
	return g
}

// fitFont picks a monospace pixel size whose line height matches the editor's.
func (g *Gutter) fitFont(cr *cairo.Context, lineH int) {
	if g.font != nil && g.lineH == lineH {
		return
	}
	g.lineH = lineH
	g.font = pango.FontDescriptionFromString("monospace")
	probe := float64(lineH) * 0.8
	g.font.SetAbsoluteSize(probe * float64(pango.SCALE))
	l := pango.CairoCreateLayout(cr)
	l.SetFontDescription(g.font)
	l.SetText("0", -1)
	_, h := l.GetSize()
	if h > 0 {
		g.font.SetAbsoluteSize(probe * float64(lineH) / (float64(h) / float64(pango.SCALE)) * float64(pango.SCALE))
	}
	l.SetFontDescription(g.font)
	w, _ := l.GetSize()
	g.digitW = w / pango.SCALE
	g.width = 0
	g.updateWidth()
}

// updateWidth resizes the gutter to fit the largest line number.
func (g *Gutter) updateWidth() {
	if g.digitW == 0 {
		return
	}
	digits := max(len(strconv.Itoa(g.e.Buf.GetLineCount())), 2)
	if w := digits*g.digitW + 2*gutterPad; w != g.width {
		g.width = w
		g.e.View.SetBorderWindowSize(gtk.TEXT_WINDOW_LEFT, w)
	}
}

func (g *Gutter) draw(v *gtk.TextView, cr *cairo.Context) bool {
	// (gotk3's GetLineAtY returns an uninitialised iter, so go via location.)
	first := g.e.Buf.GetIterAtLine(v.GetIterAtLocation(0, v.GetVisibleRect().GetY()).GetLine())
	_, lineH := v.GetLineYrange(first)
	if lineH <= 0 {
		return false
	}
	g.fitFont(cr, lineH)

	fg := [4]float64{0.5, 0.5, 0.5, 1}
	if sc, err := v.GetStyleContext(); err == nil {
		c := sc.GetColor(gtk.STATE_FLAG_NORMAL)
		fg = [4]float64{c.GetRed(), c.GetGreen(), c.GetBlue(), c.GetAlpha()}
	}
	curLine := g.e.Buf.GetIterAtMark(g.e.Buf.GetInsert()).GetLine()
	g.lastLine = curLine

	vis := v.GetVisibleRect()
	bottom := vis.GetY() + vis.GetHeight()
	layout := pango.CairoCreateLayout(cr)
	layout.SetFontDescription(g.font)
	iter := first
	for {
		y, h := v.GetLineYrange(iter)
		if y > bottom {
			break
		}
		line := iter.GetLine()
		_, wy := v.BufferToWindowCoords(gtk.TEXT_WINDOW_WIDGET, 0, y)
		layout.SetText(strconv.Itoa(line+1), -1)
		tw, th := layout.GetSize()
		alpha := 0.45
		if line == curLine {
			alpha = 1
		}
		cr.SetSourceRGBA(fg[0], fg[1], fg[2], fg[3]*alpha)
		cr.MoveTo(float64(g.width-gutterPad-tw/pango.SCALE), float64(wy)+float64(h-th/pango.SCALE)/2)
		pango.CairoShowLayout(cr, layout)
		// ForwardLine reports false when it reaches the end iter, which may
		// still be the start of a final (empty) line that needs a number.
		if !iter.ForwardLine() && iter.GetLine() == line {
			break
		}
	}
	return false
}
