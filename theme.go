package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
)

// Theme holds every colour EdMin draws with.
type Theme struct {
	Name string

	BG, FG    string // editor and list surfaces
	Panel     string // window chrome: header bar, tabs, status bar
	Border    string
	Hover     string
	Selection string
	Dim       string // line numbers in search results
	MatchBG   string // search matches
	MatchFG   string
	JumpLine  string // line highlighted after a jump
	TermBG    string
	TermFG    string
	Syntax    map[string]string // highlight group → foreground
	DefMarker string            // "def" label in symbol results
}

var themes = []*Theme{
	{
		Name: "Light", BG: "#ffffff", FG: "#1f1f1f", Panel: "#f3f3f3", Border: "#d4d4d4", Hover: "#e4e4e4",
		Selection: "#add6ff", Dim: "#888888", MatchBG: "#ffe066", MatchFG: "#1f1f1f", JumpLine: "#e8f0fe",
		TermBG: "#1e1e1e", TermFG: "#d4d4d4", DefMarker: "#267f99",
		Syntax: map[string]string{
			hlKeyword: "#af00db", hlString: "#a31515", hlComment: "#008000", hlNumber: "#098658",
			hlType: "#267f99", hlFunction: "#795e26", hlConstant: "#0000ff", hlProperty: "#001080",
		},
	},
	{
		Name: "Dark", BG: "#1e1e1e", FG: "#d4d4d4", Panel: "#252526", Border: "#3c3c3c", Hover: "#2f3033",
		Selection: "#264f78", Dim: "#858585", MatchBG: "#9e6a03", MatchFG: "#ffffff", JumpLine: "#2a3550",
		TermBG: "#181818", TermFG: "#d4d4d4", DefMarker: "#4ec9b0",
		Syntax: map[string]string{
			hlKeyword: "#c586c0", hlString: "#ce9178", hlComment: "#6a9955", hlNumber: "#b5cea8",
			hlType: "#4ec9b0", hlFunction: "#dcdcaa", hlConstant: "#569cd6", hlProperty: "#9cdcfe",
		},
	},
	{
		Name: "Tan", BG: "#f4ecd8", FG: "#433422", Panel: "#e9dfc7", Border: "#cdbf9e", Hover: "#dfd3b6",
		Selection: "#d9c7a0", Dim: "#8c7b61", MatchBG: "#f2c14e", MatchFG: "#2e2416", JumpLine: "#ebdcb8",
		TermBG: "#3c3428", TermFG: "#ebdbb2", DefMarker: "#076678",
		Syntax: map[string]string{
			hlKeyword: "#9d0006", hlString: "#79740e", hlComment: "#928374", hlNumber: "#8f3f71",
			hlType: "#b57614", hlFunction: "#076678", hlConstant: "#af3a03", hlProperty: "#427b58",
		},
	},
	{
		Name: "Solarized Dark", BG: "#002b36", FG: "#93a1a1", Panel: "#073642", Border: "#0e4b59", Hover: "#0a4250",
		Selection: "#11505f", Dim: "#657b83", MatchBG: "#b58900", MatchFG: "#002b36", JumpLine: "#0b3d49",
		TermBG: "#002b36", TermFG: "#93a1a1", DefMarker: "#2aa198",
		Syntax: map[string]string{
			hlKeyword: "#859900", hlString: "#2aa198", hlComment: "#586e75", hlNumber: "#d33682",
			hlType: "#b58900", hlFunction: "#268bd2", hlConstant: "#cb4b16", hlProperty: "#6c71c4",
		},
	},
}

func themeByName(name string) *Theme {
	for _, t := range themes {
		if t.Name == name {
			return t
		}
	}
	return themes[0]
}

// css styles the window chrome, editors, lists and terminals.
func (t *Theme) css() string {
	return fmt.Sprintf(`
window, .background, headerbar, notebook header, notebook tab, paned > separator, .edmin-status {
	background-color: %[3]s; background-image: none; color: %[2]s; border-color: %[4]s; }
headerbar { box-shadow: none; }
notebook header { border-color: %[4]s; }
notebook tab:checked { background-color: %[1]s; }
notebook tab label, headerbar label { color: %[2]s; }
textview, textview text, textview border, treeview.view, list, list row, viewport, entry, iconview {
	background-color: %[1]s; color: %[2]s; border-color: %[4]s; }
textview text selection, entry selection, treeview.view:selected, list row:selected, row:selected {
	background-color: %[6]s; color: %[2]s; }
treeview.view header button { background-color: %[3]s; color: %[2]s; }
button, button.flat, headerbar button {
	background-color: %[3]s; background-image: none; color: %[2]s; border-color: %[4]s;
	box-shadow: none; text-shadow: none; -gtk-icon-shadow: none; }
button:hover { background-color: %[5]s; }
button:checked, button:active { background-color: %[6]s; }
checkbutton, radiobutton, label { color: %[2]s; }
#edmin-terminal, #edmin-terminal text, #edmin-terminal border { background-color: %[7]s; color: %[8]s; }
textview text, entry { caret-color: %[2]s; }
scrollbar, scrollbar trough { background-color: %[3]s; border-color: %[4]s; }
scrollbar slider { background-color: %[9]s; }
scrollbar slider:hover { background-color: %[2]s; }
`, t.BG, t.FG, t.Panel, t.Border, t.Hover, t.Selection, t.TermBG, t.TermFG, t.Dim)
}

// cssClass is the style class that scopes t's CSS to the windows using it.
func (t *Theme) cssClass() string {
	for i, x := range themes {
		if x == t {
			return fmt.Sprintf("edmin-theme-%d", i)
		}
	}
	return ""
}

// scopedCSS returns t's CSS with every selector limited to toplevels (windows,
// dialogs, menus) carrying t's style class, so each window can use its own
// theme.
func (t *Theme) scopedCSS() string {
	cls := t.cssClass()
	var b strings.Builder
	for _, rule := range strings.SplitAfter(t.css(), "}") {
		sels, body, ok := strings.Cut(rule, "{")
		if !ok {
			continue
		}
		var scoped []string
		for _, sel := range strings.Split(sels, ",") {
			sel = strings.TrimSpace(sel)
			if rest, isWin := strings.CutPrefix(sel, "window"); isWin {
				scoped = append(scoped, "window."+cls+rest)
			} else {
				scoped = append(scoped, "."+cls+" "+sel)
			}
		}
		b.WriteString(strings.Join(scoped, ", ") + " {" + body + "\n")
	}
	return b.String()
}

// loadThemeCSS installs the CSS for every theme once; windows pick theirs
// through a style class.
func loadThemeCSS() {
	var css strings.Builder
	for _, t := range themes {
		css.WriteString(t.scopedCSS())
	}
	p, _ := gtk.CssProviderNew()
	p.LoadFromData(css.String())
	screen, _ := gdk.ScreenGetDefault()
	gtk.AddProviderForScreen(screen, p, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// styled is any widget with a style context: windows, dialogs, menus.
type styled interface {
	GetStyleContext() (*gtk.StyleContext, error)
}

// themed gives a toplevel (the window, or one of its dialogs or menus) the
// window's theme.
func (a *App) themed(w styled) {
	sc, err := w.GetStyleContext()
	if err != nil {
		return
	}
	for _, t := range themes {
		sc.RemoveClass(t.cssClass())
	}
	sc.AddClass(a.theme.cssClass())
}

// setTheme switches this window to t and restyles everything open in it.
func (a *App) setTheme(t *Theme) {
	a.theme = t
	a.themed(a.win)
	for _, e := range a.editors.editors {
		e.styleTags()
	}
	for _, term := range a.terminals {
		term.restyle(t)
	}
	a.search.restyle()
}

// ---- Persisted settings ----

type Settings struct {
	Theme string `json:"theme,omitempty"`
	// Shell is the command a project's terminals run, as a list of
	// arguments. Only used in the project file; empty means detect it.
	Shell []string `json:"shell,omitempty"`
	// Open lists the project folders open in windows, reopened when EdMin
	// starts without arguments. Only used in the user-wide file.
	Open []string `json:"open,omitempty"`
}

// settingsPath is the user-wide settings file. Its theme is the default for
// projects that haven't chosen their own.
func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "edmin", "settings.json")
}

// projectSettingsPath holds the theme chosen for one project.
func projectSettingsPath(root string) string {
	return filepath.Join(root, ".edmin", "settings.json")
}

func loadSettings(p string) Settings {
	var s Settings
	if p != "" {
		if data, err := os.ReadFile(p); err == nil {
			json.Unmarshal(data, &s)
		}
	}
	return s
}

// projectTheme is the theme saved for root, or else the user's default.
func projectTheme(root string) *Theme {
	if s := loadSettings(projectSettingsPath(root)); s.Theme != "" {
		return themeByName(s.Theme)
	}
	return themeByName(loadSettings(settingsPath()).Theme)
}

func saveSettings(p string, s Settings) error {
	if p == "" {
		return fmt.Errorf("no user config directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// ---- Settings window ----

func (a *App) showSettings() {
	d, _ := gtk.DialogNewWithButtons("Settings", a.win, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Close", gtk.RESPONSE_CLOSE})
	d.SetDefaultSize(320, -1)
	a.themed(d)
	box, _ := d.GetContentArea()
	box.SetSpacing(6)
	box.SetMarginStart(16)
	box.SetMarginEnd(16)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)

	head, _ := gtk.LabelNew("")
	head.SetMarkup("<b>Theme</b> <small>(this window)</small>")
	head.SetXAlign(0)
	box.PackStart(head, false, false, 0)

	var group *gtk.RadioButton
	for _, t := range themes {
		t := t
		rb, _ := gtk.RadioButtonNewWithLabelFromWidget(group, t.Name)
		if group == nil {
			group = rb
		}
		rb.SetActive(t == a.theme)
		rb.Connect("toggled", func() {
			if !rb.GetActive() || t == a.theme {
				return
			}
			a.setTheme(t)
			a.themed(d)
			// Remember it for this project, and as the default for new ones.
			ps := loadSettings(projectSettingsPath(a.root))
			ps.Theme = t.Name
			err := saveSettings(projectSettingsPath(a.root), ps)
			if err == nil {
				s := loadSettings(settingsPath())
				s.Theme = t.Name
				err = saveSettings(settingsPath(), s)
			}
			if err != nil {
				a.setStatusMsg("Could not save settings: " + err.Error())
			}
		})
		box.PackStart(rb, false, false, 0)
	}
	d.ShowAll()
	d.Run()
	d.Destroy()
}
