package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	ftName = iota
	ftPath
	ftIsDir
	ftIcon
)

// ignoredDirs are never shown or searched.
var ignoredDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, ".edmin": true}

// FileTree is the lazily loaded project explorer.
type FileTree struct {
	app   *App
	Root  *gtk.ScrolledWindow
	view  *gtk.TreeView
	store *gtk.TreeStore
}

func NewFileTree(app *App) *FileTree {
	f := &FileTree{app: app}
	f.store, _ = gtk.TreeStoreNew(glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_BOOLEAN, glib.TYPE_STRING)
	f.view, _ = gtk.TreeViewNewWithModel(f.store)
	f.view.SetHeadersVisible(false)
	f.view.SetEnableSearch(true)
	f.view.SetSearchColumn(ftName)

	col, _ := gtk.TreeViewColumnNew()
	icon, _ := gtk.CellRendererPixbufNew()
	text, _ := gtk.CellRendererTextNew()
	col.PackStart(icon, false)
	col.PackStart(text, true)
	col.AddAttribute(icon, "icon-name", ftIcon)
	col.AddAttribute(text, "text", ftName)
	f.view.AppendColumn(col)

	f.view.Connect("test-expand-row", func(_ *gtk.TreeView, iter *gtk.TreeIter, _ *gtk.TreePath) bool {
		f.loadChildren(iter)
		return false
	})
	f.view.Connect("row-activated", func(_ *gtk.TreeView, path *gtk.TreePath) {
		iter, err := f.store.GetIter(path)
		if err != nil {
			return
		}
		p, isDir := f.rowInfo(iter)
		if isDir {
			if f.view.RowExpanded(path) {
				f.view.CollapseRow(path)
			} else {
				f.view.ExpandRow(path, false)
			}
			return
		}
		f.app.editors.Open(p)
	})
	f.view.Connect("button-press-event", f.onButton)

	f.Root, _ = gtk.ScrolledWindowNew(nil, nil)
	f.Root.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	f.Root.Add(f.view)
	return f
}

func (f *FileTree) rowInfo(iter *gtk.TreeIter) (path string, isDir bool) {
	v, _ := f.store.GetValue(iter, ftPath)
	pv, _ := v.GoValue()
	d, _ := f.store.GetValue(iter, ftIsDir)
	dv, _ := d.GoValue()
	path, _ = pv.(string)
	isDir, _ = dv.(bool)
	return
}

// Reload rebuilds the tree from the project root.
func (f *FileTree) Reload() {
	f.store.Clear()
	f.fill(nil, f.app.root)
}

func (f *FileTree) fill(parent *gtk.TreeIter, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir && ignoredDirs[name] {
			continue
		}
		iter := f.store.Append(parent)
		icon := "text-x-generic"
		if isDir {
			icon = "folder"
		}
		f.store.SetValue(iter, ftName, name)
		f.store.SetValue(iter, ftPath, full)
		f.store.SetValue(iter, ftIsDir, isDir)
		f.store.SetValue(iter, ftIcon, icon)
		if isDir {
			// Placeholder so the expander arrow shows; replaced on expand.
			ph := f.store.Append(iter)
			f.store.SetValue(ph, ftName, "…")
			f.store.SetValue(ph, ftPath, "")
			f.store.SetValue(ph, ftIsDir, false)
		}
	}
}

func (f *FileTree) loadChildren(iter *gtk.TreeIter) {
	var child gtk.TreeIter
	if !f.store.IterChildren(iter, &child) {
		return
	}
	if p, _ := f.rowInfo(&child); p != "" {
		return // already loaded
	}
	f.store.Remove(&child)
	dir, _ := f.rowInfo(iter)
	f.fill(iter, dir)
}

// selectedDir returns the directory for "new file" actions: the selected
// folder, the selected file's folder, or the project root.
func (f *FileTree) selectedDir() string {
	sel, _ := f.view.GetSelection()
	_, iter, ok := sel.GetSelected()
	if !ok {
		return f.app.root
	}
	p, isDir := f.rowInfo(iter)
	if p == "" {
		return f.app.root
	}
	if isDir {
		return p
	}
	return filepath.Dir(p)
}

func (f *FileTree) onButton(_ *gtk.TreeView, ev *gdk.Event) bool {
	b := gdk.EventButtonNewFromEvent(ev)
	if b.Button() != gdk.BUTTON_SECONDARY {
		return false
	}
	if path, _, _, _, ok := f.view.GetPathAtPos(int(b.X()), int(b.Y())); ok {
		f.view.SetCursor(path, nil, false)
	}
	menu, _ := gtk.MenuNew()
	add := func(label string, fn func()) {
		it, _ := gtk.MenuItemNewWithLabel(label)
		it.Connect("activate", fn)
		menu.Append(it)
	}
	add("New File…", func() { f.create(false) })
	add("New Folder…", func() { f.create(true) })
	add("Refresh", f.Reload)
	menu.ShowAll()
	menu.PopupAtPointer(ev)
	return true
}

func (f *FileTree) create(dir bool) {
	title := "New File"
	if dir {
		title = "New Folder"
	}
	base := f.selectedDir()
	name, ok := f.app.prompt(title, "Name (relative to "+relPath(f.app.root, base)+"):", "")
	if !ok || strings.TrimSpace(name) == "" {
		return
	}
	full := filepath.Join(base, name)
	var err error
	if dir {
		err = os.MkdirAll(full, 0755)
	} else {
		if err = os.MkdirAll(filepath.Dir(full), 0755); err == nil {
			var fh *os.File
			fh, err = os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err == nil {
				fh.Close()
			}
		}
	}
	if err != nil {
		f.app.showError("Could not create "+name, err.Error())
		return
	}
	f.Reload()
	if !dir {
		f.app.editors.Open(full)
	}
}

func relPath(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}
