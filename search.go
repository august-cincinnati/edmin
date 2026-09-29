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

// SearchPanel hosts project-wide search (Ctrl+Shift+F) and symbol results.
type SearchPanel struct {
	app    *App
	Root   *gtk.Box
	entry  *gtk.SearchEntry
	cs     *gtk.CheckButton
	status *gtk.Label
	view   *gtk.TreeView
	store  *gtk.TreeStore
	gen    atomic.Int64

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

func NewSearchPanel(app *App) *SearchPanel {
	s := &SearchPanel{app: app}
	s.Root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	s.Root.SetMarginTop(4)
	s.entry, _ = gtk.SearchEntryNew()
	s.entry.SetPlaceholderText("Search in project (Enter)")
	s.cs, _ = gtk.CheckButtonNewWithLabel("Match case")
	s.status, _ = gtk.LabelNew("")
	s.status.SetXAlign(0)
	s.status.SetEllipsize(3) // PANGO_ELLIPSIZE_END
	s.status.SetMarginStart(4)

	s.store, _ = gtk.TreeStoreNew(glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_INT, glib.TYPE_INT)
	s.view, _ = gtk.TreeViewNewWithModel(s.store)
	s.view.SetHeadersVisible(false)
	s.view.SetTooltipColumn(srPath)
	r, _ := gtk.CellRendererTextNew()
	r.SetProperty("ellipsize", 3)
	col, _ := gtk.TreeViewColumnNewWithAttribute("", r, "markup", srMarkup)
	s.view.AppendColumn(col)
	s.view.Connect("row-activated", func(_ *gtk.TreeView, path *gtk.TreePath) {
		iter, err := s.store.GetIter(path)
		if err != nil {
			return
		}
		p := getString(s.store.ToTreeModel(), iter, srPath)
		line := getInt(s.store.ToTreeModel(), iter, srLine)
		colb := getInt(s.store.ToTreeModel(), iter, srCol)
		if line < 0 {
			if s.view.RowExpanded(path) {
				s.view.CollapseRow(path)
			} else {
				s.view.ExpandRow(path, false)
			}
			return
		}
		if e := s.app.editors.Open(p); e != nil {
			e.GotoLine(line, colb)
		}
	})
	sw, _ := gtk.ScrolledWindowNew(nil, nil)
	sw.Add(s.view)

	s.entry.Connect("activate", s.run)
	s.cs.Connect("toggled", func() {
		if t, _ := s.entry.GetText(); t != "" {
			s.run()
		}
	})
	top, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	top.SetMarginStart(4)
	top.SetMarginEnd(4)
	top.PackStart(s.entry, false, false, 0)
	top.PackStart(s.cs, false, false, 0)
	s.Root.PackStart(top, false, false, 0)
	s.Root.PackStart(s.status, false, false, 0)
	s.Root.PackStart(sw, true, true, 0)
	return s
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

func (s *SearchPanel) Focus(text string) {
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

func (s *SearchPanel) run() {
	needle, _ := s.entry.GetText()
	if needle == "" {
		return
	}
	matchCase := s.cs.GetActive()
	gen := s.gen.Add(1)
	root := s.app.root
	open := s.app.openBuffers()
	s.status.SetText("Searching…")
	s.store.Clear()

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
			s.show(msg, results, needle)
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

// show fills the results tree grouped by file.
func (s *SearchPanel) show(status string, results []match, needle string) {
	s.lastStatus, s.lastResults, s.lastNeedle = status, results, needle
	s.store.Clear()
	s.status.SetText(status)
	// Symbol results list their definitions first, in a group of their own.
	var defs []match
	for _, m := range results {
		if m.isDef {
			defs = append(defs, m)
		}
	}
	if len(defs) > 0 {
		di := s.store.Append(nil)
		s.store.SetValue(di, srMarkup, fmt.Sprintf("<b>Definitions</b> <small>(%d)</small>", len(defs)))
		s.store.SetValue(di, srPath, "")
		s.store.SetValue(di, srLine, -1)
		s.store.SetValue(di, srCol, 0)
		for _, m := range defs {
			ci := s.store.Append(di)
			s.store.SetValue(ci, srMarkup, "<small>"+html.EscapeString(relPath(s.app.root, m.path))+"</small> "+matchMarkup(s.app.theme, m, needle))
			s.store.SetValue(ci, srPath, m.path)
			s.store.SetValue(ci, srLine, m.line)
			s.store.SetValue(ci, srCol, m.colByte)
		}
	}
	byFile := map[string][]match{}
	var files []string
	for _, m := range results {
		if m.isDef {
			continue
		}
		if _, ok := byFile[m.path]; !ok {
			files = append(files, m.path)
		}
		byFile[m.path] = append(byFile[m.path], m)
	}
	sort.Strings(files)
	for _, f := range files {
		ms := byFile[f]
		fi := s.store.Append(nil)
		s.store.SetValue(fi, srMarkup, fmt.Sprintf("<b>%s</b> <small>(%d)</small>", html.EscapeString(relPath(s.app.root, f)), len(ms)))
		s.store.SetValue(fi, srPath, f)
		s.store.SetValue(fi, srLine, -1)
		s.store.SetValue(fi, srCol, 0)
		for _, m := range ms {
			ci := s.store.Append(fi)
			s.store.SetValue(ci, srMarkup, matchMarkup(s.app.theme, m, needle))
			s.store.SetValue(ci, srPath, m.path)
			s.store.SetValue(ci, srLine, m.line)
			s.store.SetValue(ci, srCol, m.colByte)
		}
	}
	if len(files) <= 30 {
		s.view.ExpandAll()
	}
}

// restyle redraws the current results in the window's theme colours.
func (s *SearchPanel) restyle() {
	if s.lastResults != nil {
		s.show(s.lastStatus, s.lastResults, s.lastNeedle)
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
