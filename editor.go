package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// undoOp is a single reversible buffer change.
type undoOp struct {
	insert bool
	offset int // character offset
	text   string
}

// Editor is one open file in a tab.
type Editor struct {
	Path  string
	Root  *gtk.ScrolledWindow
	View  *gtk.TextView
	Buf   *gtk.TextBuffer
	label *gtk.Label
	lang  *Language
	area  *EditorArea

	hlPending bool
	hlGen     int // incremented on every edit; stale highlight results are dropped
	undo      []undoOp
	redo      []undoOp
	replaying bool
	lastBreak bool // next insert starts a new undo group
}

// EditorArea manages the tabbed editors and the in-file search bar.
type EditorArea struct {
	app      *App
	Root     *gtk.Box
	nb       *gtk.Notebook
	editors  []*Editor
	findBar  *gtk.Box
	findEnt  *gtk.Entry
	findCase *gtk.CheckButton
	findInfo *gtk.Label
	welcome  *gtk.Label
}

// highlightStyles are the non-colour tag properties; colours come from the theme.
var highlightStyles = map[string]map[string]interface{}{
	hlKeyword: {"weight": 700},
	hlComment: {"style": 2},
}

func NewEditorArea(app *App) *EditorArea {
	a := &EditorArea{app: app}
	a.Root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	a.nb, _ = gtk.NotebookNew()
	a.nb.SetScrollable(true)
	a.nb.Connect("switch-page", func(_ *gtk.Notebook, _ *gtk.Widget, n uint) {
		glib.IdleAdd(func() bool { a.app.updateStatus(); a.app.updateTitle(); return false })
	})
	a.welcome, _ = gtk.LabelNew("Open a file from the explorer.\n\n" +
		"Ctrl+S save · Ctrl+W close tab · Ctrl+F find · Ctrl+Shift+F find in project\n" +
		"Ctrl+Click a symbol: jump to definition / find usages\n" +
		"Ctrl+1 explorer · Ctrl+2 terminal · Ctrl+3 build panel · Ctrl+4 settings\n" +
		"Ctrl+Shift+1–4 closes them")
	a.welcome.SetJustify(gtk.JUSTIFY_CENTER)
	a.welcome.SetVExpand(true)
	a.Root.PackStart(a.welcome, true, true, 0)
	a.Root.PackStart(a.nb, true, true, 0)
	a.buildFindBar()
	a.Root.PackEnd(a.findBar, false, false, 0)
	return a
}

func (a *EditorArea) updateWelcome() {
	if len(a.editors) == 0 {
		a.nb.Hide()
		a.welcome.Show()
	} else {
		a.welcome.Hide()
		a.nb.Show()
	}
}

func (a *EditorArea) Current() *Editor {
	n := a.nb.GetCurrentPage()
	if n < 0 {
		return nil
	}
	w, err := a.nb.GetNthPage(n)
	if err != nil {
		return nil
	}
	for _, e := range a.editors {
		if e.Root.Native() == w.ToWidget().Native() {
			return e
		}
	}
	return nil
}

func (a *EditorArea) find(path string) *Editor {
	for _, e := range a.editors {
		if e.Path == path {
			return e
		}
	}
	return nil
}

// Open opens path in a tab (or focuses its existing tab).
func (a *EditorArea) Open(path string) *Editor {
	path, _ = filepath.Abs(path)
	if e := a.find(path); e != nil {
		a.nb.SetCurrentPage(a.nb.PageNum(e.Root))
		return e
	}
	data, err := os.ReadFile(path)
	if err != nil {
		a.app.showError("Cannot open file", err.Error())
		return nil
	}
	if !utf8.Valid(data) {
		a.app.showError("Cannot open file", path+" does not look like a UTF-8 text file.")
		return nil
	}
	e := &Editor{Path: path, area: a, lang: languageFor(path), lastBreak: true}
	e.Root, _ = gtk.ScrolledWindowNew(nil, nil)
	e.View, _ = gtk.TextViewNew()
	e.View.SetMonospace(true)
	e.View.SetLeftMargin(6)
	e.View.SetWrapMode(gtk.WRAP_NONE)
	e.Buf, _ = e.View.GetBuffer()
	e.Root.Add(e.View)
	for name := range a.app.theme.Syntax {
		props := map[string]interface{}{}
		for k, v := range highlightStyles[name] {
			props[k] = v
		}
		e.Buf.CreateTag(name, props)
	}
	e.Buf.CreateTag("search-match", nil)
	e.Buf.CreateTag("jump-line", nil)
	e.styleTags()
	e.Buf.SetText(string(data))
	e.Buf.PlaceCursor(e.Buf.GetStartIter())
	e.Buf.SetModified(false)
	e.connect()
	NewGutter(e)

	tab, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	e.label, _ = gtk.LabelNew(filepath.Base(path))
	e.label.SetTooltipText(path)
	closeBtn, _ := gtk.ButtonNewFromIconName("window-close-symbolic", gtk.ICON_SIZE_MENU)
	closeBtn.SetRelief(gtk.RELIEF_NONE)
	closeBtn.SetFocusOnClick(false)
	closeBtn.Connect("clicked", func() { a.Close(e) })
	tab.PackStart(e.label, false, false, 0)
	tab.PackStart(closeBtn, false, false, 0)
	tab.ShowAll()

	a.editors = append(a.editors, e)
	e.Root.ShowAll()
	n := a.nb.AppendPage(e.Root, tab)
	a.nb.SetTabReorderable(e.Root, true)
	a.updateWelcome()
	a.nb.SetCurrentPage(n)
	e.scheduleHighlight()
	e.View.GrabFocus()
	return e
}

func (a *EditorArea) Close(e *Editor) {
	if e.Buf.GetModified() {
		switch a.app.askSave(filepath.Base(e.Path)) {
		case gtk.RESPONSE_YES:
			if !e.Save() {
				return
			}
		case gtk.RESPONSE_NO:
		default:
			return
		}
	}
	a.nb.RemovePage(a.nb.PageNum(e.Root))
	for i, x := range a.editors {
		if x == e {
			a.editors = append(a.editors[:i], a.editors[i+1:]...)
			break
		}
	}
	a.updateWelcome()
}

// Unsaved returns editors with unsaved changes.
func (a *EditorArea) Unsaved() []*Editor {
	var out []*Editor
	for _, e := range a.editors {
		if e.Buf.GetModified() {
			out = append(out, e)
		}
	}
	return out
}

// styleTags colours the editor's tags from its window's theme.
func (e *Editor) styleTags() {
	theme := e.area.app.theme
	tt, _ := e.Buf.GetTagTable()
	set := func(name string, props map[string]string) {
		if tag, err := tt.Lookup(name); err == nil && tag != nil {
			for k, v := range props {
				tag.SetProperty(k, v)
			}
		}
	}
	for name, fg := range theme.Syntax {
		set(name, map[string]string{"foreground": fg})
	}
	set("search-match", map[string]string{"background": theme.MatchBG, "foreground": theme.MatchFG})
	set("jump-line", map[string]string{"paragraph-background": theme.JumpLine})
}

func (e *Editor) Text() string {
	s, _ := e.Buf.GetText(e.Buf.GetStartIter(), e.Buf.GetEndIter(), true)
	return s
}

func (e *Editor) Save() bool {
	if err := os.WriteFile(e.Path, []byte(e.Text()), 0644); err != nil {
		e.area.app.showError("Save failed", err.Error())
		return false
	}
	e.Buf.SetModified(false)
	e.lastBreak = true
	return true
}

func (e *Editor) updateLabel() {
	name := filepath.Base(e.Path)
	if e.Buf.GetModified() {
		name = "● " + name
	}
	e.label.SetText(name)
}

func (e *Editor) connect() {
	e.Buf.Connect("modified-changed", func() { e.updateLabel(); e.area.app.updateTitle() })
	e.Buf.Connect("changed", func() { e.scheduleHighlight() })
	e.Buf.Connect("mark-set", func(_ *gtk.TextBuffer, _ *gtk.TextIter, m *gtk.TextMark) {
		if m.Native() == e.Buf.GetInsert().Native() {
			e.area.app.updateStatus()
		}
	})
	e.Buf.Connect("insert-text", func(_ *gtk.TextBuffer, iter *gtk.TextIter, text string) {
		e.record(undoOp{insert: true, offset: iter.GetOffset(), text: text})
	})
	e.Buf.Connect("delete-range", func(_ *gtk.TextBuffer, s, en *gtk.TextIter) {
		e.record(undoOp{insert: false, offset: s.GetOffset(), text: s.GetText(en)})
	})
	e.View.Connect("key-press-event", e.onKey)
	e.View.Connect("button-press-event", e.onClick)
}

// record appends to the undo history, merging runs of typed characters.
func (e *Editor) record(op undoOp) {
	if e.replaying {
		return
	}
	e.redo = nil
	if n := len(e.undo); n > 0 && !e.lastBreak {
		last := &e.undo[n-1]
		wordish := !strings.ContainsAny(op.text, " \n\t")
		if op.insert && last.insert && wordish && utf8.RuneCountInString(op.text) == 1 &&
			last.offset+utf8.RuneCountInString(last.text) == op.offset {
			last.text += op.text
			return
		}
		if !op.insert && !last.insert && utf8.RuneCountInString(op.text) == 1 && op.offset+1 == last.offset && wordish {
			last.text = op.text + last.text
			last.offset = op.offset
			return
		}
	}
	e.undo = append(e.undo, op)
	e.lastBreak = utf8.RuneCountInString(op.text) != 1
}

func (e *Editor) apply(op undoOp, reverse bool) {
	e.replaying = true
	defer func() { e.replaying = false }()
	insert := op.insert != reverse
	start := e.Buf.GetIterAtOffset(op.offset)
	if insert {
		e.Buf.Insert(start, op.text)
		e.Buf.PlaceCursor(e.Buf.GetIterAtOffset(op.offset + utf8.RuneCountInString(op.text)))
	} else {
		end := e.Buf.GetIterAtOffset(op.offset + utf8.RuneCountInString(op.text))
		e.Buf.Delete(start, end)
		e.Buf.PlaceCursor(e.Buf.GetIterAtOffset(op.offset))
	}
	e.View.ScrollToMark(e.Buf.GetInsert(), 0.1, false, 0, 0)
}

func (e *Editor) Undo() {
	if n := len(e.undo); n > 0 {
		op := e.undo[n-1]
		e.undo = e.undo[:n-1]
		e.apply(op, true)
		e.redo = append(e.redo, op)
		e.lastBreak = true
	}
}

func (e *Editor) Redo() {
	if n := len(e.redo); n > 0 {
		op := e.redo[n-1]
		e.redo = e.redo[:n-1]
		e.apply(op, false)
		e.undo = append(e.undo, op)
		e.lastBreak = true
	}
}

func (e *Editor) onKey(_ *gtk.TextView, ev *gdk.Event) bool {
	k := gdk.EventKeyNewFromEvent(ev)
	mods := shortcutMods(k.State())
	switch k.KeyVal() {
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		if mods != 0 {
			return false
		}
		// Auto-indent: carry the current line's leading whitespace.
		ins := e.Buf.GetIterAtMark(e.Buf.GetInsert())
		ls := e.Buf.GetIterAtLine(ins.GetLine())
		line := ls.GetText(ins)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if s, en, ok := e.Buf.GetSelectionBounds(); ok {
			e.Buf.Delete(s, en)
		}
		e.lastBreak = true
		e.Buf.InsertAtCursor("\n" + indent)
		e.View.ScrollToMark(e.Buf.GetInsert(), 0, false, 0, 0)
		return true
	case gdk.KEY_Escape:
		e.clearJumpLine()
		return false
	}
	return false
}

func (e *Editor) onClick(_ *gtk.TextView, ev *gdk.Event) bool {
	b := gdk.EventButtonNewFromEvent(ev)
	e.lastBreak = true
	e.clearJumpLine()
	if b.Button() != gdk.BUTTON_PRIMARY || b.Type() != gdk.EVENT_BUTTON_PRESS ||
		shortcutMods(b.State())&gdk.CONTROL_MASK == 0 {
		return false
	}
	// The event's X/Y are relative to whichever window received it (the
	// text area, not the widget, which also holds the gutter), so work from
	// root coordinates relative to the text window.
	ox, oy := e.View.GetWindow(gtk.TEXT_WINDOW_TEXT).GetOrigin()
	wx, wy := int(b.XRoot())-ox, int(b.YRoot())-oy
	if wx < 0 || wy < 0 {
		return false // Ctrl+click in the gutter
	}
	bx, by := e.View.WindowToBufferCoords(gtk.TEXT_WINDOW_TEXT, wx, wy)
	iter := e.View.GetIterAtLocation(bx, by)
	e.Buf.PlaceCursor(iter)
	e.area.app.symbolAction(e, iter.GetLine(), iter.GetLineIndex())
	return true
}

func (e *Editor) scheduleHighlight() {
	e.hlGen++
	if e.lang == nil || e.hlPending {
		return
	}
	e.hlPending = true
	glib.TimeoutAdd(120, func() bool {
		e.hlPending = false
		e.highlight()
		return false
	})
}

func (e *Editor) highlight() {
	if e.lang == nil || e.Buf.GetCharCount() > 2_000_000 {
		return
	}
	// Parse and query off the UI thread (the first use of a grammar also
	// compiles its queries), then apply the tags if nothing changed since.
	src := []byte(e.Text())
	gen, lang := e.hlGen, e.lang
	go func() {
		spans := lang.Highlight(src)
		glib.IdleAdd(func() bool {
			if gen == e.hlGen {
				e.applyHighlights(spans)
			}
			return false
		})
	}()
}

func (e *Editor) applyHighlights(spans []Span) {
	start, end := e.Buf.GetBounds()
	for name := range e.area.app.theme.Syntax {
		e.Buf.RemoveTagByName(name, start, end)
	}
	nlines := e.Buf.GetLineCount()
	iterAt := func(row, col int) *gtk.TextIter {
		if row >= nlines {
			return e.Buf.GetEndIter()
		}
		it := e.Buf.GetIterAtLine(row)
		if col > 0 {
			if bl := it.GetBytesInLine(); col > bl {
				col = bl
			}
			it = e.Buf.GetIterAtLineIndex(row, col)
		}
		return it
	}
	for _, s := range spans {
		e.Buf.ApplyTagByName(s.Class, iterAt(s.StartRow, s.StartCol), iterAt(s.EndRow, s.EndCol))
	}
}

// GotoLine moves the cursor to a 0-based line and byte column and highlights it.
func (e *Editor) GotoLine(line, colByte int) {
	if line >= e.Buf.GetLineCount() {
		line = e.Buf.GetLineCount() - 1
	}
	e.clearJumpLine()
	le := e.Buf.GetIterAtLine(line)
	le.ForwardLine()
	e.Buf.ApplyTagByName("jump-line", e.Buf.GetIterAtLine(line), le)
	it := e.Buf.GetIterAtLine(line)
	if colByte > 0 && colByte <= it.GetBytesInLine() {
		it = e.Buf.GetIterAtLineIndex(line, colByte)
	}
	e.Buf.PlaceCursor(it)
	e.scrollToLine(line, 50)
	e.View.GrabFocus()
}

// scrollToLine positions line ~30% from the top of the view. A freshly
// opened tab has no size or layout yet, so it retries until both are ready.
func (e *Editor) scrollToLine(line, tries int) {
	glib.TimeoutAdd(20, func() bool {
		if e.View.GetAllocatedHeight() <= 1 || !e.View.GetMapped() {
			if tries > 0 {
				e.scrollToLine(line, tries-1)
			}
			return false
		}
		cur := e.Buf.GetIterAtMark(e.Buf.GetInsert())
		y, h := e.View.GetLineYrange(e.Buf.GetIterAtLine(line))
		vadj := e.Root.GetVAdjustment()
		if (h <= 0 || vadj.GetUpper() < float64(y+h)) && tries > 0 {
			// Text layout is still being validated; wait for it to reach the line.
			e.scrollToLine(line, tries-1)
			return false
		}
		vadj.SetValue(max(0, min(float64(y)-vadj.GetPageSize()*0.3, vadj.GetUpper()-vadj.GetPageSize())))
		x := float64(e.View.GetIterLocation(cur).GetX())
		hadj := e.Root.GetHAdjustment()
		if x < hadj.GetPageSize()*0.8 {
			hadj.SetValue(0)
		} else {
			hadj.SetValue(x - hadj.GetPageSize()/2)
		}
		return false
	})
}

func (e *Editor) clearJumpLine() {
	s, en := e.Buf.GetBounds()
	e.Buf.RemoveTagByName("jump-line", s, en)
}

// ---- In-file search (Ctrl+F) ----

func (a *EditorArea) buildFindBar() {
	a.findBar, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	a.findBar.SetMarginStart(4)
	a.findBar.SetMarginEnd(4)
	a.findBar.SetMarginTop(2)
	a.findBar.SetMarginBottom(2)
	lbl, _ := gtk.LabelNew("Find:")
	a.findEnt, _ = gtk.EntryNew()
	a.findEnt.SetWidthChars(30)
	a.findCase, _ = gtk.CheckButtonNewWithLabel("Match case")
	prev, _ := gtk.ButtonNewFromIconName("go-up-symbolic", gtk.ICON_SIZE_BUTTON)
	next, _ := gtk.ButtonNewFromIconName("go-down-symbolic", gtk.ICON_SIZE_BUTTON)
	closeBtn, _ := gtk.ButtonNewFromIconName("window-close-symbolic", gtk.ICON_SIZE_BUTTON)
	closeBtn.SetRelief(gtk.RELIEF_NONE)
	a.findInfo, _ = gtk.LabelNew("")
	a.findBar.PackStart(lbl, false, false, 0)
	a.findBar.PackStart(a.findEnt, false, false, 0)
	a.findBar.PackStart(prev, false, false, 0)
	a.findBar.PackStart(next, false, false, 0)
	a.findBar.PackStart(a.findCase, false, false, 4)
	a.findBar.PackStart(a.findInfo, false, false, 4)
	a.findBar.PackEnd(closeBtn, false, false, 0)

	a.findEnt.Connect("changed", func() { a.searchCurrent(true, true) })
	a.findEnt.Connect("activate", func() { a.searchCurrent(true, false) })
	a.findCase.Connect("toggled", func() { a.searchCurrent(true, true) })
	next.Connect("clicked", func() { a.searchCurrent(true, false) })
	prev.Connect("clicked", func() { a.searchCurrent(false, false) })
	closeBtn.Connect("clicked", a.HideFind)
	a.findEnt.Connect("key-press-event", func(_ *gtk.Entry, ev *gdk.Event) bool {
		k := gdk.EventKeyNewFromEvent(ev)
		switch k.KeyVal() {
		case gdk.KEY_Escape:
			a.HideFind()
			return true
		case gdk.KEY_Return, gdk.KEY_KP_Enter:
			if gdk.ModifierType(k.State())&gdk.SHIFT_MASK != 0 {
				a.searchCurrent(false, false)
				return true
			}
		}
		return false
	})
}

func (a *EditorArea) ShowFind() {
	e := a.Current()
	if e != nil {
		if s, en, ok := e.Buf.GetSelectionBounds(); ok && s.GetLine() == en.GetLine() {
			a.findEnt.SetText(s.GetText(en))
		}
	}
	a.findBar.ShowAll()
	a.findEnt.GrabFocus()
	a.searchCurrent(true, true)
}

func (a *EditorArea) HideFind() {
	a.findBar.Hide()
	for _, e := range a.editors {
		s, en := e.Buf.GetBounds()
		e.Buf.RemoveTagByName("search-match", s, en)
	}
	if e := a.Current(); e != nil {
		e.View.GrabFocus()
	}
}

// searchCurrent highlights every match and selects the next/previous one.
// If fromSelStart is set, the search restarts at the current selection start
// (used while typing so the match under the cursor stays selected).
func (a *EditorArea) searchCurrent(forward, fromSelStart bool) {
	e := a.Current()
	if e == nil {
		return
	}
	needle, _ := a.findEnt.GetText()
	s, en := e.Buf.GetBounds()
	e.Buf.RemoveTagByName("search-match", s, en)
	if needle == "" {
		a.findInfo.SetText("")
		return
	}
	flags := gtk.TEXT_SEARCH_TEXT_ONLY
	if !a.findCase.GetActive() {
		flags |= gtk.TEXT_SEARCH_CASE_INSENSITIVE
	}
	count := 0
	it := e.Buf.GetStartIter()
	for {
		ms, me, ok := it.ForwardSearch(needle, flags, nil)
		if !ok {
			break
		}
		e.Buf.ApplyTagByName("search-match", ms, me)
		count++
		it = me
	}
	if count == 0 {
		a.findInfo.SetText("No matches")
		return
	}
	a.findInfo.SetText(pluralize(count, "match", "matches"))

	var ms, me *gtk.TextIter
	var ok bool
	selS, selE, hasSel := e.Buf.GetSelectionBounds()
	if !hasSel {
		selS = e.Buf.GetIterAtMark(e.Buf.GetInsert())
		selE = selS
	}
	if forward {
		from := selE
		if fromSelStart {
			from = selS
		}
		ms, me, ok = from.ForwardSearch(needle, flags, nil)
		if !ok {
			ms, me, ok = e.Buf.GetStartIter().ForwardSearch(needle, flags, nil)
		}
	} else {
		ms, me, ok = selS.BackwardSearch(needle, flags, nil)
		if !ok {
			ms, me, ok = e.Buf.GetEndIter().BackwardSearch(needle, flags, nil)
		}
	}
	if ok {
		e.Buf.SelectRange(ms, me)
		e.View.ScrollToMark(e.Buf.GetInsert(), 0.1, false, 0, 0)
	}
}
