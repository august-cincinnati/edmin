package main

import (
	"runtime"

	"github.com/gotk3/gotk3/gdk"
)

// shortcutMods reduces an event's modifier state to Ctrl, Shift and Alt,
// counting macOS's Command key (reported as Meta) as Ctrl so that Cmd+S
// works like Ctrl+S there.
func shortcutMods(state uint) gdk.ModifierType {
	m := gdk.ModifierType(state)
	if isCommand(state) {
		m |= gdk.CONTROL_MASK
	}
	return m & (gdk.CONTROL_MASK | gdk.SHIFT_MASK | gdk.MOD1_MASK)
}

// isCommand reports whether macOS's Command key is held.
func isCommand(state uint) bool {
	return runtime.GOOS == "darwin" && gdk.ModifierType(state)&gdk.META_MASK != 0
}
