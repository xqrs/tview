package tview

import "github.com/gdamore/tcell/v3"

// Primitive is the top-most interface for all graphical primitives.
type Primitive interface {
	// Draw draws this primitive onto the screen. Implementers can call the
	// screen's ShowCursor() function but should only do so when they have focus.
	// (They will need to keep track of this themselves.)
	Draw(screen tcell.Screen)

	// GetRect returns the current position of the primitive, x, y, width, and
	// height.
	GetRect() (int, int, int, int)
	// SetRect sets a new position of the primitive.
	SetRect(x, y, width, height int)

	// InputHandler receives key events when this primitive has focus.
	InputHandler(event *tcell.EventKey) Command
	// MouseHandler receives mouse events.
	// The returned capture primitive (if non-nil) receives follow-up mouse events until the capture is released.
	MouseHandler(action MouseAction, event *tcell.EventMouse) (Primitive, Command)
	// PasteHandler receives pasted text.
	PasteHandler(text string) Command

	// HasFocus determines if the primitive has focus. This function must return
	// true also if one of this primitive's child elements has focus.
	HasFocus() bool
	// Focus is called by the application when the primitive receives focus.
	// Implementers may call delegate() to pass the focus on to another primitive.
	Focus(delegate func(p Primitive))
	// Blur is called by the application when the primitive loses focus.
	Blur()
}
