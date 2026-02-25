package tview

import (
	"github.com/gdamore/tcell/v3"
)

// Button is labeled box that triggers an action when selected.
//
// See https://github.com/ayn2op/tview/wiki/Button for an example.
type Button struct {
	*Box

	// If set to true, the button cannot be activated.
	disabled bool

	// The text to be displayed inside the button.
	text string

	// The button's style (when deactivated).
	style tcell.Style

	// The button's style (when activated).
	activatedStyle tcell.Style

	// The button's style (when disabled).
	disabledStyle tcell.Style

	// An optional function which is called when the button was selected.
	selected func()

	// An optional function which is called when the user leaves the button. A
	// key is provided indicating which key was pressed to leave (tab or
	// backtab).
	exit func(tcell.Key)
}

// NewButton returns a new input field.
func NewButton(label string) *Button {
	box := NewBox()
	box.SetRect(0, 0, TaggedStringWidth(label)+4, 1)
	return &Button{
		Box:            box,
		text:           label,
		style:          tcell.StyleDefault.Background(Styles.ContrastBackgroundColor).Foreground(Styles.PrimaryTextColor),
		activatedStyle: tcell.StyleDefault.Background(Styles.PrimaryTextColor).Foreground(Styles.InverseTextColor),
		disabledStyle:  tcell.StyleDefault.Background(Styles.ContrastBackgroundColor).Foreground(Styles.ContrastSecondaryTextColor),
	}
}

// SetLabel sets the button text.
func (b *Button) SetLabel(label string) *Button {
	if b.text != label {
		b.text = label
		b.MarkDirty()
	}
	return b
}

// GetLabel returns the button text.
func (b *Button) GetLabel() string {
	return b.text
}

// SetLabelColor sets the color of the button text.
func (b *Button) SetLabelColor(color tcell.Color) *Button {
	style := b.style.Foreground(color)
	if b.style != style {
		b.style = style
		b.MarkDirty()
	}
	return b
}

// SetStyle sets the style of the button used when it is not focused.
func (b *Button) SetStyle(style tcell.Style) *Button {
	if b.style != style {
		b.style = style
		b.MarkDirty()
	}
	return b
}

// SetLabelColorActivated sets the color of the button text when the button is
// in focus.
func (b *Button) SetLabelColorActivated(color tcell.Color) *Button {
	style := b.activatedStyle.Foreground(color)
	if b.activatedStyle != style {
		b.activatedStyle = style
		b.MarkDirty()
	}
	return b
}

// SetBackgroundColorActivated sets the background color of the button text when
// the button is in focus.
func (b *Button) SetBackgroundColorActivated(color tcell.Color) *Button {
	style := b.activatedStyle.Background(color)
	if b.activatedStyle != style {
		b.activatedStyle = style
		b.MarkDirty()
	}
	return b
}

// SetActivatedStyle sets the style of the button used when it is focused.
func (b *Button) SetActivatedStyle(style tcell.Style) *Button {
	if b.activatedStyle != style {
		b.activatedStyle = style
		b.MarkDirty()
	}
	return b
}

// SetDisabledStyle sets the style of the button used when it is disabled.
func (b *Button) SetDisabledStyle(style tcell.Style) *Button {
	if b.disabledStyle != style {
		b.disabledStyle = style
		b.MarkDirty()
	}
	return b
}

// SetDisabled sets whether or not the button is disabled. Disabled buttons
// cannot be activated.
//
// If the button is part of a form, you should set focus to the form itself
// after calling this function to set focus to the next non-disabled form item.
func (b *Button) SetDisabled(disabled bool) *Button {
	if b.disabled != disabled {
		b.disabled = disabled
		b.MarkDirty()
	}
	return b
}

// GetDisabled returns whether or not the button is disabled.
func (b *Button) GetDisabled() bool {
	return b.disabled
}

// SetSelectedFunc sets a handler which is called when the button was selected.
func (b *Button) SetSelectedFunc(handler func()) *Button {
	b.selected = handler
	return b
}

// SetExitFunc sets a handler which is called when the user leaves the button.
// The callback function is provided with the key that was pressed, which is one
// of the following:
//
//   - KeyEscape: Leaving the button with no specific direction.
//   - KeyTab: Move to the next field.
//   - KeyBacktab: Move to the previous field.
func (b *Button) SetExitFunc(handler func(key tcell.Key)) *Button {
	b.exit = handler
	return b
}

// Draw draws this primitive onto the screen.
func (b *Button) Draw(screen tcell.Screen) {
	// Draw the box.
	style := b.style
	if b.disabled {
		style = b.disabledStyle
	}
	if b.HasFocus() && !b.disabled {
		style = b.activatedStyle
	}
	backgroundColor := style.GetBackground()
	b.SetBackgroundColor(backgroundColor)
	b.DrawForSubclass(screen, b)

	// Draw label.
	x, y, width, height := b.GetInnerRect()
	if width > 0 && height > 0 {
		y = y + height/2
		printWithStyle(screen, b.text, x, y, 0, width, AlignmentCenter, style, true)
	}
}

// InputHandler returns the handler for this primitive.
func (b *Button) InputHandler(event *tcell.EventKey, setFocus func(p Primitive)) {
	if b.disabled {
		return
	}

	// Process key event.
	switch key := event.Key(); key {
	case tcell.KeyEnter: // Selected.
		if b.selected != nil {
			b.selected()
		}
	case tcell.KeyBacktab, tcell.KeyTab, tcell.KeyEscape: // Leave. No action.
		if b.exit != nil {
			b.exit(key)
		}
	}
}

// MouseHandler returns the mouse handler for this primitive.
func (b *Button) MouseHandler(action MouseAction, event *tcell.EventMouse, setFocus func(p Primitive)) (consumed bool, capture Primitive) {
	if b.disabled {
		return false, nil
	}

	if !b.InRect(event.Position()) {
		return false, nil
	}

	// Process mouse event.
	switch action {
	case MouseLeftDown:
		setFocus(b)
		consumed = true
	case MouseLeftClick:
		if b.selected != nil {
			b.selected()
		}
		consumed = true
	}

	return
}
