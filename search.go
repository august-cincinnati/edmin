package main

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	srMarkup = iota
	srPath
	srLine
	srCol
)

const maxSearchResults = 5000

// SearchDialog hosts project-wide search (Ctrl+Shift+F) and symbol results
// in a window of its own, with definitions and usages on separate tabs.
type SearchDialog struct {
	app    *App
	win    *gtk.Window
	entry  *gtk.SearchEntry
	cs     *gtk.CheckButton
	status *gtk.Label
	nb     *gtk.Notebook

	defView, useView   *gtk.TreeView
	defStore, useStore *gtk.TreeStore
	defLbl, useLbl     *gtk.Label
	gen                atomic.Int64

	// The results on display, re-rendered when the theme changes.
	lastStatus, lastNeedle string
	lastResults            []match
}

type match struct {
	path    string
	line    int // 0-based
	colByte int
	text    string
	isDef   bool
}

const (
	tabDefs = iota
	tabUsages
)

func NewSearchDialog(app *App) *SearchDialog {
	s := &SearchDialog{app: app}
	s.win, _ = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	s.win.SetTitle("Search")
	s.win.SetTransientFor(app.win)
	s.win.SetDestroyWithParent(true)
	s.win.SetTypeHint(gdk.WINDOW_TYPE_HINT_DIALOG)
	s.win.SetDefaultSize(720, 520)
	s.win.Connect("delete-event", func() bool { s.win.Hide(); return true })
	s.win.Connect("key-press-event", func(_ *gtk.Window, ev *gdk.Event) bool {
		if gdk.EventKeyNewFromEvent(ev).KeyVal() == gdk.KEY_Escape {
			s.win.Hide()
			return true
		}
		return false
	})

	s.entry, _ = gtk.SearchEntryNew()
	s.entry.SetPlaceholderText("Search in project (Enter)")
	s.entry.SetHExpand(true)
	s.cs, _ = gtk.CheckButtonNewWithLabel("Match case")
	s.status, _ = gtk.LabelNew("")
	s.status.SetXAlign(0)
	s.status.SetEllipsize(3) // PANGO_ELLIPSIZE_END

	s.defStore, s.defView = s.newResultView()
	s.useStore, s.useView = s.newResultView()
	s.defLbl, _ = gtk.LabelNew("Definition")
	s.useLbl, _ = gtk.LabelNew("Usages")
	s.nb, _ = gtk.NotebookNew()
	s.nb.AppendPage(scrolled(s.defView), s.defLbl)
	s.nb.AppendPage(scrolled(s.useView), s.useLbl)

	s.entry.Connect("activate", s.run)
	s.cs.Connect("toggled", func() {
		if t, _ := s.entry.GetText(); t != "" {
			s.run()
		}
	})
	top, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	top.PackStart(s.entry, true, true, 0)
	top.PackStart(s.cs, false, false, 0)

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(8)
	box.SetMarginEnd(8)
	box.PackStart(top, false, false, 0)
	box.PackStart(s.status, false, false, 0)
	box.PackStart(s.nb, true, true, 0)
	s.win.Add(box)
	box.ShowAll()
	return s
}

func scrolled(w gtk.IWidget) *gtk.ScrolledWindow {
	sw, _ := gtk.ScrolledWindowNew(nil, nil)
	sw.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	sw.Add(w)
	return sw
}

// newResultView makes a results tree; activating a match opens it in the
// editor, and activating a group row toggles it.
func (s *SearchDialog) newResultView() (*gtk.TreeStore, *gtk.TreeView) {
	store, _ := gtk.TreeStoreNew(glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_INT, glib.TYPE_INT)
	view, _ := gtk.TreeViewNewWithModel(store)
	view.SetHeadersVisible(false)
	view.SetTooltipColumn(srPath)
	r, _ := gtk.CellRendererTextNew()
	r.SetProperty("ellipsize", 3)
	col, _ := gtk.TreeViewColumnNewWithAttribute("", r, "markup", srMarkup)
	view.AppendColumn(col)
	view.Connect("row-activated", func(_ *gtk.TreeView, path *gtk.TreePath) {
		iter, err := store.GetIter(path)
		if err != nil {
			return
		}
		m := store.ToTreeModel()
		p := getString(m, iter, srPath)
		line := getInt(m, iter, srLine)
		colb := getInt(m, iter, srCol)
		if line < 0 {
			if view.RowExpanded(path) {
				view.CollapseRow(path)
			} else {
				view.ExpandRow(path, false)
			}
			return
		}
		if e := s.app.editors.Open(p); e != nil {
			e.GotoLine(line, colb)
			s.app.win.Present()
		}
	})
	return store, view
}

// present shows the dialog in the window's current theme.
func (s *SearchDialog) present() {
	s.app.themed(s.win)
	s.win.Present()
}

func getString(m *gtk.TreeModel, iter *gtk.TreeIter, col int) string {
	v, err := m.GetValue(iter, col)
	if err != nil {
		return ""
	}
	g, _ := v.GoValue()
	s, _ := g.(string)
	return s
}

func getInt(m *gtk.TreeModel, iter *gtk.TreeIter, col int) int {
	v, err := m.GetValue(iter, col)
	if err != nil {
		return 0
	}
	g, _ := v.GoValue()
	i, _ := g.(int)
	return i
}

// Focus opens the dialog for a text search, optionally running one for text.
func (s *SearchDialog) Focus(text string) {
	s.present()
	s.nb.SetCurrentPage(tabUsages)
	if text != "" {
		s.entry.SetText(text)
	}
	s.entry.GrabFocus()
	if text != "" {
		s.run()
	}
}

// walkProject calls fn for each regular, non-ignored file in the project.
func walkProject(root string, fn func(path string)) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (ignoredDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			fn(p)
		}
		return nil
	})
}

// readTextFile returns file contents, or nil for large or binary files.
func readTextFile(p string) []byte {
	st, err := os.Stat(p)
	if err != nil || st.Size() > 4<<20 {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil
	}
	return data
}

func (s *SearchDialog) run() {
	needle, _ := s.entry.GetText()
	if needle == "" {
		return
	}
	matchCase := s.cs.GetActive()
	gen := s.gen.Add(1)
	root := s.app.root
	open := s.app.openBuffers()
	s.status.SetText("Searching…")
	s.defStore.Clear()
	s.useStore.Clear()

	go func() {
		var results []match
		lowNeedle := strings.ToLower(needle)
		walkProject(root, func(p string) {
			if s.gen.Load() != gen || len(results) >= maxSearchResults {
				return
			}
			var data []byte
			if txt, ok := open[p]; ok {
				data = []byte(txt)
			} else {
				data = readTextFile(p)
			}
			if data == nil {
				return
			}
			for i, line := range strings.Split(string(data), "\n") {
				hay := line
				n := needle
				if !matchCase {
					hay, n = strings.ToLower(line), lowNeedle
				}
				if idx := strings.Index(hay, n); idx >= 0 {
					results = append(results, match{path: p, line: i, colByte: idx, text: line})
				}
			}
		})
		glib.IdleAdd(func() bool {
			if s.gen.Load() != gen {
				return false
			}
			msg := fmt.Sprintf("%s for “%s”", pluralize(len(results), "result", "results"), needle)
			if len(results) >= maxSearchResults {
				msg += " (truncated)"
			}
			s.show(msg, results, needle, false)
			return false
		})
	}()
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// show fills the Definition tab with the definitions among results and the
// Usages tab with the rest, grouped by file. symbol selects the tab to show:
// symbol lookups open on their definitions when there are any.
func (s *SearchDialog) show(status string, results []match, needle string, symbol bool) {
	s.lastStatus, s.lastResults, s.lastNeedle = status, results, needle
	s.defStore.Clear()
	s.useStore.Clear()
	s.status.SetText(status)

	addRow := func(store *gtk.TreeStore, parent *gtk.TreeIter, markup, path string, line, col int) *gtk.TreeIter {
		it := store.Append(parent)
		store.SetValue(it, srMarkup, markup)
		store.SetValue(it, srPath, path)
		store.SetValue(it, srLine, line)
		store.SetValue(it, srCol, col)
		return it
	}

	var defs []match
	byFile := map[string][]match{}
	var files []string
	for _, m := range results {
		if m.isDef {
			defs = append(defs, m)
			continue
		}
		if _, ok := byFile[m.path]; !ok {
			files = append(files, m.path)
		}
		byFile[m.path] = append(byFile[m.path], m)
	}

	for _, m := range defs {
		addRow(s.defStore, nil, "<small>"+html.EscapeString(relPath(s.app.root, m.path))+"</small> "+matchMarkup(s.app.theme, m, needle),
			m.path, m.line, m.colByte)
	}
	if len(defs) == 0 {
		addRow(s.defStore, nil, fmt.Sprintf("<i><span foreground=\"%s\">No definitions found</span></i>", s.app.theme.Dim), "", -1, 0)
	}

	sort.Strings(files)
	for _, f := range files {
		ms := byFile[f]
		fi := addRow(s.useStore, nil, fmt.Sprintf("<b>%s</b> <small>(%d)</small>", html.EscapeString(relPath(s.app.root, f)), len(ms)), f, -1, 0)
		for _, m := range ms {
			addRow(s.useStore, fi, matchMarkup(s.app.theme, m, needle), m.path, m.line, m.colByte)
		}
	}
	if len(files) <= 30 {
		s.useView.ExpandAll()
	}

	s.defLbl.SetText(fmt.Sprintf("Definition (%d)", len(defs)))
	s.useLbl.SetText(fmt.Sprintf("Usages (%d)", len(results)-len(defs)))
	if symbol && len(defs) > 0 {
		s.nb.SetCurrentPage(tabDefs)
	} else {
		s.nb.SetCurrentPage(tabUsages)
	}
}

// restyle redraws the dialog and its results in the window's theme colours.
func (s *SearchDialog) restyle() {
	s.app.themed(s.win)
	if s.lastResults != nil {
		s.show(s.lastStatus, s.lastResults, s.lastNeedle, s.nb.GetCurrentPage() == tabDefs)
	}
}

func matchMarkup(theme *Theme, m match, needle string) string {
	text := strings.TrimLeft(m.text, " \t")
	trimmed := len(m.text) - len(text)
	if len(text) > 200 {
		cut := 200
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	prefix := fmt.Sprintf("<span foreground=\"%s\">%d:</span> ", theme.Dim, m.line+1)
	if m.isDef {
		prefix += fmt.Sprintf("<span foreground=\"%s\"><b>def</b></span> ", theme.DefMarker)
	}
	col := m.colByte - trimmed
	if col >= 0 && col+len(needle) <= len(text) {
		return prefix + html.EscapeString(text[:col]) + fmt.Sprintf("<b><span background=\"%s\" foreground=\"%s\">", theme.MatchBG, theme.MatchFG) +
			html.EscapeString(text[col:col+len(needle)]) + "</span></b>" + html.EscapeString(text[col+len(needle):])
	}
	return prefix + html.EscapeString(text)
}
