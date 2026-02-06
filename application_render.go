package tview

import (
	"unicode/utf8"

	"github.com/gdamore/tcell/v3"
	"github.com/rivo/uniseg"
)

func normalizeFrameSize(width, height int) (int, int) {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	return width, height
}

type cell struct {
	text  string
	style tcell.Style
	dw    uint8
	cont  bool
	leadX int
	sig   uint64
	gen   uint32
}

// frame stores one logical render frame.
//
// Cells are generation-tagged, so a new frame can start without physically
// clearing the entire backing slice. A cell is considered present only when its
// generation matches the frame generation.
type frame struct {
	width  int
	height int

	gen   uint32
	cells []cell

	rowGen   []uint32
	rowStart []int
	rowEnd   []int

	clearAll bool
}

func newFrame(width, height int) *frame {
	width, height = normalizeFrameSize(width, height)
	return &frame{
		width:    width,
		height:   height,
		cells:    make([]cell, width*height),
		rowGen:   make([]uint32, height),
		rowStart: make([]int, height),
		rowEnd:   make([]int, height),
	}
}

func (f *frame) resize(width, height int) {
	width, height = normalizeFrameSize(width, height)
	if f.width == width && f.height == height {
		return
	}
	f.width, f.height = width, height
	f.cells = make([]cell, width*height)
	f.rowGen = make([]uint32, height)
	f.rowStart = make([]int, height)
	f.rowEnd = make([]int, height)
	f.gen = 0
	f.clearAll = false
}

func (f *frame) beginFrame() {
	f.gen++
	f.clearAll = false
	if f.gen != 0 {
		return
	}
	for i := range f.cells {
		f.cells[i].gen = 0
	}
	for i := range f.rowGen {
		f.rowGen[i] = 0
	}
	f.gen = 1
}

// Clear resets the current logical frame and marks it as a full repaint pass.
// This matches primitive expectations that calling Screen.Clear() removes prior
// content before subsequent SetContent() calls.
func (f *frame) Clear() {
	f.beginFrame()
	f.clearAll = true
}

func (f *frame) markSpan(y, start, end int) {
	if y < 0 || y >= f.height || start >= end {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > f.width {
		end = f.width
	}
	if start >= end {
		return
	}
	if f.rowGen[y] != f.gen {
		f.rowGen[y] = f.gen
		f.rowStart[y] = start
		f.rowEnd[y] = end
		return
	}
	if start < f.rowStart[y] {
		f.rowStart[y] = start
	}
	if end > f.rowEnd[y] {
		f.rowEnd[y] = end
	}
}

func (f *frame) SetContent(x int, y int, primary rune, combining []rune, style tcell.Style) {
	text := string(primary)
	if len(combining) > 0 {
		text += string(combining)
	}
	width := uniseg.StringWidth(text)
	if width <= 0 {
		width = 1
	}
	// Match terminal clipping behavior for wide graphemes at the right edge.
	if width > 1 && x == f.width-1 {
		text = " "
		width = 1
	}

	f.putCellText(x, y, text, style, widthToCellDW(width), false, -1)
	// Mark trailing columns as continuation cells so diffs can reason about the
	// full occupied width of wide graphemes.
	for i := 1; i < width; i++ {
		f.putCellText(x+i, y, "", style, 0, true, x)
	}
}

func widthToCellDW(width int) uint8 {
	if width <= 0 {
		return 1
	}
	if width > 255 {
		return 255
	}
	return uint8(width)
}

const (
	fnv64Offset = 1469598103934665603
	fnv64Prime  = 1099511628211
)

func hashMixUint64(h uint64, v uint64) uint64 {
	h ^= v
	h *= fnv64Prime
	return h
}

func hashMixString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnv64Prime
	}
	return h
}

func styleSignature(style tcell.Style) uint64 {
	h := uint64(fnv64Offset)
	h = hashMixUint64(h, uint64(style.GetForeground()))
	h = hashMixUint64(h, uint64(style.GetBackground()))
	h = hashMixUint64(h, uint64(style.GetAttributes()))
	h = hashMixUint64(h, uint64(style.GetUnderlineStyle()))
	h = hashMixUint64(h, uint64(style.GetUnderlineColor()))
	id, url := style.GetUrl()
	h = hashMixString(h, id)
	h = hashMixString(h, url)
	return h
}

func cellSignature(text string, style tcell.Style, dw uint8, cont bool, leadX int) uint64 {
	h := uint64(fnv64Offset)
	h = hashMixString(h, text)
	h = hashMixUint64(h, styleSignature(style))
	h = hashMixUint64(h, uint64(dw))
	if cont {
		h = hashMixUint64(h, 1)
	} else {
		h = hashMixUint64(h, 0)
	}
	h = hashMixUint64(h, uint64(leadX+1))
	return h
}

func (f *frame) putCellText(x int, y int, text string, style tcell.Style, dw uint8, cont bool, leadX int) {
	if x < 0 || y < 0 || x >= f.width || y >= f.height {
		return
	}

	// If this coordinate already contains a wide lead written earlier in the
	// same frame, clear its old tail now. This preserves "last write wins"
	// semantics when primitives overwrite a wide grapheme with narrow content.
	index := y*f.width + x
	prev := f.cells[index]
	if prev.gen == f.gen && !prev.cont && prev.dw > 1 {
		oldEnd := x + int(prev.dw)
		if oldEnd > f.width {
			oldEnd = f.width
		}
		if x+1 < oldEnd {
			f.markSpan(y, x+1, oldEnd)
			for i := x + 1; i < oldEnd; i++ {
				f.cells[y*f.width+i] = cell{}
			}
		}
	}

	f.markSpan(y, x, x+1)
	cell := &f.cells[index]
	cell.text = text
	cell.style = style
	cell.dw = dw
	cell.cont = cont
	cell.leadX = leadX
	cell.sig = cellSignature(text, style, dw, cont, leadX)
	cell.gen = f.gen
}

func (f *frame) rowSpan(y int) (start int, end int, ok bool) {
	if y < 0 || y >= f.height {
		return 0, 0, false
	}
	if f.rowGen[y] != f.gen {
		return 0, 0, false
	}
	return f.rowStart[y], f.rowEnd[y], true
}

func (f *frame) cellAt(x, y int) (c cell, ok bool) {
	if x < 0 || y < 0 || x >= f.width || y >= f.height {
		return cell{}, false
	}
	c = f.cells[y*f.width+x]
	if c.gen != f.gen {
		return cell{}, false
	}
	return c, true
}

func cellsEqual(a cell, aOK bool, b cell, bOK bool) bool {
	if aOK != bOK {
		return false
	}
	if !aOK {
		return true
	}
	if a.text != b.text || a.style != b.style || a.dw != b.dw || a.cont != b.cont || a.leadX != b.leadX {
		return false
	}
	return true
}

type captureScreen struct {
	tcell.Screen
	frame        *frame
	defaultStyle tcell.Style
}

func (s *captureScreen) SetContent(x int, y int, primary rune, combining []rune, style tcell.Style) {
	s.frame.SetContent(x, y, primary, combining, style)
}

func (s *captureScreen) Clear() {
	s.frame.Clear()
}

func (s *captureScreen) Fill(r rune, style tcell.Style) {
	for y := 0; y < s.frame.height; y++ {
		for x := 0; x < s.frame.width; x++ {
			s.frame.SetContent(x, y, r, nil, style)
		}
	}
}

func (s *captureScreen) SetStyle(style tcell.Style) {
	s.defaultStyle = style
}

func (s *captureScreen) Get(x, y int) (str string, style tcell.Style, width int) {
	if c, ok := s.frame.cellAt(x, y); ok {
		w := uniseg.StringWidth(c.text)
		if w <= 0 {
			w = 1
		}
		return c.text, c.style, w
	}
	return "", tcell.StyleDefault, 1
}

func (s *captureScreen) Put(x int, y int, str string, style tcell.Style) (string, int) {
	if str == "" {
		return "", 0
	}

	cluster, remain, width, _ := uniseg.FirstGraphemeClusterInString(str, -1)
	if cluster == "" {
		r, size := utf8.DecodeRuneInString(str)
		if size == 0 {
			return "", 0
		}
		cluster = string(r)
		remain = str[size:]
		width = 1
	}
	if width <= 0 {
		return remain, 0
	}

	// Match terminal clipping behavior for wide graphemes at the right edge.
	if width > 1 && x == s.frame.width-1 {
		cluster = " "
		width = 1
	}

	s.frame.putCellText(x, y, cluster, style, widthToCellDW(width), false, -1)
	// Mark trailing columns as continuation cells. This ensures removing or
	// replacing a wide grapheme will still touch the right-half columns in diff.
	for i := 1; i < width; i++ {
		s.frame.putCellText(x+i, y, "", style, 0, true, x)
	}

	return remain, width
}

func (s *captureScreen) PutStr(x int, y int, str string) {
	s.PutStrStyled(x, y, str, s.defaultStyle)
}

func (s *captureScreen) PutStrStyled(x int, y int, str string, style tcell.Style) {
	for str != "" && x < s.frame.width {
		remain, width := s.Put(x, y, str, style)
		if width <= 0 || remain == str {
			return
		}
		x += width
		str = remain
	}
}
