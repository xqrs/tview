package tview

import (
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v3"
)

const (
	// The size of the event/update/redraw channels.
	queueSize = 100

	// The minimum time between two consecutive redraws.
	redrawPause = 50 * time.Millisecond
)

// DoubleClickInterval specifies the maximum time between clicks to register a
// double click rather than click.
var DoubleClickInterval = 500 * time.Millisecond

// MouseAction indicates one of the actions the mouse is logically doing.
type MouseAction int16

// Available mouse actions.
const (
	MouseMove MouseAction = iota
	MouseLeftDown
	MouseLeftUp
	MouseLeftClick
	MouseLeftDoubleClick
	MouseMiddleDown
	MouseMiddleUp
	MouseMiddleClick
	MouseMiddleDoubleClick
	MouseRightDown
	MouseRightUp
	MouseRightClick
	MouseRightDoubleClick
	MouseScrollUp
	MouseScrollDown
	MouseScrollLeft
	MouseScrollRight

	// The following special value will not be provided as a mouse action but
	// indicate that an overridden mouse event was consumed. See
	// [Box.SetMouseCapture] for details.
	MouseConsumed
)

// queuedUpdate represented the execution of f queued by
// Application.QueueUpdate(). If "done" is not nil, it receives exactly one
// element after f has executed.
type queuedUpdate struct {
	f    func()
	done chan struct{}
}

// Application represents the top node of an application.
//
// It is not strictly required to use this class as none of the other classes
// depend on it. However, it provides useful tools to set up an application and
// plays nicely with all widgets.
//
// The following command displays a primitive p on the screen until Ctrl-C is
// pressed:
//
//	if err := tview.NewApplication().SetRoot(p, true).Run(); err != nil {
//	    panic(err)
//	}
type Application struct {
	sync.RWMutex

	// The application's screen. Apart from Run(), this variable should never be
	// set directly. Always use the screenReplacement channel after calling
	// Fini(), to set a new screen (or nil to stop the application).
	screen tcell.Screen

	// The primitive which currently has the keyboard focus.
	focus Primitive

	// The root primitive to be seen on the screen.
	root Primitive

	// An optional capture function which receives a key event and returns the
	// event to be forwarded to the default input handler (nil if nothing should
	// be forwarded).
	inputCapture func(event *tcell.EventKey) *tcell.EventKey

	events chan tcell.Event

	// Functions queued from goroutines, used to serialize updates to primitives.
	updates chan queuedUpdate

	// An optional capture function which receives a mouse event and returns the
	// event to be forwarded to the default mouse handler (nil if nothing should
	// be forwarded).
	mouseCapture func(event *tcell.EventMouse, action MouseAction) (*tcell.EventMouse, MouseAction)

	mouseCapturingPrimitive Primitive        // A Primitive returned by a MouseHandler which will capture future mouse events.
	lastMouseX, lastMouseY  int              // The last position of the mouse.
	mouseDownX, mouseDownY  int              // The position of the mouse when its button was last pressed.
	lastMouseClick          time.Time        // The time when a mouse button was last clicked.
	lastMouseButtons        tcell.ButtonMask // The last mouse button state.

	// frontFrame is the last frame flushed to the terminal, while backFrame
	// captures the frame currently being rendered.
	frontFrame *frame
	backFrame  *frame

	// forceRedraw bypasses row-span diffing for the next frame.
	forceRedraw bool

	// spaces caches a full-width run of blanks for clear-run batching.
	spaces string
}

// NewApplication creates and returns a new application.
func NewApplication() *Application {
	return &Application{
		events:  make(chan tcell.Event, queueSize),
		updates: make(chan queuedUpdate, queueSize),
	}
}

// SetInputCapture sets a function which captures all key events before they are
// forwarded to the key event handler of the primitive which currently has
// focus. This function can then choose to forward that key event (or a
// different one) by returning it or stop the key event processing by returning
// nil.
//
// The only default global key event is Ctrl-C which stops the application. It
// requires special handling:
//
//   - If you do not wish to change the default behavior, return the original
//     event object passed to your input capture function.
//   - If you wish to block Ctrl-C from any functionality, return nil.
//   - If you do not wish Ctrl-C to stop the application but still want to
//     forward the Ctrl-C event to primitives down the hierarchy, return a new
//     key event with the same key and modifiers, e.g.
//     tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone).
func (a *Application) SetInputCapture(capture func(event *tcell.EventKey) *tcell.EventKey) *Application {
	a.inputCapture = capture
	return a
}

// GetInputCapture returns the function installed with SetInputCapture() or nil
// if no such function has been installed.
func (a *Application) GetInputCapture() func(event *tcell.EventKey) *tcell.EventKey {
	return a.inputCapture
}

// SetMouseCapture sets a function which captures mouse events (consisting of
// the original tcell mouse event and the semantic mouse action) before they are
// forwarded to the appropriate mouse event handler. This function can then
// choose to forward that event (or a different one) by returning it or stop
// the event processing by returning a nil mouse event. In such a case, the
// event is considered consumed and the screen will be redrawn.
func (a *Application) SetMouseCapture(capture func(event *tcell.EventMouse, action MouseAction) (*tcell.EventMouse, MouseAction)) *Application {
	a.mouseCapture = capture
	return a
}

// GetMouseCapture returns the function installed with SetMouseCapture() or nil
// if no such function has been installed.
func (a *Application) GetMouseCapture() func(event *tcell.EventMouse, action MouseAction) (*tcell.EventMouse, MouseAction) {
	return a.mouseCapture
}

// SetScreen sets the application's screen.
func (a *Application) SetScreen(screen tcell.Screen) *Application {
	a.Lock()
	defer a.Unlock()
	if a.screen == nil {
		a.screen = screen
		a.frontFrame = nil
		a.backFrame = nil
		a.forceRedraw = true
	}
	return a
}

// Run starts the application and thus the event loop. This function returns
// when [Application.Stop] was called.
//
// Note that while an application is running, it fully claims stdin, stdout, and
// stderr. If you use these standard streams, they may not work as expected.
// Consider stopping the application first or suspending it (using
// [Application.Suspend]) if you have to interact with the standard streams, for
// example when needing to print a call stack during a panic.
func (a *Application) Run() error {
	var (
		appErr      error
		lastRedraw  time.Time   // The time the screen was last redrawn.
		redrawTimer *time.Timer // A timer to schedule the next redraw.
	)
	a.Lock()

	// Make a screen if there is none yet.
	if a.screen == nil {
		screen, err := tcell.NewScreen()
		if err != nil {
			a.Unlock()
			return err
		}
		if err = screen.Init(); err != nil {
			a.Unlock()
			return err
		}
		a.screen = screen
	}

	// We catch panics to clean up because they mess up the terminal.
	defer func() {
		if p := recover(); p != nil {
			a.Stop()
			panic(p)
		}
	}()

	// Draw the screen for the first time.
	a.Unlock()
	a.draw()

	a.RLock()
	screen := a.screen
	a.RUnlock()
	a.events = screen.EventQ()

	// Start event loop.
	var (
		pasteBuffer strings.Builder
		pasting     bool // Set to true while we receive paste key events.
	)
EventLoop:
	for {
		select {
		// If we received an event, handle it.
		case event := <-a.events:
			if event == nil {
				break EventLoop
			}

			switch event := event.(type) {
			case *tcell.EventKey:
				// If we are pasting, collect runes, nothing else.
				if pasting {
					switch event.Key() {
					case tcell.KeyRune:
						pasteBuffer.WriteString(event.Str())
					case tcell.KeyEnter:
						pasteBuffer.WriteRune('\n')
					case tcell.KeyTab:
						pasteBuffer.WriteRune('\t')
					}
					break
				}

				a.RLock()
				root := a.root
				inputCapture := a.inputCapture
				a.RUnlock()

				// Intercept keys.
				var draw bool
				originalEvent := event
				if inputCapture != nil {
					event = inputCapture(event)
					if event == nil {
						a.draw()
						break // Don't forward event.
					}
					draw = true
				}

				// Ctrl-C closes the application.
				if event == originalEvent && event.Key() == tcell.KeyCtrlC {
					a.Stop()
					break
				}

				// Pass other key events to the root primitive.
				if root != nil && root.HasFocus() {
					if handler := root.InputHandler(); handler != nil {
						handler(event, func(p Primitive) {
							a.SetFocus(p)
						})
						draw = true
					}
				}

				// Redraw.
				if draw {
					a.draw()
				}
			case *tcell.EventPaste:
				if event.Start() {
					pasting = true
					pasteBuffer.Reset()
				} else if event.End() {
					pasting = false
					a.RLock()
					root := a.root
					a.RUnlock()
					if root != nil && root.HasFocus() && pasteBuffer.Len() > 0 {
						// Pass paste event to the root primitive.
						if handler := root.PasteHandler(); handler != nil {
							handler(pasteBuffer.String(), func(p Primitive) {
								a.SetFocus(p)
							})
						}

						// Redraw.
						a.draw()
					}
				}
			case *tcell.EventResize:
				if time.Since(lastRedraw) < redrawPause {
					if redrawTimer != nil {
						redrawTimer.Stop()
					}
					redrawTimer = time.AfterFunc(redrawPause, func() {
						a.events <- event
					})
				}
				lastRedraw = time.Now()
				a.draw()
			case *tcell.EventMouse:
				consumed, isMouseDownAction := a.fireMouseActions(event)
				if consumed {
					a.draw()
				}
				a.lastMouseButtons = event.Buttons()
				if isMouseDownAction {
					a.mouseDownX, a.mouseDownY = event.Position()
				}
			case *tcell.EventError:
				appErr = event
				a.Stop()
			}

		// If we have updates, now is the time to execute them.
		case update := <-a.updates:
			update.f()
			if update.done != nil {
				update.done <- struct{}{}
			}
		}
	}

	return appErr
}

// fireMouseActions analyzes the provided mouse event, derives mouse actions
// from it and then forwards them to the corresponding primitives.
func (a *Application) fireMouseActions(event *tcell.EventMouse) (consumed, isMouseDownAction bool) {
	// We want to relay follow-up events to the same target primitive.
	var targetPrimitive Primitive

	// Helper function to fire a mouse action.
	fire := func(action MouseAction) {
		switch action {
		case MouseLeftDown, MouseMiddleDown, MouseRightDown:
			isMouseDownAction = true
		}

		// Intercept event.
		if a.mouseCapture != nil {
			event, action = a.mouseCapture(event, action)
			if event == nil {
				consumed = true
				return // Don't forward event.
			}
		}

		// Determine the target primitive.
		var primitive, capturingPrimitive Primitive
		if a.mouseCapturingPrimitive != nil {
			primitive = a.mouseCapturingPrimitive
			targetPrimitive = a.mouseCapturingPrimitive
		} else if targetPrimitive != nil {
			primitive = targetPrimitive
		} else {
			primitive = a.root
		}
		if primitive != nil {
			if handler := primitive.MouseHandler(); handler != nil {
				var wasConsumed bool
				wasConsumed, capturingPrimitive = handler(action, event, func(p Primitive) {
					a.SetFocus(p)
				})
				if wasConsumed {
					consumed = true
				}
			}
		}
		a.mouseCapturingPrimitive = capturingPrimitive
	}

	x, y := event.Position()
	buttons := event.Buttons()
	clickMoved := x != a.mouseDownX || y != a.mouseDownY
	buttonChanges := buttons ^ a.lastMouseButtons

	if x != a.lastMouseX || y != a.lastMouseY {
		fire(MouseMove)
		a.lastMouseX = x
		a.lastMouseY = y
	}

	for _, buttonEvent := range []struct {
		button                  tcell.ButtonMask
		down, up, click, dclick MouseAction
	}{
		{tcell.ButtonPrimary, MouseLeftDown, MouseLeftUp, MouseLeftClick, MouseLeftDoubleClick},
		{tcell.ButtonMiddle, MouseMiddleDown, MouseMiddleUp, MouseMiddleClick, MouseMiddleDoubleClick},
		{tcell.ButtonSecondary, MouseRightDown, MouseRightUp, MouseRightClick, MouseRightDoubleClick},
	} {
		if buttonChanges&buttonEvent.button != 0 {
			if buttons&buttonEvent.button != 0 {
				fire(buttonEvent.down)
			} else {
				fire(buttonEvent.up) // A user override might set event to nil.
				if !clickMoved && event != nil {
					if a.lastMouseClick.Add(DoubleClickInterval).Before(time.Now()) {
						fire(buttonEvent.click)
						a.lastMouseClick = time.Now()
					} else {
						fire(buttonEvent.dclick)
						a.lastMouseClick = time.Time{} // reset
					}
				}
			}
		}
	}

	for _, wheelEvent := range []struct {
		button tcell.ButtonMask
		action MouseAction
	}{
		{tcell.WheelUp, MouseScrollUp},
		{tcell.WheelDown, MouseScrollDown},
		{tcell.WheelLeft, MouseScrollLeft},
		{tcell.WheelRight, MouseScrollRight}} {
		if buttons&wheelEvent.button != 0 {
			fire(wheelEvent.action)
		}
	}

	return consumed, isMouseDownAction
}

// Stop stops the application, causing Run() to return.
func (a *Application) Stop() {
	a.Lock()
	defer a.Unlock()
	screen := a.screen
	if screen == nil {
		return
	}
	screen.Fini()
	a.screen = nil
}

// Suspend temporarily suspends the application by exiting terminal UI mode and
// invoking the provided function "f". When "f" returns, terminal UI mode is
// entered again and the application resumes.
//
// A return value of true indicates that the application was suspended and "f"
// was called. If false is returned, the application was already suspended,
// terminal UI mode was not exited, and "f" was not called.
func (a *Application) Suspend(f func()) bool {
	a.RLock()
	screen := a.screen
	a.RUnlock()
	if screen == nil {
		return false // Screen has not yet been initialized.
	}

	// Enter suspended mode.
	if err := screen.Suspend(); err != nil {
		return false // Suspension failed.
	}

	// Wait for "f" to return.
	f()

	// If the screen object has changed in the meantime, we need to do more.
	a.RLock()
	defer a.RUnlock()
	if a.screen != screen {
		// Calling Stop() while in suspend mode currently still leads to a
		// panic, see https://github.com/gdamore/tcell/issues/440.
		screen.Fini()
		if a.screen == nil {
			return true // If stop was called (a.screen is nil), we're done already.
		}
	} else {
		// It hasn't changed. Resume.
		screen.Resume() // Not much we can do in case of an error.
	}

	// Continue application loop.
	return true
}

// Draw refreshes the screen (during the next update cycle). It calls the Draw()
// function of the application's root primitive and then syncs the screen
// buffer. It is almost never necessary to call this function. It can actually
// deadlock your application if you call it from the main thread (e.g. in a
// callback function of a widget). Please see
// https://github.com/ayn2op/tview/wiki/Concurrency for details.
func (a *Application) Draw() *Application {
	a.QueueUpdate(func() {
		a.draw()
	})
	return a
}

// ForceDraw refreshes the screen immediately. Use this function with caution as
// it may lead to race conditions with updates to primitives in other
// goroutines. It is always preferable to call [Application.Draw] instead.
// Never call this function from a goroutine.
//
// It is safe to call this function during queued updates and direct event
// handling.
func (a *Application) ForceDraw() *Application {
	return a.draw()
}

// draw actually does what Draw() promises to do.
func (a *Application) draw() *Application {
	a.Lock()
	screen := a.screen
	root := a.root
	drawWidth, drawHeight := 0, 0
	if screen != nil {
		drawWidth, drawHeight = screen.Size()
		a.ensureDiffFrames(drawWidth, drawHeight)
	}
	front := a.frontFrame
	back := a.backFrame
	forceRedraw := a.forceRedraw
	spaces := a.spaces
	a.Unlock()

	// Maybe we're not ready yet or not anymore.
	if screen == nil || root == nil {
		return a
	}

	root.SetRect(0, 0, drawWidth, drawHeight)

	back.beginFrame()
	root.Draw(&captureScreen{Screen: screen, frame: back})
	if a.blitDiff(screen, front, back, forceRedraw, spaces) {
		screen.Show()
	}

	a.Lock()
	a.frontFrame, a.backFrame = a.backFrame, a.frontFrame
	a.forceRedraw = false
	a.Unlock()

	return a
}

// Sync forces a full re-sync of the screen buffer with the actual screen during
// the next event cycle. This is useful for when the terminal screen is
// corrupted so you may want to offer your users a keyboard shortcut to refresh
// the screen.
func (a *Application) Sync() *Application {
	a.updates <- queuedUpdate{f: func() {
		a.Lock()
		screen := a.screen
		a.forceRedraw = true
		a.Unlock()
		if screen == nil {
			return
		}
		screen.Sync()
	}}
	return a
}

// SetRoot sets the root primitive for this application. This function must be called at least once or nothing will be displayed when
// the application starts.
//
// It also calls SetFocus() on the primitive.
func (a *Application) SetRoot(root Primitive) *Application {
	a.Lock()
	a.root = root
	if a.screen != nil {
		a.frontFrame = nil
		a.backFrame = nil
		a.forceRedraw = true
	}
	a.Unlock()

	a.SetFocus(root)
	return a
}

// SetFocus sets the focus to a new primitive. All key events will be directed
// down the hierarchy (starting at the root) until a primitive handles them,
// which per default goes towards the focused primitive.
//
// Blur() will be called on the previously focused primitive. Focus() will be
// called on the new primitive.
func (a *Application) SetFocus(p Primitive) *Application {
	a.Lock()
	if a.focus != nil {
		a.focus.Blur()
	}
	a.focus = p
	if a.screen != nil {
		a.screen.HideCursor()
	}
	a.Unlock()
	if p != nil {
		p.Focus(func(p Primitive) {
			a.SetFocus(p)
		})
	}

	return a
}

// GetFocus returns the primitive which has the current focus. If none has it,
// nil is returned.
func (a *Application) GetFocus() Primitive {
	a.RLock()
	defer a.RUnlock()
	return a.focus
}

// QueueUpdate is used to synchronize access to primitives from non-main
// goroutines. The provided function will be executed as part of the event loop
// and thus will not cause race conditions with other such update functions or
// the Draw() function.
//
// Note that Draw() is not implicitly called after the execution of f as that
// may not be desirable. You can call Draw() from f if the screen should be
// refreshed after each update. Alternatively, use QueueUpdateDraw() to follow
// up with an immediate refresh of the screen.
//
// This function returns after f has executed.
func (a *Application) QueueUpdate(f func()) *Application {
	ch := make(chan struct{})
	a.updates <- queuedUpdate{f: f, done: ch}
	<-ch
	return a
}

// QueueUpdateDraw works like QueueUpdate() except it refreshes the screen
// immediately after executing f.
func (a *Application) QueueUpdateDraw(f func()) *Application {
	a.QueueUpdate(func() {
		f()
		a.draw()
	})
	return a
}

// QueueEvent sends an event to the Application event loop.
//
// It is not recommended for event to be nil.
func (a *Application) QueueEvent(event tcell.Event) *Application {
	a.events <- event
	return a
}

func (a *Application) ensureDiffFrames(width, height int) {
	if a.frontFrame == nil || a.backFrame == nil {
		a.frontFrame = newFrame(width, height)
		a.backFrame = newFrame(width, height)
		a.ensureSpaces(width)
		// Seed both generations so first diff sees "empty previous frame"
		// consistently without special-case checks in the blitter.
		a.frontFrame.beginFrame()
		a.backFrame.beginFrame()
		a.forceRedraw = true
		return
	}
	if a.frontFrame.width == width && a.frontFrame.height == height &&
		a.backFrame.width == width && a.backFrame.height == height {
		return
	}
	a.frontFrame.resize(width, height)
	a.backFrame.resize(width, height)
	a.ensureSpaces(width)
	a.frontFrame.beginFrame()
	a.backFrame.beginFrame()
	a.forceRedraw = true
}

func (a *Application) ensureSpaces(width int) {
	if width <= 0 {
		a.spaces = ""
		return
	}
	if len(a.spaces) >= width {
		return
	}
	a.spaces = strings.Repeat(" ", width)
}

// blitDiff writes only changed cells from back -> screen, using front as the
// previous flushed baseline.
//
// Rows are selected by dirty-line metadata tracked while primitives draw.
// For non-forced redraws, we only visit rows touched in either frame and use
// the union of their dirty spans.
func (a *Application) blitDiff(screen tcell.Screen, front, back *frame, forceRedraw bool, spaces string) bool {
	if front == nil || back == nil {
		return false
	}

	width, height := back.width, back.height
	changed := false

	// If the draw pass called Clear(), clear the terminal once and then repaint
	// only rows touched afterward in this frame.
	if back.clearAll {
		screen.Clear()
		changed = true
		for y := range height {
			start, end, ok := back.rowSpan(y)
			if !ok || start >= end {
				continue
			}
			base := y * width
			bRow := back.cells[base : base+width]
			if a.paintBackRowRangeNoCompare(screen, y, start, end, bRow, back.gen, spaces) {
				changed = true
			}
		}
		return changed
	}

	// Forced redraws (e.g. Sync/resize/root swaps) repaint the entire viewport
	// directly from back without consulting front. This avoids spending CPU on
	// equality checks when we already know a full repaint is required.
	if forceRedraw {
		// Clear first for conservative correctness across resize/root swaps and
		// complex glyph transitions before repainting every viewport cell.
		screen.Clear()
		changed = true
		for y := range height {
			base := y * width
			bRow := back.cells[base : base+width]
			if a.paintBackRowRangeNoCompare(screen, y, 0, width, bRow, back.gen, spaces) {
				changed = true
			}
		}
		return changed
	}

	for y := range height {
		var start, end int
		backStart, backEnd, backOK := back.rowSpan(y)
		frontStart, frontEnd, frontOK := front.rowSpan(y)
		switch {
		case backOK && frontOK:
			// Use the union of old/new dirty windows so deletions and insertions
			// on either side are both considered.
			start = min(backStart, frontStart)
			end = max(backEnd, frontEnd)
		case backOK:
			start, end = backStart, backEnd
		case frontOK:
			start, end = frontStart, frontEnd
		default:
			continue
		}
		if start >= end {
			continue
		}

		base := y * width
		fRow := front.cells[base : base+width]
		bRow := back.cells[base : base+width]
		// Ncurses-style row hashing short-circuits dirty windows that were
		// touched but ended up visually unchanged.
		if rowRangeSignature(fRow, front.gen, start, end) == rowRangeSignature(bRow, back.gen, start, end) {
			continue
		}
		// Walk the candidate dirty window and emit only changed runs.
		for x := start; x < end; {
			frontCell := fRow[x]
			frontOK := frontCell.gen == front.gen
			backCell := bRow[x]
			backOK := backCell.gen == back.gen

			// Cheap-state fast path: both missing means unchanged blank.
			if !frontOK && !backOK {
				x++
				continue
			}
			if backOK && backCell.cont {
				// Continuation columns are metadata-only and should be covered by
				// a lead wide grapheme. If metadata is orphaned, clear this cell
				// defensively so stale right-half artifacts cannot survive.
				if !isContinuationCoveredByLead(bRow, back.gen, x) {
					screen.PutStrStyled(x, y, spaces[:1], tcell.StyleDefault)
					changed = true
				}
				x++
				continue
			}
			// Keep cellsEqual authoritative for semantic equality.
			if cellsEqual(frontCell, frontOK, backCell, backOK) {
				// Dirty spans can still contain unchanged cells; skip them so we
				// only emit terminal writes for actual visual deltas.
				x++
				continue
			}

			cell := bRow[x]
			if cell.gen != back.gen {
				runStart := x
				for x < end {
					fc := fRow[x]
					fok := fc.gen == front.gen
					bc := bRow[x]
					bok := bc.gen == back.gen
					// Stop clear-run immediately when new content starts.
					if bok {
						break
					}
					if !fok {
						// Both sides are logically blank; keep extending clear-run.
						x++
						continue
					}
					if cellsEqual(fc, fok, bc, bok) {
						break
					}
					x++
				}
				// This range changed from previous content to logical blanks.
				screen.PutStrStyled(runStart, y, spaces[:x-runStart], tcell.StyleDefault)
				changed = true
				continue
			}
			// Batch contiguous single-cell graphemes with the same style.
			if cell.dw == 1 {
				style := cell.style
				runStart := x
				var b strings.Builder
				for x < end {
					next := bRow[x]
					fc := fRow[x]
					fok := fc.gen == front.gen
					bc := bRow[x]
					bok := bc.gen == back.gen
					if !bok || next.style != style || next.dw != 1 {
						break
					}
					// For single-width text runs, equality is a simple field check.
					if fok && fc.style == bc.style && fc.dw == bc.dw && fc.text == bc.text {
						break
					}
					b.WriteString(next.text)
					x++
				}
				screen.PutStrStyled(runStart, y, b.String(), style)
				changed = true
				continue
			}

			screen.Put(x, y, cell.text, cell.style)
			changed = true
			x++
		}
	}

	return changed
}

// paintBackRowRangeNoCompare writes [start,end) from back to screen without
// consulting front. It is used by explicit full-repaint modes (clearAll and
// forceRedraw).
func (a *Application) paintBackRowRangeNoCompare(screen tcell.Screen, y, start, end int, bRow []cell, backGen uint32, spaces string) bool {
	wrote := false
	for x := start; x < end; {
		cell := bRow[x]
		if cell.gen != backGen {
			// Missing cells are logical blanks in this generation; batch them
			// into one clear run to avoid per-cell terminal calls.
			runStart := x
			for x < end && bRow[x].gen != backGen {
				x++
			}
			screen.PutStrStyled(runStart, y, spaces[:x-runStart], tcell.StyleDefault)
			wrote = true
			continue
		}
		if cell.cont {
			// Continuation cells do not emit output directly; when metadata is
			// orphaned, clear the cell defensively.
			if !isContinuationCoveredByLead(bRow, backGen, x) {
				screen.PutStrStyled(x, y, spaces[:1], tcell.StyleDefault)
				wrote = true
			}
			x++
			continue
		}
		if cell.dw == 1 {
			// Fast path: batch contiguous single-cell graphemes sharing style.
			style := cell.style
			runStart := x
			var b strings.Builder
			for x < end {
				next := bRow[x]
				if next.gen != backGen || next.style != style || next.dw != 1 {
					break
				}
				b.WriteString(next.text)
				x++
			}
			screen.PutStrStyled(runStart, y, b.String(), style)
			wrote = true
			continue
		}

		// Wide/complex graphemes are emitted one cell at a time.
		screen.Put(x, y, cell.text, cell.style)
		wrote = true
		x++
	}
	return wrote
}

func rowRangeSignature(row []cell, gen uint32, start, end int) uint64 {
	h := uint64(fnv64Offset)
	for x := start; x < end; x++ {
		c := row[x]
		if c.gen != gen {
			h = hashMixUint64(h, 0)
			continue
		}
		h = hashMixUint64(h, c.sig)
	}
	return hashMixUint64(h, uint64(end-start))
}

// isContinuationCoveredByLead validates continuation metadata in O(1) using
// the stored lead column. This keeps the hot path cheap while guarding against
// stale continuation flags.
func isContinuationCoveredByLead(bRow []cell, backGen uint32, x int) bool {
	if x < 0 || x >= len(bRow) {
		return false
	}
	c := bRow[x]
	if c.gen != backGen || !c.cont {
		return false
	}
	leadX := c.leadX
	if leadX < 0 || leadX >= x || leadX >= len(bRow) {
		return false
	}
	lead := bRow[leadX]
	if lead.gen != backGen || lead.cont || lead.dw <= 1 {
		return false
	}
	return leadX+int(lead.dw) > x
}
