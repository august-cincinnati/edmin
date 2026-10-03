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
	Root  *gtk.Box
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
	f.view.Connect("key-press-event", func(_ *gtk.TreeView, ev *gdk.Event) bool {
		k := gdk.EventKeyNewFromEvent(ev)
		kv := k.KeyVal()
		if shortcutMods(k.State()) != 0 {
			return false
		}
		switch kv {
		case gdk.KEY_Delete, gdk.KEY_KP_Delete:
			f.deleteSelected()
			return true
		case gdk.KEY_Right, gdk.KEY_KP_Right:
			return f.arrowRight()
		case gdk.KEY_Left, gdk.KEY_KP_Left:
			return f.arrowLeft()
		}
		return false
	})

	sw, _ := gtk.ScrolledWindowNew(nil, nil)
	sw.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	sw.Add(f.view)

	f.Root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	f.Root.PackStart(sw, true, true, 0)
	return f
}

// Toolbar returns the explorer's buttons, for the window's header bar.
func (f *FileTree) Toolbar() *gtk.Box {
	bar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	mk := func(icon, tip string, fn func()) {
		bt, _ := gtk.ButtonNewFromIconName(icon, gtk.ICON_SIZE_BUTTON)
		bt.SetTooltipText(tip)
		bt.Connect("clicked", fn)
		bar.PackStart(bt, false, false, 0)
	}
	mk("document-new-symbolic", "New file (Ctrl+N)", func() { f.app.showExplorer(); f.create(false) })
	mk("folder-new-symbolic", "New folder (Ctrl+Shift+N)", func() { f.app.showExplorer(); f.create(true) })
	mk("view-refresh-symbolic", "Refresh file tree", f.Reload)
	mk("find-location-symbolic", "Jump to open file (Ctrl+Shift+E)", f.RevealCurrent)
	mk("pan-up-symbolic", "Collapse all folders", f.view.CollapseAll)
	if sc, err := bar.GetStyleContext(); err == nil {
		sc.AddClass("linked")
	}
	return bar
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

// Reload rebuilds the tree from the project root, keeping expanded folders open.
func (f *FileTree) Reload() {
	expanded := f.expandedDirs(nil, nil)
	f.store.Clear()
	f.fill(nil, f.app.root)
	for _, p := range expanded {
		if iter, ok := f.iterFor(p); ok {
			if tp, err := f.store.GetPath(iter); err == nil {
				f.view.ExpandRow(tp, false)
			}
		}
	}
}

// expandedDirs lists the paths of expanded folders below parent, parents first.
func (f *FileTree) expandedDirs(parent *gtk.TreeIter, out []string) []string {
	var it gtk.TreeIter
	for ok := f.store.IterChildren(parent, &it); ok; ok = f.store.IterNext(&it) {
		p, isDir := f.rowInfo(&it)
		if !isDir {
			continue
		}
		if tp, err := f.store.GetPath(&it); err == nil && f.view.RowExpanded(tp) {
			out = append(out, p)
			out = f.expandedDirs(&it, out)
		}
	}
	return out
}

// iterFor finds the row for p, expanding its ancestors so it is loaded.
func (f *FileTree) iterFor(p string) (*gtk.TreeIter, bool) {
	rel, err := filepath.Rel(f.app.root, p)
	if err != nil || rel == "." || !insideRoot(rel) {
		return nil, false
	}
	var parent *gtk.TreeIter
	cur := f.app.root
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		if parent != nil {
			if tp, err := f.store.GetPath(parent); err == nil {
				f.view.ExpandRow(tp, false)
			}
		}
		cur = filepath.Join(cur, name)
		var it gtk.TreeIter
		found := false
		for ok := f.store.IterChildren(parent, &it); ok; ok = f.store.IterNext(&it) {
			if rp, _ := f.rowInfo(&it); rp == cur {
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
		row := it
		parent = &row
	}
	return parent, true
}

// reveal expands the tree down to p, selects it and scrolls it into view.
func (f *FileTree) reveal(p string) bool {
	iter, ok := f.iterFor(p)
	if !ok {
		return false
	}
	if tp, err := f.store.GetPath(iter); err == nil {
		f.view.SetCursor(tp, nil, false)
		f.view.ScrollToCell(tp, nil, true, 0, 0.3)
	}
	return true
}

// RevealCurrent shows the explorer and selects the file open in the current
// tab, reloading once in case the file was created outside EdMin.
func (f *FileTree) RevealCurrent() {
	e := f.app.editors.Current()
	if e == nil || e.Path == "" {
		return
	}
	f.app.showExplorer()
	if !f.reveal(e.Path) {
		f.Reload()
		f.reveal(e.Path)
	}
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
	} else if sel, err := f.view.GetSelection(); err == nil {
		// Clicked empty space: target the project root.
		sel.UnselectAll()
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
	if e := f.app.editors.Current(); e != nil && e.Path != "" {
		add("Jump to Open File", f.RevealCurrent)
	}
	menu.ShowAll()
	// Black on white whatever the theme, like the Settings window.
	if sc, err := menu.GetStyleContext(); err == nil {
		sc.AddClass("edmin-menu")
	}
	if top, err := menu.GetToplevel(); err == nil {
		if w, ok := top.(*gtk.Window); ok {
			if sc, err := w.GetStyleContext(); err == nil {
				sc.AddClass("edmin-menu")
			}
		}
	}
	menu.PopupAtPointer(ev)
	return true
}

func (f *FileTree) create(dir bool) {
	title := "New File"
	if dir {
		title = "New Folder"
	}
	base := f.selectedDir()
	name, ok := f.app.prompt(title, "Name (relative to "+relPath(f.app.root, base)+", may include subfolders):", "")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return
	}
	full := filepath.Join(base, name)
	if rel, err := filepath.Rel(f.app.root, full); err != nil || rel == "." || !insideRoot(rel) {
		f.app.showError("Could not create "+name, "The path must be inside the project folder.")
		return
	}
	err := os.MkdirAll(filepath.Dir(full), 0755)
	if err == nil {
		if dir {
			err = os.Mkdir(full, 0755)
		} else {
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
	f.reveal(full)
	if !dir {
		f.app.editors.Open(full)
	}
}

// deleteSelected asks before deleting the selected file or folder, then
// deletes it and closes any tabs open on it.
// arrowRight expands the selected folder.
func (f *FileTree) arrowRight() bool {
	sel, _ := f.view.GetSelection()
	_, iter, ok := sel.GetSelected()
	if !ok {
		return false
	}
	if _, isDir := f.rowInfo(iter); !isDir {
		return true
	}
	if tp, err := f.store.GetPath(iter); err == nil {
		f.view.ExpandRow(tp, false)
	}
	return true
}

// arrowLeft collapses the selected folder if it is open, else moves the
// selection to its parent folder.
func (f *FileTree) arrowLeft() bool {
	sel, _ := f.view.GetSelection()
	_, iter, ok := sel.GetSelected()
	if !ok {
		return false
	}
	tp, err := f.store.GetPath(iter)
	if err != nil {
		return true
	}
	if _, isDir := f.rowInfo(iter); isDir && f.view.RowExpanded(tp) {
		f.view.CollapseRow(tp)
		return true
	}
	if tp.GetDepth() > 1 && tp.Up() {
		f.view.SetCursor(tp, nil, false)
	}
	return true
}

func (f *FileTree) deleteSelected() {
	sel, _ := f.view.GetSelection()
	_, iter, ok := sel.GetSelected()
	if !ok {
		return
	}
	p, isDir := f.rowInfo(iter)
	if rel, err := filepath.Rel(f.app.root, p); p == "" || err != nil || rel == "." || !insideRoot(rel) {
		return
	}
	title, detail := "Delete file?", "%s will be permanently deleted."
	if isDir {
		title, detail = "Delete folder?", "%s and everything in it will be permanently deleted."
	}
	d := gtk.MessageDialogNew(f.app.win, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_NONE, "%s", title)
	f.app.themed(d)
	d.FormatSecondaryText(detail, relPath(f.app.root, p))
	d.AddButton("Cancel", gtk.RESPONSE_NO)
	d.AddButton("Yes", gtk.RESPONSE_YES)
	d.SetDefaultResponse(gtk.RESPONSE_YES)
	if w, err := d.GetWidgetForResponse(gtk.RESPONSE_YES); err == nil && w != nil {
		w.ToWidget().GrabFocus()
	}
	r := d.Run()
	d.Destroy()
	if r != gtk.RESPONSE_YES {
		return
	}
	var err error
	if isDir {
		err = os.RemoveAll(p)
	} else {
		err = os.Remove(p)
	}
	// Close tabs on anything that was deleted, even if only partly.
	for _, e := range append([]*Editor(nil), f.app.editors.editors...) {
		if e.Path == p || isDir && strings.HasPrefix(e.Path, p+string(filepath.Separator)) {
			if _, serr := os.Stat(e.Path); os.IsNotExist(serr) {
				e.Buf.SetModified(false)
				f.app.editors.Close(e)
			}
		}
	}
	f.Reload()
	if err != nil {
		f.app.showError("Could not delete "+filepath.Base(p), err.Error())
	}
}

// insideRoot reports whether a path relative to the project root stays inside it.
func insideRoot(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func relPath(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}
