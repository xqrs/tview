package tview

import (
	"github.com/gdamore/tcell/v3"
	"github.com/rivo/uniseg"
)

// ScrollListItem represents a primitive which can be measured for a given width.
//
// Scroll list items are responsible for reporting their own height so the list can
// layout and scroll variable-height items.
type ScrollListItem interface {
	Primitive
	Height(width int) int
}

// ScrollListBuilder returns a list item for the given index and cursor position.
// It must return nil when the index is out of range.
type ScrollListBuilder func(index int, cursor int) ScrollListItem

// ScrollList displays a virtual list of primitives returned by a builder function.
type ScrollList struct {
	*Box

	Builder     ScrollListBuilder
	gap         int
	snapToItems bool
	trackEnd    bool
	atEnd       bool

	cursor int
	scroll scrollListState

	changed func(index int)

	lastDraw []scrollListDrawnItem
	lastRect scrollListRect
}

type scrollListState struct {
	// Index of the top item in the viewport.
	top int
	// Line offset into the top item; negative values mean the item is scrolled up.
	offset int
	// Pending scroll delta in lines to apply on the next draw.
	pending int
	// Ensure the cursor is visible on the next draw.
	wantsCursor bool
}

type scrollListDrawnItem struct {
	index  int
	item   ScrollListItem
	row    int
	height int
}

type scrollListRect struct {
	x      int
	y      int
	width  int
	height int
}

// NewScrollList returns a new scroll list.
func NewScrollList() *ScrollList {
	return &ScrollList{
		Box:    NewBox(),
		cursor: -1,
	}
}

// SetBuilder sets the builder used to create list items on demand.
func (l *ScrollList) SetBuilder(builder ScrollListBuilder) *ScrollList {
	l.Builder = builder
	return l
}

// Clear removes all items from the list by clearing the builder and resetting
// scroll state.
func (l *ScrollList) Clear() *ScrollList {
	l.Builder = nil
	l.cursor = -1
	l.scroll = scrollListState{}
	l.lastDraw = nil
	l.lastRect = scrollListRect{}
	l.atEnd = false
	return l
}

// SetGap sets the number of blank rows between items.
func (l *ScrollList) SetGap(gap int) *ScrollList {
	if gap < 0 {
		gap = 0
	}
	l.gap = gap
	return l
}

// SetSnapToItems toggles snapping so only fully visible items are shown.
func (l *ScrollList) SetSnapToItems(snap bool) *ScrollList {
	l.snapToItems = snap
	return l
}

// SetTrackEnd toggles auto-scrolling when the view is already at the end.
func (l *ScrollList) SetTrackEnd(track bool) *ScrollList {
	l.trackEnd = track
	return l
}

// ScrollToStart resets the scroll position to the top (index 0), without
// changing the cursor.
func (l *ScrollList) ScrollToStart() *ScrollList {
	l.scroll.top = 0
	l.scroll.offset = 0
	l.scroll.wantsCursor = false
	l.atEnd = false
	return l
}

// ScrollToEnd scrolls the view so the last items are visible.
func (l *ScrollList) ScrollToEnd() *ScrollList {
	_, _, width, height := l.GetInnerRect()
	if width <= 0 || height <= 0 {
		return l
	}
	l.scroll.top, l.scroll.offset = l.endScrollState(width, height)
	l.scroll.wantsCursor = false
	l.atEnd = true
	return l
}

// SetCursor sets the currently selected item index.
func (l *ScrollList) SetCursor(index int) *ScrollList {
	if index < -1 {
		index = -1
	}
	if l.cursor != index {
		l.cursor = index
		l.atEnd = false
		l.ensureScroll()
		if l.changed != nil {
			l.changed(l.cursor)
		}
	}
	return l
}

// Cursor returns the current cursor index.
func (l *ScrollList) Cursor() int {
	return l.cursor
}

// SetPendingScroll sets a pending scroll amount, in lines. Positive numbers
// scroll down.
func (l *ScrollList) SetPendingScroll(lines int) *ScrollList {
	l.scroll.pending = lines
	return l
}

// ScrollUp scrolls the list up by one line.
func (l *ScrollList) ScrollUp() *ScrollList {
	l.scroll.pending -= 1
	return l
}

// ScrollDown scrolls the list down by one line.
func (l *ScrollList) ScrollDown() *ScrollList {
	l.scroll.pending += 1
	return l
}

// NextItem moves the cursor to the next item, if any.
func (l *ScrollList) NextItem() bool {
	if l.Builder == nil {
		return false
	}
	if l.cursor < 0 {
		if l.Builder(0, l.cursor) == nil {
			return false
		}
		l.cursor = 0
		l.ensureScroll()
		if l.changed != nil {
			l.changed(l.cursor)
		}
		return true
	}
	if l.Builder(l.cursor+1, l.cursor) == nil {
		return false
	}
	l.cursor++
	l.ensureScroll()
	if l.changed != nil {
		l.changed(l.cursor)
	}
	return true
}

// PrevItem moves the cursor to the previous item, if any.
func (l *ScrollList) PrevItem() bool {
	if l.cursor <= 0 {
		return false
	}
	if l.Builder == nil {
		return false
	}
	if l.Builder(l.cursor-1, l.cursor) == nil {
		return false
	}
	l.cursor--
	l.ensureScroll()
	if l.changed != nil {
		l.changed(l.cursor)
	}
	return true
}

// SetChangedFunc sets a handler that is called when the cursor changes.
func (l *ScrollList) SetChangedFunc(handler func(index int)) *ScrollList {
	l.changed = handler
	return l
}

// Draw draws this primitive onto the screen.
func (l *ScrollList) Draw(screen tcell.Screen) {
	l.DrawForSubclass(screen, l)

	x, y, width, height := l.GetInnerRect()
	if width <= 0 || height <= 0 || l.Builder == nil {
		return
	}

	usableWidth := width
	if usableWidth <= 0 {
		return
	}

	// If we were already at the end, keep following new items without
	// forcing full scans during normal scrolling.
	if l.trackEnd && l.atEnd {
		l.scroll.top, l.scroll.offset = l.endScrollState(usableWidth, height)
		l.scroll.wantsCursor = false
	}

	// In snap mode, ensure the cursor item is within the fully visible window.
	if l.snapToItems && l.scroll.wantsCursor && l.cursor >= 0 {
		visible := l.visibleItemCount(usableWidth, height)
		if l.cursor < l.scroll.top || l.cursor >= l.scroll.top+visible {
			l.scroll.top = l.cursor
			l.scroll.offset = 0
		}
		l.scroll.wantsCursor = false
	}

	pendingDelta := l.scroll.pending
	ah := -(l.scroll.offset + pendingDelta)
	l.scroll.pending = 0

	if ah > 0 && l.scroll.top == 0 {
		ah = 0
		l.scroll.offset = 0
	}

rebuild:
	// Rebuild the viewport whenever we change top/offset to keep the cursor in view.
	children := make([]scrollListDrawnItem, 0, 16)
	startIndex := l.scroll.top

	if ah > 0 {
		// We scrolled upward into the previous top item; prepend enough items above.
		l.insertChildren(&children, usableWidth, ah)
		if len(children) > 0 {
			last := children[len(children)-1]
			ah = last.row + last.height + l.gap
		}
	}

	endReached := false
	for i := startIndex; ; i++ {
		item := l.Builder(i, l.cursor)
		if item == nil {
			endReached = true
			break
		}

		itemHeight := l.itemHeight(item, usableWidth)
		children = append(children, scrollListDrawnItem{
			index:  i,
			item:   item,
			row:    ah,
			height: itemHeight,
		})
		ah += itemHeight + l.gap

		if l.scroll.wantsCursor && i <= l.cursor {
			continue
		}
		if ah >= height {
			break
		}
	}

	if len(children) == 0 {
		l.scroll.top = 0
		l.scroll.offset = 0
		l.lastDraw = nil
		l.lastRect = scrollListRect{x: x, y: y, width: width, height: height}
		l.atEnd = false
		return
	}

	// If the cursor item didn't make it into the built slice, restart from it.
	if l.snapToItems && l.scroll.wantsCursor && l.cursor >= 0 {
		found := false
		for _, child := range children {
			if child.index == l.cursor {
				found = true
				break
			}
		}
		if !found {
			l.scroll.top = l.cursor
			l.scroll.offset = 0
			l.scroll.wantsCursor = false
			goto rebuild
		}
	}

	if l.snapToItems {
		// Drop partial items so only fully visible ones remain.
		children = l.trimToFullItems(children, height)
		if len(children) == 0 {
			l.scroll.top = 0
			l.scroll.offset = 0
			l.lastDraw = nil
			l.lastRect = scrollListRect{x: x, y: y, width: width, height: height}
			l.atEnd = false
			return
		}

		// Fill remaining space with fully visible items if possible.
		nextIndex := children[len(children)-1].index + 1
		currentBottom := children[len(children)-1].row + children[len(children)-1].height
		for {
			item := l.Builder(nextIndex, l.cursor)
			if item == nil {
				break
			}
			itemHeight := l.itemHeight(item, usableWidth)
			nextRow := currentBottom + l.gap
			if nextRow+itemHeight > height {
				break
			}
			children = append(children, scrollListDrawnItem{
				index:  nextIndex,
				item:   item,
				row:    nextRow,
				height: itemHeight,
			})
			currentBottom = nextRow + itemHeight
			nextIndex++
		}
	}

	// When scrolling down at the end, clamp so the last item aligns to the bottom.
	if endReached && pendingDelta > 0 {
		last := children[len(children)-1]
		bottom := last.row + last.height
		if children[0].row < 0 && bottom < height {
			adj := height - bottom
			for i := range children {
				children[i].row += adj
			}
		}
	}

	// Non-snap mode: adjust rows so the cursor item is fully visible.
	if l.scroll.wantsCursor {
		for _, child := range children {
			if child.index != l.cursor {
				continue
			}
			bottom := child.row + child.height
			if bottom > height {
				adj := height - bottom
				for i := range children {
					children[i].row += adj
				}
			}
			l.scroll.wantsCursor = false
			break
		}
	}

	if l.snapToItems {
		// Snap mode uses the first item as the top anchor.
		l.scroll.top = children[0].index
		l.scroll.offset = 0
	} else {
		// Non-snap mode keeps the first partially visible item as the top anchor.
		for i := range children {
			child := children[i]
			span := child.height
			if l.gap > 0 {
				span += l.gap
			}
			if child.row <= 0 && child.row+span > 0 {
				l.scroll.top = child.index
				l.scroll.offset = -child.row
				break
			}
		}
	}

	last := children[len(children)-1]
	if !endReached && l.Builder(last.index+1, l.cursor) == nil {
		endReached = true
	}
	l.atEnd = endReached && last.row+last.height <= height

	l.lastDraw = children
	l.lastRect = scrollListRect{x: x, y: y, width: width, height: height}

	clipped := newClippedScreen(screen, x, y, width, height)
	for _, child := range children {
		child.item.SetRect(x, y+child.row, usableWidth, child.height)
		child.item.Draw(clipped)
	}
}

func (l *ScrollList) itemHeight(item ScrollListItem, width int) int {
	if item == nil {
		return 0
	}
	height := max(item.Height(width), 1)
	return height
}

func (l *ScrollList) insertChildren(children *[]scrollListDrawnItem, width int, ah int) {
	if l.scroll.top <= 0 {
		return
	}

	l.scroll.top--
	for ah > 0 {
		// Account for the gap between the inserted item and the current top.
		if l.gap > 0 {
			ah -= l.gap
		}
		item := l.Builder(l.scroll.top, l.cursor)
		if item == nil {
			break
		}
		height := l.itemHeight(item, width)
		ah -= height
		entry := scrollListDrawnItem{
			index:  l.scroll.top,
			item:   item,
			row:    ah,
			height: height,
		}
		*children = append([]scrollListDrawnItem{entry}, *children...)

		if l.scroll.top == 0 {
			break
		}
		l.scroll.top--
	}

	l.scroll.offset = ah

	if l.scroll.top == 0 && ah > 0 {
		// We hit the absolute top; normalize rows to avoid overscrolling.
		l.scroll.offset = 0
		row := 0
		for i := range *children {
			child := (*children)[i]
			child.row = row
			(*children)[i] = child
			row += child.height + l.gap
		}
	}
}

func (l *ScrollList) ensureScroll() {
	if l.cursor < 0 {
		l.scroll.wantsCursor = false
		return
	}
	if l.cursor > l.scroll.top {
		l.scroll.wantsCursor = true
		return
	}
	l.scroll.top = l.cursor
	l.scroll.offset = 0
}

func (l *ScrollList) scrollByItems(delta int, count int, width int, height int) {
	if l.Builder == nil {
		return
	}
	if count < 1 {
		count = 1
	}
	if delta > 0 {
		// Step the top index downward without going past the end.
		for i := 0; i < count; i++ {
			if l.Builder(l.scroll.top+1, l.cursor) == nil {
				break
			}
			l.scroll.top++
		}
	} else {
		// Step the top index upward without going below zero.
		for i := 0; i < count; i++ {
			if l.scroll.top <= 0 {
				break
			}
			l.scroll.top--
		}
	}
	l.scroll.offset = 0
	l.scroll.wantsCursor = false
	l.lastDraw = nil
	l.lastRect = scrollListRect{x: 0, y: 0, width: width, height: height}
}

func (l *ScrollList) visibleItemCount(width int, height int) int {
	if l.Builder == nil || width <= 0 || height <= 0 {
		return 0
	}
	total := 0
	count := 0
	for idx := l.scroll.top; ; idx++ {
		item := l.Builder(idx, l.cursor)
		if item == nil {
			break
		}
		if count > 0 {
			total += l.gap
		}
		itemHeight := l.itemHeight(item, width)
		if total+itemHeight > height {
			break
		}
		total += itemHeight
		count++
	}
	// Always move at least one item so navigation feels responsive.
	if count == 0 {
		return 1
	}
	return count
}

func (l *ScrollList) endScrollState(width int, height int) (int, int) {
	if l.Builder == nil || width <= 0 || height <= 0 {
		return 0, 0
	}
	start := max(l.scroll.top, 0)
	// If the current top is past the end, restart from the beginning.
	if l.Builder(start, l.cursor) == nil && start != 0 {
		start = 0
	}
	last := start
	for {
		if l.Builder(last, l.cursor) == nil {
			last--
			break
		}
		last++
	}
	if last < 0 {
		return 0, 0
	}

	// Walk upward from the last item until we fill a viewport.
	total := 0
	for i := last; i >= 0; i-- {
		item := l.Builder(i, l.cursor)
		if item == nil {
			continue
		}
		if total > 0 {
			total += l.gap
		}
		itemHeight := l.itemHeight(item, width)
		if total+itemHeight > height {
			offset := max(total+itemHeight-height, 0)
			return i, offset
		}
		total += itemHeight
		if i == 0 {
			break
		}
	}
	return 0, 0
}

// InputHandler returns the handler for this primitive.
func (l *ScrollList) InputHandler() func(event *tcell.EventKey, setFocus func(p Primitive)) {
	return l.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p Primitive)) {
		switch event.Key() {
		case tcell.KeyDown:
			l.NextItem()
		case tcell.KeyUp:
			l.PrevItem()
		case tcell.KeyPgDn:
			_, _, width, height := l.GetInnerRect()
			if l.snapToItems {
				l.scrollByItems(1, l.visibleItemCount(width, height), width, height)
			} else {
				if height < 1 {
					height = 1
				}
				l.scroll.pending += height
			}
		case tcell.KeyPgUp:
			_, _, width, height := l.GetInnerRect()
			if l.snapToItems {
				l.scrollByItems(-1, l.visibleItemCount(width, height), width, height)
			} else {
				if height < 1 {
					height = 1
				}
				l.scroll.pending -= height
			}
		}
	})
}

// MouseHandler returns the mouse handler for this primitive.
func (l *ScrollList) MouseHandler() func(action MouseAction, event *tcell.EventMouse, setFocus func(p Primitive)) (consumed bool, capture Primitive) {
	return l.WrapMouseHandler(func(action MouseAction, event *tcell.EventMouse, setFocus func(p Primitive)) (consumed bool, capture Primitive) {
		x, y := event.Position()
		if !l.InRect(x, y) {
			return false, nil
		}

		switch action {
		case MouseLeftClick:
			setFocus(l)
			index := l.indexAtPoint(x, y)
			if index >= 0 {
				previous := l.cursor
				l.cursor = index
				l.ensureScroll()
				if l.changed != nil && l.cursor != previous {
					l.changed(l.cursor)
				}
			}
			return true, nil
		case MouseScrollUp:
			_, _, width, height := l.GetInnerRect()
			if l.snapToItems {
				l.scrollByItems(-1, 1, width, height)
			} else {
				l.scroll.pending -= 3
			}
			return true, nil
		case MouseScrollDown:
			_, _, width, height := l.GetInnerRect()
			if l.snapToItems {
				l.scrollByItems(1, 1, width, height)
			} else {
				l.scroll.pending += 3
			}
			return true, nil
		}

		return false, nil
	})
}

func (l *ScrollList) indexAtPoint(x, y int) int {
	if len(l.lastDraw) == 0 {
		return -1
	}
	if x < l.lastRect.x || x >= l.lastRect.x+l.lastRect.width || y < l.lastRect.y || y >= l.lastRect.y+l.lastRect.height {
		return -1
	}

	row := y - l.lastRect.y
	for _, child := range l.lastDraw {
		span := child.height
		if l.gap > 0 {
			span += l.gap
		}
		if row >= child.row && row < child.row+span {
			return child.index
		}
	}
	return -1
}

var _ Primitive = &ScrollList{}

type clippedScreen struct {
	tcell.Screen
	x      int
	y      int
	width  int
	height int
}

func newClippedScreen(screen tcell.Screen, x, y, width, height int) *clippedScreen {
	return &clippedScreen{
		Screen: screen,
		x:      x,
		y:      y,
		width:  width,
		height: height,
	}
}

func (s *clippedScreen) inBounds(x, y int) bool {
	return x >= s.x && x < s.x+s.width && y >= s.y && y < s.y+s.height
}

func (s *clippedScreen) SetContent(x int, y int, primary rune, combining []rune, style tcell.Style) {
	if !s.inBounds(x, y) {
		return
	}
	s.Screen.SetContent(x, y, primary, combining, style)
}

func (s *clippedScreen) Put(x int, y int, str string, style tcell.Style) (string, int) {
	if !s.inBounds(x, y) {
		return str, 0
	}
	return s.Screen.Put(x, y, str, style)
}

func (s *clippedScreen) PutStr(x int, y int, str string) {
	s.PutStrStyled(x, y, str, tcell.StyleDefault)
}

func (s *clippedScreen) PutStrStyled(x int, y int, str string, style tcell.Style) {
	if y < s.y || y >= s.y+s.height {
		return
	}

	gr := uniseg.NewGraphemes(str)
	for gr.Next() {
		cluster := gr.Str()
		width := max(uniseg.StringWidth(cluster), 1)
		if x >= s.x+s.width {
			return
		}
		if x >= s.x && x+width <= s.x+s.width {
			s.Screen.Put(x, y, cluster, style)
		}
		x += width
	}
}

func (s *clippedScreen) ShowCursor(x int, y int) {
	if !s.inBounds(x, y) {
		s.Screen.ShowCursor(-1, -1)
		return
	}
	s.Screen.ShowCursor(x, y)
}

func (l *ScrollList) trimToFullItems(children []scrollListDrawnItem, height int) []scrollListDrawnItem {
	if len(children) == 0 {
		return children
	}

	// Drop any items that start above the viewport.
	start := 0
	for start < len(children) && children[start].row < 0 {
		start++
	}
	if start > 0 {
		children = children[start:]
	}
	if len(children) == 0 {
		return children
	}

	// Realign the first item to row 0 so we can fill below it.
	shift := -children[0].row
	if shift != 0 {
		for i := range children {
			children[i].row += shift
		}
	}

	// Trim trailing items that don't fully fit.
	end := len(children)
	for end > 0 && children[end-1].row+children[end-1].height > height {
		end--
	}
	children = children[:end]

	return children
}
