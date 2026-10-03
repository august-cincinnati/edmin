package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const termScrollback = 5000

// Terminal is a GTK widget hosting a shell in a pty, rendered through VT.
type Terminal struct {
	Root  *gtk.ScrolledWindow
	view  *gtk.TextView
	buf   *gtk.TextBuffer
	vt    *VT
	pty   *os.File
	cmd   *exec.Cmd
	dir   string
	title *gtk.Label

	tags      map[attr]*gtk.TextTag
	cursorTag *gtk.TextTag

	charW, charH int
	started      bool
	dirty        atomic.Bool
	closed       atomic.Bool
	onExit       func()
	theme        *Theme
	shell        []string
	sbLines      int      // scrollback lines currently in the buffer
	shown        [][]cell // every line in the buffer, as last rendered
	cursorLine   int      // buffer line holding the cursor, or -1
	follow       bool     // keep the view pinned to the bottom
}

// NewTerminal runs shell (the user's shell if empty) in dir.
func NewTerminal(dir string, shell []string, theme *Theme, onExit func()) *Terminal {
	t := &Terminal{dir: dir, shell: shell, theme: theme, onExit: onExit, tags: map[attr]*gtk.TextTag{}, cursorLine: -1}
	t.Root, _ = gtk.ScrolledWindowNew(nil, nil)
	t.Root.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_ALWAYS)
	t.view, _ = gtk.TextViewNew()
	t.view.SetEditable(false)
	t.view.SetCursorVisible(false)
	t.view.SetMonospace(true)
	t.view.SetWrapMode(gtk.WRAP_NONE)
	t.view.SetCanFocus(true)
	t.view.SetName("edmin-terminal")
	t.Root.Add(t.view)
	t.buf, _ = t.view.GetBuffer()

	t.cursorTag = t.buf.CreateTag("cursor", map[string]interface{}{"background": theme.TermFG, "foreground": theme.TermBG})

	t.buf.SetText("MM\nMM")
	t.follow = true
	// Follow mode changes only through user scrolling; while following, the
	// view is pinned to the bottom whenever the content grows.
	adj := t.Root.GetVAdjustment()
	updateFollow := func() bool {
		t.follow = adj.GetValue()+adj.GetPageSize() >= adj.GetUpper()-float64(max(t.charH, 1))*1.5
		return false
	}
	adj.Connect("changed", func() {
		if t.follow {
			adj.SetValue(adj.GetUpper() - adj.GetPageSize())
		}
	})
	t.view.Connect("scroll-event", func() bool { glib.IdleAdd(updateFollow); return false })
	sb := t.Root.GetVScrollbar()
	sb.Connect("button-release-event", func() bool { glib.IdleAdd(updateFollow); return false })
	t.view.Connect("size-allocate", func() { glib.IdleAdd(t.onResize) })
	t.view.Connect("key-press-event", t.onKey)
	t.view.Connect("button-press-event", func(_ *gtk.TextView, ev *gdk.Event) bool {
		t.view.GrabFocus()
		return false
	})
	return t
}

func (t *Terminal) measure() bool {
	if t.charW > 0 {
		return true
	}
	a := t.buf.GetStartIter()
	b := t.buf.GetIterAtOffset(1)
	ra, rb := t.view.GetIterLocation(a), t.view.GetIterLocation(b)
	if rb.GetX()-ra.GetX() <= 0 || ra.GetHeight() <= 0 {
		return false
	}
	t.charW, t.charH = rb.GetX()-ra.GetX(), ra.GetHeight()
	return true
}

func (t *Terminal) gridSize() (rows, cols int) {
	r := t.view.GetVisibleRect()
	cols = max((r.GetWidth()-t.charW)/t.charW, 10)
	rows = max(r.GetHeight()/t.charH, 3)
	return
}

func (t *Terminal) onResize() bool {
	if t.closed.Load() || !t.measure() {
		return false
	}
	rows, cols := t.gridSize()
	if !t.started {
		t.start(rows, cols)
		return false
	}
	if rows != t.vt.rows || cols != t.vt.cols {
		t.vt.Resize(rows, cols)
		setPtySize(t.pty, rows, cols)
		t.scheduleRender()
	}
	return false
}

func (t *Terminal) start(rows, cols int) {
	t.started = true
	t.vt = NewVT(rows, cols)
	t.vt.Reply = func(b []byte) { t.pty.Write(b) }
	t.buf.SetText("")
	pty, cmd, err := startShell(t.dir, t.shell, rows, cols)
	if err != nil {
		shell := t.shell
		if len(shell) == 0 {
			shell = defaultShell()
		}
		t.buf.SetText("failed to start " + strings.Join(shell, " ") + ": " + err.Error())
		return
	}
	t.pty, t.cmd = pty, cmd
	go t.readLoop()
}

func (t *Terminal) readLoop() {
	b := make([]byte, 32*1024)
	for {
		n, err := t.pty.Read(b)
		if n > 0 {
			t.vt.Write(b[:n])
			t.scheduleRender()
		}
		if err != nil {
			break
		}
	}
	t.cmd.Wait()
	glib.IdleAdd(func() bool {
		if !t.closed.Load() && t.onExit != nil {
			t.onExit()
		}
		return false
	})
}

func (t *Terminal) scheduleRender() {
	if t.dirty.Swap(true) {
		return
	}
	glib.TimeoutAdd(16, func() bool {
		if t.vt != nil && t.vt.Holding() && !t.closed.Load() {
			return true // wait for the frame to finish; the timer repeats
		}
		t.dirty.Store(false)
		if !t.closed.Load() {
			t.render()
		}
		return false
	})
}

var xtermBase = [16]string{
	"#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
	"#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
}

func colorHex(c int, def string) string {
	switch {
	case c == colorDefault:
		return def
	case c&0x1000000 != 0:
		return fmt.Sprintf("#%06x", c&0xffffff)
	case c < 16:
		return xtermBase[c]
	case c < 232:
		c -= 16
		lv := func(x int) int {
			if x == 0 {
				return 0
			}
			return 55 + x*40
		}
		return fmt.Sprintf("#%02x%02x%02x", lv(c/36), lv(c/6%6), lv(c%6))
	default:
		g := 8 + (c-232)*10
		return fmt.Sprintf("#%02x%02x%02x", g, g, g)
	}
}

// colors resolves a's foreground and background in theme; bg is empty when
// it is the terminal's default background.
func (a attr) colors(theme *Theme) (fg, bg string) {
	f, b := a.fg, a.bg
	if a.bold && f >= 0 && f < 8 {
		f += 8
	}
	fg, bg = colorHex(f, theme.TermFG), colorHex(b, theme.TermBG)
	if a.inverse {
		fg, bg = bg, fg
	}
	if bg == theme.TermBG {
		bg = ""
	}
	return fg, bg
}

// restyle recolours existing text after a theme change.
func (t *Terminal) restyle(theme *Theme) {
	t.theme = theme
	for a, tag := range t.tags {
		fg, bg := a.colors(theme)
		tag.SetProperty("foreground", fg)
		if bg != "" {
			tag.SetProperty("background", bg)
		} else {
			tag.SetProperty("background-set", false)
		}
	}
	t.cursorTag.SetProperty("background", theme.TermFG)
	t.cursorTag.SetProperty("foreground", theme.TermBG)
}

func (t *Terminal) tagFor(a attr) *gtk.TextTag {
	if a == defaultAttr {
		return nil
	}
	if tag, ok := t.tags[a]; ok {
		return tag
	}
	fgs, bgs := a.colors(t.theme)
	props := map[string]interface{}{"foreground": fgs}
	if bgs != "" {
		props["background"] = bgs
	}
	if a.bold {
		props["weight"] = 700
	}
	if a.under {
		props["underline"] = 1
	}
	tag := t.buf.CreateTag("", props)
	t.tags[a] = tag
	return tag
}

// trimLine drops trailing blank cells from l, keeping at least minLen.
func trimLine(l []cell, minLen int) []cell {
	end := len(l)
	for end > minLen && l[end-1].ch == ' ' && l[end-1].a.bg == colorDefault && !l[end-1].a.inverse {
		end--
	}
	return l[:end]
}

// insertLine inserts one row of cells at iter.
func (t *Terminal) insertLine(iter *gtk.TextIter, l []cell) {
	var sb strings.Builder
	for i := 0; i < len(l); {
		a := l[i].a
		sb.Reset()
		j := i
		for ; j < len(l) && l[j].a == a; j++ {
			sb.WriteRune(l[j].ch)
		}
		if tag := t.tagFor(a); tag != nil {
			t.buf.InsertWithTag(iter, sb.String(), tag)
		} else {
			t.buf.Insert(iter, sb.String())
		}
		i = j
	}
}

// render brings the buffer up to date with the emulator. Only the lines that
// changed since the last render are rewritten: replacing the whole screen on
// every frame makes the text view re-measure everything, so programs that
// redraw often (spinners, TUIs) made the view jump about.
func (t *Terminal) render() {
	if t.vt == nil {
		return
	}
	s := t.vt.Snapshot()

	// The live region starts after the scrollback; it gains the lines that
	// scrolled off since the last render, followed by the screen.
	base := t.sbLines
	if s.ClearScrollback {
		base = 0
	}
	var lines [][]cell
	for _, l := range s.Scrollback {
		lines = append(lines, trimLine(l, 0))
	}
	// Before anything has scrolled off, drop the trailing blank rows below
	// the cursor to avoid a huge empty area. Once there is scrollback, keep
	// the full screen so a cleared screen (e.g. Ctrl+L) hides it.
	last := len(s.Lines) - 1
	for base+len(s.Scrollback) == 0 && last > s.CY && lineBlank(s.Lines[last]) {
		last--
	}
	for y := 0; y <= last; y++ {
		minLen := 0
		if y == s.CY {
			minLen = s.CX + 1
		}
		lines = append(lines, trimLine(s.Lines[y], minLen))
	}

	// Take the cursor off its old cell; a rewritten line loses it anyway.
	if t.cursorLine >= 0 && t.cursorLine < len(t.shown) {
		a := t.buf.GetIterAtLine(t.cursorLine)
		b := t.buf.GetIterAtLine(t.cursorLine)
		if !b.EndsLine() {
			b.ForwardToLineEnd()
		}
		t.buf.RemoveTag(t.cursorTag, a, b)
	}
	t.cursorLine = -1

	old := t.shown[base:]
	for i, l := range lines {
		n := base + i
		switch {
		case i < len(old):
			if slices.Equal(old[i], l) {
				continue
			}
			a := t.buf.GetIterAtLine(n)
			b := t.buf.GetIterAtLine(n)
			if !b.EndsLine() {
				b.ForwardToLineEnd()
			}
			t.buf.Delete(a, b)
			t.insertLine(a, l)
		default:
			iter := t.buf.GetEndIter()
			if n > 0 {
				t.buf.Insert(iter, "\n")
			}
			t.insertLine(iter, l)
		}
	}
	if len(lines) < len(old) {
		// Remove the leftover lines, along with the newline before them.
		a := t.buf.GetIterAtLine(base + len(lines) - 1)
		if !a.EndsLine() {
			a.ForwardToLineEnd()
		}
		t.buf.Delete(a, t.buf.GetEndIter())
	}
	t.shown = append(t.shown[:base], lines...)
	t.sbLines = base + len(s.Scrollback)

	if s.CursorVisible {
		t.cursorLine = t.sbLines + s.CY
		a := t.buf.GetIterAtLine(t.cursorLine)
		a.ForwardChars(s.CX)
		b := t.buf.GetIterAtOffset(a.GetOffset() + 1)
		t.buf.ApplyTag(t.cursorTag, a, b)
	}

	if t.sbLines > termScrollback+500 {
		drop := t.sbLines - termScrollback
		t.buf.Delete(t.buf.GetStartIter(), t.buf.GetIterAtLine(drop))
		t.shown = slices.Delete(t.shown, 0, drop)
		t.sbLines -= drop
		if t.cursorLine >= 0 {
			t.cursorLine -= drop
		}
	}

	if t.follow {
		t.scrollToBottom()
	}
}

func (t *Terminal) scrollToBottom() {
	adj := t.Root.GetVAdjustment()
	adj.SetValue(adj.GetUpper() - adj.GetPageSize())
}

// screenStartIter returns the start of the live screen region, which follows
// the scrollback lines in the buffer.
func (t *Terminal) screenStartIter() *gtk.TextIter {
	if t.sbLines >= t.buf.GetLineCount() {
		return t.buf.GetEndIter()
	}
	return t.buf.GetIterAtLine(t.sbLines)
}

func lineBlank(l []cell) bool {
	for _, c := range l {
		if c.ch != ' ' || c.a.bg != colorDefault {
			return false
		}
	}
	return true
}

// Send writes raw input to the shell.
func (t *Terminal) Send(s string) {
	if t.pty != nil {
		t.pty.Write([]byte(s))
	}
}

// RunCommand types a command line into the shell and executes it.
func (t *Terminal) RunCommand(cmd string) {
	if t.pty == nil {
		// Shell not started yet; retry shortly.
		glib.TimeoutAdd(100, func() bool {
			if t.pty == nil && !t.closed.Load() {
				return true
			}
			t.RunCommand(cmd)
			return false
		})
		return
	}
	t.Send(cmd + "\r")
}

func (t *Terminal) Close() {
	if t.closed.Swap(true) {
		return
	}
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Signal(os.Interrupt)
		t.cmd.Process.Kill()
	}
	if t.pty != nil {
		t.pty.Close()
	}
}

func (t *Terminal) Focus() { t.view.GrabFocus() }

func (t *Terminal) copySelection() {
	if s, e, ok := t.buf.GetSelectionBounds(); ok {
		text, _ := t.buf.GetText(s, e, false)
		clip, _ := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD)
		clip.SetText(text)
	}
}

func (t *Terminal) paste() {
	clip, _ := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD)
	text, err := clip.WaitForText()
	if err != nil || text == "" {
		return
	}
	if t.vt != nil && t.vt.BracketedPaste {
		text = "\x1b[200~" + text + "\x1b[201~"
	}
	t.Send(text)
}

func (t *Terminal) onKey(_ *gtk.TextView, ev *gdk.Event) bool {
	k := gdk.EventKeyNewFromEvent(ev)
	state := gdk.ModifierType(k.State()) & (gdk.CONTROL_MASK | gdk.SHIFT_MASK | gdk.MOD1_MASK)
	kv := k.KeyVal()
	ctrl := state&gdk.CONTROL_MASK != 0
	shift := state&gdk.SHIFT_MASK != 0
	alt := state&gdk.MOD1_MASK != 0

	// Ctrl+Shift+C/V copy and paste; on macOS so do Cmd+C/V, and other Cmd
	// combos are left to the app since Ctrl stays the terminal's own key.
	if isCommand(k.State()) {
		switch kv {
		case gdk.KEY_C, gdk.KEY_c:
			t.copySelection()
			return true
		case gdk.KEY_V, gdk.KEY_v:
			t.paste()
			return true
		}
		return false
	}
	if ctrl && shift {
		switch kv {
		case gdk.KEY_C, gdk.KEY_c:
			t.copySelection()
			return true
		case gdk.KEY_V, gdk.KEY_v:
			t.paste()
			return true
		}
		return false // leave other Ctrl+Shift combos to the app
	}
	if t.pty == nil {
		return true
	}

	app := t.vt != nil && t.vt.AppCursorKeys
	arrow := func(c string) string {
		if app {
			return "\x1bO" + c
		}
		return "\x1b[" + c
	}
	var out string
	switch kv {
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		out = "\r"
	case gdk.KEY_BackSpace:
		out = "\x7f"
		if ctrl {
			out = "\x08"
		}
	case gdk.KEY_Tab:
		out = "\t"
	case gdk.KEY_ISO_Left_Tab:
		out = "\x1b[Z"
	case gdk.KEY_Escape:
		out = "\x1b"
	case gdk.KEY_Up:
		out = arrow("A")
	case gdk.KEY_Down:
		out = arrow("B")
	case gdk.KEY_Right:
		out = arrow("C")
		if ctrl {
			out = "\x1b[1;5C"
		}
	case gdk.KEY_Left:
		out = arrow("D")
		if ctrl {
			out = "\x1b[1;5D"
		}
	case gdk.KEY_Home:
		out = arrow("H")
	case gdk.KEY_End:
		out = arrow("F")
	case gdk.KEY_Delete:
		out = "\x1b[3~"
	case gdk.KEY_Insert:
		out = "\x1b[2~"
	case gdk.KEY_Page_Up:
		if shift {
			return false // let the scrolled window page
		}
		out = "\x1b[5~"
	case gdk.KEY_Page_Down:
		if shift {
			return false
		}
		out = "\x1b[6~"
	case gdk.KEY_F1, gdk.KEY_F2, gdk.KEY_F3, gdk.KEY_F4:
		out = "\x1bO" + string(rune('P'+kv-gdk.KEY_F1))
	default:
		if kv >= gdk.KEY_F5 && kv <= gdk.KEY_F12 {
			codes := []string{"15", "17", "18", "19", "20", "21", "23", "24"}
			out = "\x1b[" + codes[kv-gdk.KEY_F5] + "~"
			break
		}
		r := gdk.KeyvalToUnicode(kv)
		if r == 0 {
			return false
		}
		if ctrl {
			switch {
			case r >= 'a' && r <= 'z':
				r = r - 'a' + 1
			case r >= 'A' && r <= 'Z':
				r = r - 'A' + 1
			case r == ' ' || r == '@' || r == '2':
				r = 0
			case r == '[':
				r = 0x1b
			case r == '\\':
				r = 0x1c
			case r == ']':
				r = 0x1d
			default:
				return false
			}
		}
		out = string(r)
	}
	if alt {
		out = "\x1b" + out
	}
	t.Send(out)
	// Jump to the live screen when typing.
	t.follow = true
	t.scrollToBottom()
	return true
}
