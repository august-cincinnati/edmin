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

// panelDigit returns which of the panel shortcut digits 1-4 a key is, or 0.
// With Shift held most layouts report the shifted symbol rather than the
// digit, so the US symbols above 1-4 count too, as do keypad digits.
func panelDigit(kv uint) int {
	switch kv {
	case gdk.KEY_1, gdk.KEY_exclam, gdk.KEY_KP_1, gdk.KEY_KP_End:
		return 1
	case gdk.KEY_2, gdk.KEY_at, gdk.KEY_KP_2, gdk.KEY_KP_Down:
		return 2
	case gdk.KEY_3, gdk.KEY_numbersign, gdk.KEY_KP_3, gdk.KEY_KP_Next:
		return 3
	case gdk.KEY_4, gdk.KEY_dollar, gdk.KEY_KP_4, gdk.KEY_KP_Left:
		return 4
	}
	return 0
}
