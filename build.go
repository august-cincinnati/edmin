package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// BuildCommand is a named shell command stored per project.
type BuildCommand struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// BuildPanel lists named commands; double-click runs one in the terminal.
type BuildPanel struct {
	app   *App
	Root  *gtk.Box
	view  *gtk.TreeView
	store *gtk.ListStore
	cmds  []BuildCommand
}

func NewBuildPanel(app *App) *BuildPanel {
	b := &BuildPanel{app: app}
	b.Root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	b.Root.SetMarginTop(4)

	header, _ := gtk.LabelNew("")
	header.SetMarkup("<b>Build Commands</b>")
	header.SetXAlign(0)
	header.SetMarginStart(6)

	b.store, _ = gtk.ListStoreNew(glib.TYPE_STRING, glib.TYPE_STRING)
	b.view, _ = gtk.TreeViewNewWithModel(b.store)
	b.view.SetHeadersVisible(false)
	b.view.SetTooltipColumn(1)
	r, _ := gtk.CellRendererTextNew()
	col, _ := gtk.TreeViewColumnNewWithAttribute("", r, "text", 0)
	b.view.AppendColumn(col)
	b.view.Connect("row-activated", func(_ *gtk.TreeView, path *gtk.TreePath) {
		if i := path.GetIndices(); len(i) > 0 && i[0] < len(b.cmds) {
			b.app.runInTerminal(b.cmds[i[0]].Command)
		}
	})
	sw, _ := gtk.ScrolledWindowNew(nil, nil)
	sw.Add(b.view)

	btns, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 2)
	btns.SetMarginStart(4)
	btns.SetMarginEnd(4)
	btns.SetMarginBottom(4)
	mk := func(icon, tip string, fn func()) {
		bt, _ := gtk.ButtonNewFromIconName(icon, gtk.ICON_SIZE_BUTTON)
		bt.SetTooltipText(tip)
		bt.Connect("clicked", fn)
		btns.PackStart(bt, false, false, 0)
	}
	mk("list-add-symbolic", "Add command", func() { b.edit(-1) })
	mk("document-edit-symbolic", "Edit selected command", func() {
		if i := b.selected(); i >= 0 {
			b.edit(i)
		}
	})
	mk("list-remove-symbolic", "Remove selected command", b.remove)
	mk("media-playback-start-symbolic", "Run selected command", func() {
		if i := b.selected(); i >= 0 {
			b.app.runInTerminal(b.cmds[i].Command)
		}
	})

	hint, _ := gtk.LabelNew("")
	hint.SetMarkup("<small>Double-click to run in the terminal</small>")
	hint.SetLineWrap(true)

	b.Root.PackStart(header, false, false, 0)
	b.Root.PackStart(sw, true, true, 0)
	b.Root.PackStart(hint, false, false, 0)
	b.Root.PackStart(btns, false, false, 0)
	return b
}

func (b *BuildPanel) file() string {
	return filepath.Join(b.app.root, ".edmin", "commands.json")
}

func (b *BuildPanel) Load() {
	b.cmds = nil
	if data, err := os.ReadFile(b.file()); err == nil {
		json.Unmarshal(data, &b.cmds)
	}
	b.refresh()
}

func (b *BuildPanel) save() {
	data, _ := json.MarshalIndent(b.cmds, "", "  ")
	if err := os.MkdirAll(filepath.Dir(b.file()), 0755); err == nil {
		err = os.WriteFile(b.file(), append(data, '\n'), 0644)
		if err == nil {
			return
		}
		b.app.showError("Could not save build commands", err.Error())
	}
}

func (b *BuildPanel) refresh() {
	b.store.Clear()
	for _, c := range b.cmds {
		it := b.store.Append()
		b.store.Set(it, []int{0, 1}, []interface{}{c.Name, c.Command})
	}
}

func (b *BuildPanel) selected() int {
	sel, _ := b.view.GetSelection()
	_, iter, ok := sel.GetSelected()
	if !ok {
		return -1
	}
	path, err := b.store.GetPath(iter)
	if err != nil {
		return -1
	}
	return path.GetIndices()[0]
}

func (b *BuildPanel) remove() {
	i := b.selected()
	if i < 0 {
		return
	}
	b.cmds = append(b.cmds[:i], b.cmds[i+1:]...)
	b.save()
	b.refresh()
}

// edit shows the add/edit dialog; idx < 0 adds a new command.
func (b *BuildPanel) edit(idx int) {
	var cur BuildCommand
	title := "Add Build Command"
	if idx >= 0 {
		cur = b.cmds[idx]
		title = "Edit Build Command"
	}
	d, _ := gtk.DialogNewWithButtons(title, b.app.win, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL}, []interface{}{"Save", gtk.RESPONSE_OK})
	b.app.themed(d)
	d.SetDefaultResponse(gtk.RESPONSE_OK)
	d.SetDefaultSize(420, -1)
	grid, _ := gtk.GridNew()
	grid.SetRowSpacing(6)
	grid.SetColumnSpacing(8)
	grid.SetBorderWidth(10)
	nl, _ := gtk.LabelNew("Name")
	nl.SetXAlign(0)
	cl, _ := gtk.LabelNew("Command")
	cl.SetXAlign(0)
	ne, _ := gtk.EntryNew()
	ne.SetText(cur.Name)
	ne.SetHExpand(true)
	ne.SetActivatesDefault(true)
	ce, _ := gtk.EntryNew()
	ce.SetText(cur.Command)
	ce.SetPlaceholderText("e.g. go build ./...")
	ce.SetActivatesDefault(true)
	grid.Attach(nl, 0, 0, 1, 1)
	grid.Attach(ne, 1, 0, 1, 1)
	grid.Attach(cl, 0, 1, 1, 1)
	grid.Attach(ce, 1, 1, 1, 1)
	ca, _ := d.GetContentArea()
	ca.Add(grid)
	d.ShowAll()
	resp := d.Run()
	name, _ := ne.GetText()
	command, _ := ce.GetText()
	d.Destroy()
	if resp != gtk.RESPONSE_OK || command == "" {
		return
	}
	if name == "" {
		name = command
	}
	c := BuildCommand{Name: name, Command: command}
	if idx >= 0 {
		b.cmds[idx] = c
	} else {
		b.cmds = append(b.cmds, c)
	}
	b.save()
	b.refresh()
}
