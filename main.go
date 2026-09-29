// EdMin is a minimal text editor built with GTK3 (gotk3) and tree-sitter.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

type App struct {
	win   *gtk.Window
	root  string
	theme *Theme
	shell []string // what the project's terminals run; nil means $SHELL

	editors *EditorArea
	tree    *FileTree
	search  *SearchPanel
	build   *BuildPanel

	leftPanel  *gtk.Notebook
	rightPanel *gtk.Box
	termPanel  *gtk.Box
	termNB     *gtk.Notebook
	terminals  []*Terminal
	termCount  int
	termLabels map[*Terminal]*renamable

	status *gtk.Label

	leftBtn, termBtn, buildBtn *gtk.ToggleButton
}

// apps holds every open window; the program exits when the last one closes.
var apps []*App

func main() {
	gtk.Init(nil)
	loadThemeCSS()
	args := os.Args[1:]
	if len(args) == 0 {
		// Reopen the projects from last time, or else the current directory.
		for _, p := range loadSettings(settingsPath()).Open {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				args = append(args, p)
			}
		}
	}
	if len(args) == 0 {
		cwd, _ := os.Getwd()
		args = []string{cwd}
	}
	// Each argument (a folder or a file) opens in its own window.
	for _, arg := range args {
		root, _ := os.Getwd()
		var openFile string
		p, _ := filepath.Abs(arg)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			openFile = p
			root = filepath.Dir(p)
		} else if err == nil {
			root = p
		}
		app := newApp(root)
		if openFile != "" {
			app.editors.Open(openFile)
		}
	}
	gtk.Main()
}

// newApp opens a new window with root as its project folder.
func newApp(root string) *App {
	app := &App{root: root, theme: projectTheme(root)}
	apps = append(apps, app)
	app.buildUI()
	app.setRoot(root)
	app.restoreTerminals()
	return app
}

func (a *App) buildUI() {
	a.win, _ = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	a.win.SetDefaultSize(1300, 850)
	a.win.Connect("delete-event", func() bool { return !a.confirmQuit() })
	a.win.Connect("destroy", func() {
		for _, t := range a.terminals {
			t.Close()
		}
		for i, x := range apps {
			if x == a {
				apps = append(apps[:i], apps[i+1:]...)
				break
			}
		}
		if len(apps) == 0 {
			// The last window closing ends the session; keep its list so the
			// same projects reopen next time.
			gtk.MainQuit()
		} else {
			saveSession()
		}
	})
	a.win.Connect("key-press-event", a.onKey)

	a.editors = NewEditorArea(a)
	a.tree = NewFileTree(a)
	a.search = NewSearchPanel(a)
	a.build = NewBuildPanel(a)

	// Header bar with panel toggles.
	hb, _ := gtk.HeaderBarNew()
	hb.SetShowCloseButton(true)
	hb.SetTitle("EdMin")
	openBtn, _ := gtk.ButtonNewFromIconName("folder-open-symbolic", gtk.ICON_SIZE_BUTTON)
	openBtn.SetTooltipText("Open folder (Ctrl+O)")
	openBtn.Connect("clicked", a.openFolder)
	hb.PackStart(openBtn)
	newWinBtn, _ := gtk.ButtonNewFromIconName("window-new-symbolic", gtk.ICON_SIZE_BUTTON)
	newWinBtn.SetTooltipText("Open folder in new window (Ctrl+Shift+O)")
	newWinBtn.Connect("clicked", a.openFolderInNewWindow)
	hb.PackStart(newWinBtn)
	hb.PackStart(a.tree.Toolbar())
	settingsBtn, _ := gtk.ButtonNewFromIconName("open-menu-symbolic", gtk.ICON_SIZE_BUTTON)
	settingsBtn.SetTooltipText("Settings (Ctrl+,)")
	settingsBtn.Connect("clicked", a.showSettings)
	mkToggle := func(icon, tip string) *gtk.ToggleButton {
		b, _ := gtk.ToggleButtonNew()
		img, _ := gtk.ImageNewFromIconName(icon, gtk.ICON_SIZE_BUTTON)
		b.SetImage(img)
		b.SetTooltipText(tip)
		b.SetActive(true)
		return b
	}
	a.buildBtn = mkToggle("system-run-symbolic", "Build panel (Ctrl+Shift+B)")
	a.termBtn = mkToggle("utilities-terminal-symbolic", "Terminal (Ctrl+`)")
	a.leftBtn = mkToggle("view-list-symbolic", "Explorer (Ctrl+B)")
	hb.PackEnd(settingsBtn)
	hb.PackEnd(a.buildBtn)
	hb.PackEnd(a.termBtn)
	hb.PackEnd(a.leftBtn)
	a.win.SetTitlebar(hb)

	// Left: explorer + search tabs.
	a.leftPanel, _ = gtk.NotebookNew()
	filesLbl, _ := gtk.LabelNew("Files")
	searchLbl, _ := gtk.LabelNew("Search")
	a.leftPanel.AppendPage(a.tree.Root, filesLbl)
	a.leftPanel.AppendPage(a.search.Root, searchLbl)

	// Bottom: tabbed terminals.
	a.termPanel, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	a.termNB, _ = gtk.NotebookNew()
	a.termNB.SetScrollable(true)
	newTerm, _ := gtk.ButtonNewFromIconName("list-add-symbolic", gtk.ICON_SIZE_MENU)
	newTerm.SetRelief(gtk.RELIEF_NONE)
	newTerm.SetTooltipText("New terminal (Ctrl+Shift+`)")
	newTerm.Connect("clicked", func() { a.newTerminal() })
	newTerm.Show()
	a.termNB.SetActionWidget(newTerm, gtk.PACK_END)
	a.termNB.Connect("page-reordered", a.saveTerminals)
	a.termPanel.PackStart(a.termNB, true, true, 0)

	// Right: build commands.
	a.rightPanel = a.build.Root

	// Layout: [left | [[editor / terminal] | right]]
	vpaned, _ := gtk.PanedNew(gtk.ORIENTATION_VERTICAL)
	vpaned.Pack1(a.editors.Root, true, false)
	vpaned.Pack2(a.termPanel, false, false)
	vpaned.SetPosition(560)

	rpaned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	rpaned.Pack1(vpaned, true, false)
	rpaned.Pack2(a.rightPanel, false, false)
	rpaned.SetPosition(1300 - 250 - 240)

	lpaned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	lpaned.Pack1(a.leftPanel, false, false)
	lpaned.Pack2(rpaned, true, false)
	lpaned.SetPosition(250)

	a.status, _ = gtk.LabelNew("")
	a.status.SetXAlign(0)
	a.status.SetMarginStart(8)
	a.status.SetMarginTop(2)
	a.status.SetMarginBottom(2)
	if sc, err := a.status.GetStyleContext(); err == nil {
		sc.AddClass("edmin-status")
	}

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	box.PackStart(lpaned, true, true, 0)
	box.PackStart(a.status, false, false, 0)
	a.win.Add(box)
	a.win.ShowAll()
	a.editors.findBar.Hide()
	a.editors.updateWelcome()

	a.leftBtn.Connect("toggled", func() { a.leftPanel.SetVisible(a.leftBtn.GetActive()) })
	a.buildBtn.Connect("toggled", func() { a.rightPanel.SetVisible(a.buildBtn.GetActive()) })
	a.termBtn.Connect("toggled", func() {
		on := a.termBtn.GetActive()
		a.termPanel.SetVisible(on)
		if on && len(a.terminals) == 0 {
			a.newTerminal()
		}
		if on {
			if t := a.currentTerminal(); t != nil {
				t.Focus()
			}
		}
	})
}

// saveSession records the project folders open in windows.
func saveSession() {
	s := loadSettings(settingsPath())
	s.Open = nil
	seen := map[string]bool{}
	for _, a := range apps {
		if !seen[a.root] {
			seen[a.root] = true
			s.Open = append(s.Open, a.root)
		}
	}
	saveSettings(settingsPath(), s)
}

func (a *App) setRoot(root string) {
	a.root = root
	saveSession()
	a.setTheme(projectTheme(root))
	a.shell = projectShell(root)
	a.tree.Reload()
	a.build.Load()
	a.updateTitle()
}

func (a *App) updateTitle() {
	title := "EdMin — " + filepath.Base(a.root)
	if e := a.editors.Current(); e != nil {
		mod := ""
		if e.Buf.GetModified() {
			mod = "● "
		}
		title = mod + relPath(a.root, e.Path) + " — EdMin"
	}
	a.win.SetTitle(title)
	if hb, err := a.win.GetTitlebar(); err == nil && hb != nil {
		if h, ok := hb.(*gtk.HeaderBar); ok {
			h.SetTitle(title)
			h.SetSubtitle(a.root)
		}
	}
}

func (a *App) updateStatus() {
	e := a.editors.Current()
	if e == nil {
		a.status.SetText(a.root)
		return
	}
	it := e.Buf.GetIterAtMark(e.Buf.GetInsert())
	lang := "Plain Text"
	if e.lang != nil {
		lang = e.lang.Name
	}
	a.status.SetText(fmt.Sprintf("%s    Ln %d, Col %d    %s", relPath(a.root, e.Path), it.GetLine()+1, it.GetLineOffset()+1, lang))
}

func (a *App) setStatusMsg(msg string) { a.status.SetText(msg) }

// showExplorer opens the left panel on its Files tab.
func (a *App) showExplorer() {
	a.leftBtn.SetActive(true)
	a.leftPanel.SetCurrentPage(0)
}

// ---- Terminals ----

func (a *App) newTerminal() *Terminal { return a.newNamedTerminal("") }

// newNamedTerminal opens a terminal tab; an empty name gets "Terminal N".
func (a *App) newNamedTerminal(name string) *Terminal {
	a.termCount++
	var t *Terminal
	t = NewTerminal(a.root, a.shell, a.theme, func() { a.closeTerminal(t) })
	tab, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	lbl, _ := gtk.LabelNew(fmt.Sprintf("Terminal %d", a.termCount))
	lblBox := renamableLabel(lbl, a.saveTerminals)
	shell := a.shell
	if len(shell) == 0 {
		shell = defaultShell()
	}
	tab.SetTooltipText("Runs: " + strings.Join(shell, " ") + "\nDouble-click the name to rename")
	if name != "" {
		lbl.SetText(name)
		lblBox.custom = true
	}
	if a.termLabels == nil {
		a.termLabels = map[*Terminal]*renamable{}
	}
	a.termLabels[t] = lblBox
	cb, _ := gtk.ButtonNewFromIconName("window-close-symbolic", gtk.ICON_SIZE_MENU)
	cb.SetRelief(gtk.RELIEF_NONE)
	cb.SetFocusOnClick(false)
	cb.Connect("clicked", func() { a.closeTerminal(t) })
	tab.PackStart(lblBox, false, false, 0)
	tab.PackStart(cb, false, false, 0)
	tab.ShowAll()
	lblBox.finishShow()
	t.Root.ShowAll()
	a.terminals = append(a.terminals, t)
	n := a.termNB.AppendPage(t.Root, tab)
	a.termNB.SetTabReorderable(t.Root, true)
	a.termNB.SetCurrentPage(n)
	if !a.termBtn.GetActive() {
		a.termBtn.SetActive(true)
	}
	t.Focus()
	return t
}

// renamable is a tab label that turns into a text entry when double-clicked.
type renamable struct {
	*gtk.Box
	lbl    *gtk.Label
	ent    *gtk.Entry
	custom bool // renamed by the user
}

// renamableLabel wraps lbl; onRename runs after the user renames it.
func renamableLabel(lbl *gtk.Label, onRename func()) *renamable {
	r := &renamable{lbl: lbl}
	r.Box, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	eb, _ := gtk.EventBoxNew()
	eb.SetVisibleWindow(false)
	eb.Add(lbl)
	r.ent, _ = gtk.EntryNew()
	r.ent.SetWidthChars(12)
	r.PackStart(eb, false, false, 0)
	r.PackStart(r.ent, false, false, 0)

	editing := false
	finish := func(commit bool) {
		if !editing {
			return
		}
		editing = false
		r.ent.Hide()
		r.lbl.Show()
		if name, _ := r.ent.GetText(); commit && strings.TrimSpace(name) != "" {
			r.lbl.SetText(strings.TrimSpace(name))
			r.custom = true
			onRename()
		}
	}
	// Single clicks fall through to the notebook so the tab still switches.
	eb.Connect("button-press-event", func(_ *gtk.EventBox, ev *gdk.Event) bool {
		b := gdk.EventButtonNewFromEvent(ev)
		if b.Type() != gdk.EVENT_2BUTTON_PRESS || b.Button() != gdk.BUTTON_PRIMARY {
			return false
		}
		editing = true
		r.ent.SetText(r.lbl.GetLabel())
		r.lbl.Hide()
		r.ent.Show()
		r.ent.GrabFocus()
		return true
	})
	r.ent.Connect("activate", func() { finish(true) })
	r.ent.Connect("focus-out-event", func() bool { finish(true); return false })
	r.ent.Connect("key-press-event", func(_ *gtk.Entry, ev *gdk.Event) bool {
		if gdk.EventKeyNewFromEvent(ev).KeyVal() == gdk.KEY_Escape {
			finish(false)
			return true
		}
		return false
	})
	return r
}

// finishShow hides the entry after the tab has been shown with ShowAll.
func (r *renamable) finishShow() { r.ent.Hide() }

func (a *App) closeTerminal(t *Terminal) {
	a.dropTerminal(t)
	a.saveTerminals()
	if len(a.terminals) == 0 {
		a.termBtn.SetActive(false)
	}
}

// dropTerminal closes t and removes its tab without touching the saved list.
func (a *App) dropTerminal(t *Terminal) {
	for i, x := range a.terminals {
		if x == t {
			a.terminals = append(a.terminals[:i], a.terminals[i+1:]...)
			delete(a.termLabels, t)
			t.Close()
			a.termNB.RemovePage(a.termNB.PageNum(t.Root))
			break
		}
	}
}

func (a *App) terminalsFile() string {
	return filepath.Join(a.root, ".edmin", "terminals.json")
}

// saveTerminals stores the names of renamed terminal tabs, in tab order, so
// they reopen with the project. Tabs with default names aren't saved.
func (a *App) saveTerminals() {
	var names []string
	for i := 0; i < a.termNB.GetNPages(); i++ {
		w, err := a.termNB.GetNthPage(i)
		if err != nil {
			continue
		}
		for t, r := range a.termLabels {
			if r.custom && t.Root.Native() == w.ToWidget().Native() {
				names = append(names, r.lbl.GetLabel())
			}
		}
	}
	f := a.terminalsFile()
	if len(names) == 0 {
		// Nothing to remember; don't leave an empty file behind.
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			a.setStatusMsg("Could not save terminal names: " + err.Error())
		}
		return
	}
	data, _ := json.MarshalIndent(names, "", "  ")
	err := os.MkdirAll(filepath.Dir(f), 0755)
	if err == nil {
		err = os.WriteFile(f, append(data, '\n'), 0644)
	}
	if err != nil {
		a.setStatusMsg("Could not save terminal names: " + err.Error())
	}
}

// restoreTerminals opens the project's saved terminal tabs, or one default
// terminal if none were saved.
func (a *App) restoreTerminals() {
	var names []string
	if data, err := os.ReadFile(a.terminalsFile()); err == nil {
		json.Unmarshal(data, &names)
	}
	if len(names) == 0 {
		a.newTerminal()
		return
	}
	for _, n := range names {
		a.newNamedTerminal(n)
	}
	a.termNB.SetCurrentPage(0)
	if t := a.currentTerminal(); t != nil {
		t.Focus()
	}
}

func (a *App) currentTerminal() *Terminal {
	n := a.termNB.GetCurrentPage()
	if n < 0 {
		return nil
	}
	w, err := a.termNB.GetNthPage(n)
	if err != nil {
		return nil
	}
	for _, t := range a.terminals {
		if t.Root.Native() == w.ToWidget().Native() {
			return t
		}
	}
	return nil
}

func (a *App) focusedTerminal() *Terminal {
	for _, t := range a.terminals {
		if t.view.HasFocus() {
			return t
		}
	}
	return nil
}

func (a *App) runInTerminal(cmd string) {
	if !a.termBtn.GetActive() {
		a.termBtn.SetActive(true)
	}
	t := a.currentTerminal()
	if t == nil {
		t = a.newTerminal()
	}
	t.RunCommand(cmd)
	t.Focus()
}

// ---- Symbol navigation (Ctrl+Click) ----

// openBuffers returns the current text of open editors, keyed by path.
func (a *App) openBuffers() map[string]string {
	m := map[string]string{}
	for _, e := range a.editors.editors {
		m[e.Path] = e.Text()
	}
	return m
}

func (a *App) symbolAction(e *Editor, line, colByte int) {
	src := e.Text()
	lang := e.lang
	var name string
	if lang == nil {
		if name = wordAt(e); name == "" {
			return
		}
	}
	a.setStatusMsg("Looking up symbol…")
	open := a.openBuffers()
	root := a.root
	ext := filepath.Ext(e.Path)
	// Parsing (and a grammar's first query compilation) happens off the UI thread.
	go func() {
		clickedDef := false
		if lang != nil {
			sym, ok := lang.SymbolAt([]byte(src), line, colByte)
			if !ok {
				glib.IdleAdd(func() bool { a.setStatusMsg("No symbol under cursor"); return false })
				return
			}
			if sym.Local != nil {
				// Scoped to one embedded region (e.g. a regex named group).
				for i := range sym.Local {
					sym.Local[i].Path = e.Path
				}
				glib.IdleAdd(func() bool {
					a.showSymbolResults(e, line, sym.Name, sym.IsDef, sym.Local)
					return false
				})
				return
			}
			name, clickedDef = sym.Name, sym.IsDef
		}
		var refs []SymbolRef
		walkProject(root, func(p string) {
			// Search every file of the same language family (e.g. .js/.ts/.tsx,
			// .c/.h/.cpp), parsing each with its own grammar.
			pl := languageFor(p)
			if lang != nil && (pl == nil || pl.Family != lang.Family) || lang == nil && filepath.Ext(p) != ext {
				return
			}
			var data []byte
			if txt, ok := open[p]; ok {
				data = []byte(txt)
			} else if data = readTextFile(p); data == nil {
				return
			}
			if pl != nil {
				refs = append(refs, pl.FindRefs(p, data, name)...)
			} else {
				refs = append(refs, findWordRefs(p, data, name)...)
			}
		})
		glib.IdleAdd(func() bool {
			a.showSymbolResults(e, line, name, clickedDef, refs)
			return false
		})
	}()
}

func (a *App) showSymbolResults(from *Editor, line int, name string, clickedDef bool, refs []SymbolRef) {
	var defs []SymbolRef
	for _, r := range refs {
		if r.IsDef {
			defs = append(defs, r)
		}
	}
	if !clickedDef && len(defs) > 0 {
		if target, ok := pickDefinition(from.Path, line, defs); ok {
			a.updateStatus()
			if ed := a.editors.Open(target.Path); ed != nil {
				ed.GotoLine(target.Line, target.ColByte)
			}
			a.setStatusMsg(fmt.Sprintf("Definition of “%s” — %s:%d", name, relPath(a.root, target.Path), target.Line+1))
			return
		}
	}
	// No single definition to jump to: list definitions (first) and usages.
	a.showRefs(fmt.Sprintf("%s, %s of “%s”", pluralize(len(defs), "definition", "definitions"),
		pluralize(len(refs)-len(defs), "usage", "usages"), name), refs, name)
}

// pickDefinition chooses the most likely definition: the nearest preceding
// one in the same file, any in the same file, or the only one elsewhere.
func pickDefinition(path string, line int, defs []SymbolRef) (SymbolRef, bool) {
	var same []SymbolRef
	for _, d := range defs {
		if d.Path == path {
			same = append(same, d)
		}
	}
	if len(same) > 0 {
		sort.Slice(same, func(i, j int) bool { return same[i].Line < same[j].Line })
		best := same[0]
		for _, d := range same {
			if d.Line <= line {
				best = d
			}
		}
		return best, true
	}
	if len(defs) == 1 {
		return defs[0], true
	}
	// All candidates in one file (e.g. a Haskell signature plus its
	// equations, or overloads): jump to the first.
	oneFile := true
	for _, d := range defs {
		oneFile = oneFile && d.Path == defs[0].Path
	}
	if oneFile {
		first := defs[0]
		for _, d := range defs {
			if d.Line < first.Line {
				first = d
			}
		}
		return first, true
	}
	// Prefer definitions in the same directory (e.g. the same Go package).
	var dir []SymbolRef
	for _, d := range defs {
		if filepath.Dir(d.Path) == filepath.Dir(path) {
			dir = append(dir, d)
		}
	}
	if len(dir) == 1 {
		return dir[0], true
	}
	return SymbolRef{}, false
}

func (a *App) showRefs(title string, refs []SymbolRef, name string) {
	ms := make([]match, len(refs))
	for i, r := range refs {
		ms[i] = match{path: r.Path, line: r.Line, colByte: r.ColByte, text: r.LineText, isDef: r.IsDef}
	}
	a.search.gen.Add(1) // cancel any running text search
	a.search.show(title, ms, name)
	a.setStatusMsg(title)
	a.leftBtn.SetActive(true)
	a.leftPanel.SetCurrentPage(1)
}

// ---- Dialogs ----

func (a *App) showError(title, msg string) {
	d := gtk.MessageDialogNew(a.win, gtk.DIALOG_MODAL, gtk.MESSAGE_ERROR, gtk.BUTTONS_OK, "%s", title)
	a.themed(d)
	d.FormatSecondaryText("%s", msg)
	d.Run()
	d.Destroy()
}

// askSave asks whether to save changes; returns YES, NO, or CANCEL.
func (a *App) askSave(name string) gtk.ResponseType {
	d := gtk.MessageDialogNew(a.win, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_NONE,
		"Save changes to %s?", name)
	a.themed(d)
	d.AddButton("Don't Save", gtk.RESPONSE_NO)
	d.AddButton("Cancel", gtk.RESPONSE_CANCEL)
	d.AddButton("Save", gtk.RESPONSE_YES)
	d.SetDefaultResponse(gtk.RESPONSE_YES)
	r := d.Run()
	d.Destroy()
	return r
}

func (a *App) prompt(title, label, initial string) (string, bool) {
	d, _ := gtk.DialogNewWithButtons(title, a.win, gtk.DIALOG_MODAL,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL}, []interface{}{"OK", gtk.RESPONSE_OK})
	a.themed(d)
	d.SetDefaultResponse(gtk.RESPONSE_OK)
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	box.SetBorderWidth(10)
	l, _ := gtk.LabelNew(label)
	l.SetXAlign(0)
	e, _ := gtk.EntryNew()
	e.SetText(initial)
	e.SetActivatesDefault(true)
	box.PackStart(l, false, false, 0)
	box.PackStart(e, false, false, 0)
	ca, _ := d.GetContentArea()
	ca.Add(box)
	d.ShowAll()
	r := d.Run()
	text, _ := e.GetText()
	d.Destroy()
	return text, r == gtk.RESPONSE_OK
}

func (a *App) confirmQuit() bool {
	for _, e := range a.editors.Unsaved() {
		a.editors.nb.SetCurrentPage(a.editors.nb.PageNum(e.Root))
		switch a.askSave(filepath.Base(e.Path)) {
		case gtk.RESPONSE_YES:
			if !e.Save() {
				return false
			}
		case gtk.RESPONSE_NO:
		default:
			return false
		}
	}
	return true
}

// chooseFolder asks for a folder; it returns "" if the dialog is cancelled.
func (a *App) chooseFolder(title string) string {
	d, _ := gtk.FileChooserDialogNewWith2Buttons(title, a.win, gtk.FILE_CHOOSER_ACTION_SELECT_FOLDER,
		"Cancel", gtk.RESPONSE_CANCEL, "Open", gtk.RESPONSE_ACCEPT)
	a.themed(d)
	d.SetCurrentFolder(a.root)
	dir := ""
	if d.Run() == gtk.RESPONSE_ACCEPT {
		dir = d.GetFilename()
	}
	d.Destroy()
	return dir
}

// openFolderInNewWindow opens a chosen folder as a project in a new window,
// or raises the window that already has it open.
func (a *App) openFolderInNewWindow() {
	dir := a.chooseFolder("Open Folder in New Window")
	if dir == "" {
		return
	}
	for _, x := range apps {
		if x.root == dir {
			x.win.Present()
			return
		}
	}
	newApp(dir).win.Present()
}

func (a *App) openFolder() {
	if dir := a.chooseFolder("Open Folder"); dir != "" && dir != a.root {
		if !a.confirmQuit() {
			return
		}
		for len(a.editors.editors) > 0 {
			e := a.editors.editors[0]
			e.Buf.SetModified(false)
			a.editors.Close(e)
		}
		// The old project's terminals close with it; its saved names stay.
		for len(a.terminals) > 0 {
			a.dropTerminal(a.terminals[0])
		}
		a.termCount = 0
		a.setRoot(dir)
		a.restoreTerminals()
	}
}

// ---- Keyboard shortcuts ----

func (a *App) onKey(_ *gtk.Window, ev *gdk.Event) bool {
	k := gdk.EventKeyNewFromEvent(ev)
	mods := gdk.ModifierType(k.State()) & (gdk.CONTROL_MASK | gdk.SHIFT_MASK | gdk.MOD1_MASK)
	kv := gdk.KeyvalToLower(k.KeyVal())
	ctrl := mods == gdk.CONTROL_MASK
	ctrlShift := mods == gdk.CONTROL_MASK|gdk.SHIFT_MASK
	inTerm := a.focusedTerminal() != nil

	// Shortcuts that work everywhere, including inside a terminal.
	switch {
	case (ctrl || ctrlShift) && (kv == gdk.KEY_grave || kv == gdk.KEY_asciitilde || kv == gdk.KEY_dead_grave):
		if ctrlShift {
			a.newTerminal()
		} else {
			a.termBtn.SetActive(!a.termBtn.GetActive())
			if !a.termBtn.GetActive() {
				if e := a.editors.Current(); e != nil {
					e.View.GrabFocus()
				}
			}
		}
		return true
	case ctrlShift && kv == gdk.KEY_f:
		a.leftBtn.SetActive(true)
		a.leftPanel.SetCurrentPage(1)
		sel := ""
		if e := a.editors.Current(); e != nil {
			if s, en, ok := e.Buf.GetSelectionBounds(); ok && s.GetLine() == en.GetLine() {
				sel = s.GetText(en)
			}
		}
		a.search.Focus(sel)
		return true
	case ctrlShift && kv == gdk.KEY_b:
		a.buildBtn.SetActive(!a.buildBtn.GetActive())
		return true
	case ctrlShift && kv == gdk.KEY_o:
		a.openFolderInNewWindow()
		return true
	}
	if inTerm || !ctrl && !ctrlShift {
		return false
	}

	e := a.editors.Current()
	editorFocused := e != nil && e.View.HasFocus()
	switch kv {
	case gdk.KEY_s:
		if ctrl && e != nil {
			e.Save()
			return true
		}
	case gdk.KEY_w:
		if ctrl && e != nil {
			a.editors.Close(e)
			return true
		}
	case gdk.KEY_f:
		if ctrl && e != nil {
			a.editors.ShowFind()
			return true
		}
	case gdk.KEY_b:
		if ctrl {
			a.leftBtn.SetActive(!a.leftBtn.GetActive())
			return true
		}
	case gdk.KEY_n:
		a.showExplorer()
		a.tree.create(ctrlShift)
		return true
	case gdk.KEY_o:
		if ctrl {
			a.openFolder()
			return true
		}
	case gdk.KEY_comma:
		if ctrl {
			a.showSettings()
			return true
		}
	case gdk.KEY_z:
		if editorFocused {
			if ctrl {
				e.Undo()
			} else {
				e.Redo()
			}
			return true
		}
	case gdk.KEY_y:
		if ctrl && editorFocused {
			e.Redo()
			return true
		}
	case gdk.KEY_g:
		if ctrl && e != nil {
			if s, ok := a.prompt("Go to Line", "Line number:", ""); ok {
				var n int
				if _, err := fmt.Sscan(s, &n); err == nil && n > 0 {
					e.GotoLine(n-1, 0)
				}
			}
			return true
		}
	}
	return false
}
