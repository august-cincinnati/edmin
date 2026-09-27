# EdMin

EdMin is a minimal text editor written in Go, built with GTK3 through
[gotk3](https://github.com/gotk3/gotk3). It uses the official
[tree-sitter Go binding](https://github.com/tree-sitter/go-tree-sitter) and
the official grammars from the [tree-sitter](https://github.com/tree-sitter)
organization for syntax highlighting and symbol navigation. Those are its only
dependencies; everything else, including the terminal emulator, is plain Go.

## Features

- **File explorer** on the left. Folders load when you expand them.
  Right-click for *New File*, *New Folder* and *Refresh*. `.git` and
  `node_modules` are hidden.
- **Tabbed editor.** Tabs can be dragged to reorder, and a `●` marks
  unsaved changes. The editor has:
  - line numbers, with the current line emphasized
  - syntax highlighting for every supported language (see below)
  - auto-indent
  - undo and redo, grouped by word
- **Find in file** (`Ctrl+F`) highlights every match and has next/previous
  and a *Match case* option.
- **Find in project** (`Ctrl+Shift+F`) searches every text file, with
  results grouped by file. Click a result to open the file at that line.
- **Go to definition / Find usages** (`Ctrl+Click` on a symbol):
  - Clicking a usage jumps to its definition.
  - Otherwise (you clicked a definition, there are several candidates, or
    the definition is outside the project) the search panel lists a
    **Definitions** group first, followed by every usage grouped by file.
  - Works in every language listed under [Languages](#languages).
- **Terminals** in tabs along the bottom. Each runs your `$SHELL` through a
  pseudo-terminal and a built-in xterm-compatible emulator that supports
  colors, scrollback, and full-screen programs such as `vim` and `less`.
- **Build panel** on the right for saving named shell commands.
  Double-click a command, or select it and press ▶, to run it in the
  current terminal.

The explorer, terminal and build panels can each be hidden, using the
header-bar toggles or the keyboard shortcuts below.

## Languages

EdMin supports every language with an official tree-sitter parser:

| Language   | File types                              | Definitions come from            |
| ---------- | --------------------------------------- | -------------------------------- |
| Agda       | `.agda`                                 | structural rules                 |
| Bash       | `.sh` `.bash` `.bashrc` `.bash_profile` | structural rules                 |
| C          | `.c` `.h`                               | `tags.scm` + structural rules    |
| C++        | `.cc` `.cpp` `.cxx` `.hpp` `.hh` …      | `tags.scm` + structural rules    |
| C#         | `.cs`                                   | `tags.scm` + structural rules    |
| CSS        | `.css`                                  | structural rules (properties, custom properties, keyframes) |
| ERB / EJS  | `.erb` / `.ejs`                         | embedded Ruby / JavaScript       |
| Go         | `.go`                                   | `tags.scm` + structural rules    |
| Haskell    | `.hs` `.hs-boot`                        | `locals.scm` + structural rules  |
| HTML       | `.html` `.htm`                          | usages only (tag names)          |
| Java       | `.java`                                 | `tags.scm` + structural rules    |
| JavaScript | `.js` `.mjs` `.cjs` `.jsx`              | `tags.scm` + `locals.scm`        |
| JSDoc      | `/** … */` comments in JS/TS files      | type names resolve to JS/TS definitions |
| JSON       | `.json` `.jsonc`                        | object keys                      |
| Julia      | `.jl`                                   | `locals.scm` + structural rules  |
| OCaml      | `.ml` `.mli`                            | `tags.scm` + `locals.scm`        |
| PHP        | `.php` `.phtml`                         | `tags.scm` + structural rules    |
| Python     | `.py` `.pyi`                            | `tags.scm` + structural rules    |
| Regex      | regex literals in JS/TS files           | named groups and `\k<name>` backreferences |
| Ruby       | `.rb` `Rakefile` `Gemfile` …            | `tags.scm` + `locals.scm`        |
| Rust       | `.rs`                                   | `tags.scm` + structural rules    |
| Scala      | `.scala` `.sbt` `.sc`                   | `tags.scm` + `locals.scm`        |
| TypeScript | `.ts` `.tsx` `.mts` `.cts`              | `tags.scm` + `locals.scm`        |
| Verilog    | `.v` `.sv` `.vh` `.svh`                 | structural rules                 |

**Highlighting** uses each grammar's official `highlights.scm`. Verilog
ships no highlight query, so it gets a simpler built-in highlighter for
comments, strings, numbers and keywords. In ERB and EJS files, the code
inside `<% %>` is also highlighted as Ruby or JavaScript.

**Definitions** come from three sources:
- the grammar's `tags.scm` (the query format GitHub's code navigation uses)
- its `locals.scm`, where there is one
- structural rules for grammars that ship neither, such as a declaration's
  `name` field or the first identifier in a Verilog or Agda declaration

**Searching across files.** Ctrl+Click looks through every file in the
same language family, parsing each one with its own grammar. The families
are:
- `.js` `.jsx` `.ts` `.tsx` `.ejs`
- `.c` `.h` `.cpp` `.hpp`
- `.rb` `.erb`
- `.ml` `.mli`

**Vendored queries.** The query files are copied from the grammar
repositories into `queries/` (each with its MIT license) and embedded in the
binary. `queries/VERSIONS` records the grammar versions they came from. If
you upgrade a grammar in `go.mod`, copy its queries again from the module
cache.

## Requirements

- Linux
- Go 1.25 or newer
- GTK 3 development files and a C compiler (gotk3 and tree-sitter use cgo)

On Debian/Ubuntu:

```sh
sudo apt install golang-go libgtk-3-dev build-essential
```

## Building

```sh
go build -o edmin .
```

The first build compiles gotk3 and the tree-sitter grammars, which takes a
few minutes. Later builds are fast.

## Usage

```sh
./edmin                 # open the current directory as the project
./edmin path/to/project # open a folder
./edmin path/to/file.go # open a file (its folder becomes the project)
```

Use the folder button in the header bar, or `Ctrl+O`, to switch projects.

## Keyboard shortcuts

| Shortcut                    | Action                                   |
| --------------------------- | ---------------------------------------- |
| `Ctrl+S`                    | Save current file                        |
| `Ctrl+W`                    | Close current tab                        |
| `Ctrl+Z` / `Ctrl+Shift+Z`   | Undo / redo (`Ctrl+Y` also redoes)       |
| `Ctrl+F`                    | Find in file (`Enter` / `Shift+Enter` to step, `Esc` to close) |
| `Ctrl+Shift+F`              | Find in project                          |
| `Ctrl+G`                    | Go to line                               |
| `Ctrl+Click`                | Go to definition / find usages           |
| `Ctrl+O`                    | Open folder                              |
| `Ctrl+B`                    | Toggle file explorer                     |
| `` Ctrl+` ``                | Toggle terminal panel                    |
| `` Ctrl+Shift+` ``          | New terminal                             |
| `Ctrl+Shift+B`              | Toggle build panel                       |
| `Ctrl+Shift+C` / `Ctrl+Shift+V` | Copy / paste in the terminal         |
| `Shift+PageUp` / `Shift+PageDown` | Scroll terminal history            |

When a terminal has focus, plain `Ctrl+<key>` combinations such as `Ctrl+C`
and `Ctrl+B` go to the shell. Only the `Ctrl+Shift` shortcuts and `` Ctrl+` ``
are handled by the editor.

## Build commands

Build commands are saved per project in `.edmin/commands.json`:

```json
[
  { "name": "Run tests", "command": "go test ./..." },
  { "name": "Build", "command": "go build -o edmin ." }
]
```

You can edit this file by hand or with the panel's add, edit and remove
buttons.

## Project layout

| File          | Purpose                                                   |
| ------------- | --------------------------------------------------------- |
| `main.go`     | Window layout, panels, keyboard shortcuts, symbol lookup, dialogs |
| `editor.go`   | Editor tabs, highlighting, undo/redo, in-file search      |
| `gutter.go`   | Line-number gutter                                        |
| `filetree.go` | File explorer                                             |
| `search.go`   | Project-wide search and the results panel                 |
| `lang.go`     | Language table, highlighting and definition/usage analysis |
| `queries/`    | Official tree-sitter query files, embedded into the binary |
| `build.go`    | Build commands panel                                      |
| `terminal.go` | Terminal widget (rendering and keyboard input)            |
| `vt.go`       | VT100/xterm screen emulator (no GTK code)                 |
| `pty.go`      | Pseudo-terminal support using Linux ioctls                |
| `util.go`     | Word-based fallbacks for files without a grammar          |

## Testing

```sh
go test ./...
```

The tests cover:
- the terminal emulator
- a real shell session through the pseudo-terminal
- for every supported language: that its queries compile, that it
  highlights, and that Ctrl+Click on a usage finds the definition
- JSDoc type names and regex named groups

## Limitations

- **Symbol navigation matches by name.** It is not type-aware. When several
  definitions share a name, EdMin prefers one in the same file, then the
  same directory, then the first one when all candidates are in one file,
  and otherwise lists them all. Files without a tree-sitter grammar fall
  back to whole-word text matching.
- **Query compatibility safeguard.** The Go binding compiles `#match?`
  predicates with Go's `regexp` package. If a future query uses a pattern
  Go can't compile, or a node type the grammar lacks, EdMin drops just that
  pattern rather than the whole query. With the current grammar versions,
  every pattern compiles.
- **The terminal emulator is not complete.** Mouse reporting is not
  supported, and wide characters (CJK, emoji) are treated as one column.
- **Linux only.** The pseudo-terminal code uses Linux-specific ioctls.
- **gotk3 version.** The latest gotk3 release (v0.6.4) does not compile
  because of a missing import, so `go.mod` pins a newer commit from its
  master branch.
