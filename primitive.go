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

	// InputHandler receives key events when this primitive has focus. It is
	// called by the Application class.
	//
	// The setFocus function allows implementations to pass focus to a different
	// primitive so that future key events are sent to that primitive.
	//
	// The Application's Draw() function will be called automatically after the
	// handler returns.
	InputHandler(event *tcell.EventKey, setFocus func(p Primitive))

	// Focus is called by the application when the primitive receives focus.
	// Implementers may call delegate() to pass the focus on to another primitive.
	Focus(delegate func(p Primitive))

	// HasFocus determines if the primitive has focus. This function must return
	// true also if one of this primitive's child elements has focus.
	HasFocus() bool

	// Blur is called by the application when the primitive loses focus.
	Blur()

	// MouseHandler returns a handler which receives mouse events.
	// It is called by the Application class.
	//
	// A value of nil may also be returned to stop the downward propagation of
	// mouse events.
	//
	// The Box class provides functionality to intercept mouse events. If you
	// subclass from Box, it is recommended that you wrap your handler using
	// Box.WrapMouseHandler() so you inherit that functionality.
	MouseHandler() func(action MouseAction, event *tcell.EventMouse, setFocus func(p Primitive)) (consumed bool, capture Primitive)

	// PasteHandler returns a handler which receives pasted text.
	// It is called by the Application class.
	//
	// A value of nil may also be returned to stop the downward propagation of
	// paste events.
	//
	// The Box class may provide functionality to intercept paste events in the
	// future. If you subclass from Box, it is recommended that you wrap your
	// handler using Box.WrapPasteHandler() so you inherit that functionality.
	PasteHandler() func(text string, setFocus func(p Primitive))

	// IsDirty returns true if this primitive needs to be redrawn.
	IsDirty() bool

	// MarkDirty marks this primitive as needing a redraw.
	MarkDirty()

	// MarkClean marks this primitive as not needing redraw.
	MarkClean()
}
