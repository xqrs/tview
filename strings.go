package tview

import (
	"math/rand"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v3"
	"github.com/rivo/uniseg"
)

func hasEscapedTagPrefix(str string) bool {
	if len(str) < 4 || str[0] != '[' {
		return false
	}
	i := 1
	if str[i] == '[' || str[i] == ']' {
		return false
	}
	for i < len(str) && str[i] != '[' && str[i] != ']' {
		i++
	}
	if i >= len(str) || str[i] != '[' {
		return false
	}
	for i < len(str) && str[i] == '[' {
		i++
	}
	return i < len(str) && str[i] == ']'
}

func isAlphaNum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isTagStartChar(b byte) bool {
	return isAlphaNum(b) || b == '#' || b == '-' || b == ':'
}

func isTagNameCharOrEnd(b byte) bool {
	return isAlphaNum(b) || b == ']' || b == ':'
}

func isTagBackgroundStartChar(b byte) bool {
	return isAlphaNum(b) || b == '#' || b == '-' || b == ':' || b == ']'
}

func isAttrTag(b byte) bool {
	switch b {
	case 'b', 'u', 'i', 'l', 'd', 's', 'r', 'B', 'U', 'I', 'L', 'D', 'S', 'R':
		return true
	default:
		return false
	}
}

func attrMaskForUpper(b byte) tcell.AttrMask {
	switch b {
	case 'B':
		return tcell.AttrBold
	case 'I':
		return tcell.AttrItalic
	case 'L':
		return tcell.AttrBlink
	case 'D':
		return tcell.AttrDim
	case 'S':
		return tcell.AttrStrikeThrough
	case 'R':
		return tcell.AttrReverse
	default:
		return 0
	}
}

// stepOptions is a bit field of options for [step]. A value of 0 results in
// [step] having the same behavior as uniseg.Step, i.e. no tview-related parsing
// is performed.
type stepOptions int

// Bit fields for [stepOptions].
const (
	stepOptionsNone  stepOptions = 0
	stepOptionsStyle stepOptions = 1 << iota // Parse style tags.
)

// stepState represents the current state of the parser implemented in [step].
type stepState struct {
	unisegState     int         // The state of the uniseg parser.
	boundaries      int         // Information about boundaries, as returned by uniseg.Step.
	style           tcell.Style // The style of the returned grapheme cluster.
	escapedTagState int         // States for parsing escaped tags (defined in [step]).
	grossLength     int         // The length of the cluster, including any tags not returned.

	// The styles for the initial call to [step].
	initialForeground tcell.Color
	initialBackground tcell.Color
	initialAttributes tcell.AttrMask
}

// IsWordBoundary returns true if the boundary between the returned grapheme
// cluster and the one following it is a word boundary.
func (s *stepState) IsWordBoundary() bool {
	return s.boundaries&uniseg.MaskWord != 0
}

// IsSentenceBoundary returns true if the boundary between the returned grapheme
// cluster and the one following it is a sentence boundary.
func (s *stepState) IsSentenceBoundary() bool {
	return s.boundaries&uniseg.MaskSentence != 0
}

// LineBreak returns whether the string can be broken into the next line after
// the returned grapheme cluster. If optional is true, the line break is
// optional. If false, the line break is mandatory, e.g. after a newline
// character.
func (s *stepState) LineBreak() (lineBreak, optional bool) {
	switch s.boundaries & uniseg.MaskLine {
	case uniseg.LineCanBreak:
		return true, true
	case uniseg.LineMustBreak:
		return true, false
	}
	return false, false // uniseg.LineDontBreak.
}

// Width returns the grapheme cluster's width in cells.
func (s *stepState) Width() int {
	return s.boundaries >> uniseg.ShiftWidth
}

// GrossLength returns the grapheme cluster's length in bytes, including any
// tags that were parsed but not explicitly returned.
func (s *stepState) GrossLength() int {
	return s.grossLength
}

// Style returns the style for the grapheme cluster.
func (s *stepState) Style() tcell.Style {
	return s.style
}

// step uses uniseg.Step to iterate over the grapheme clusters of a string but
// (optionally) also parses the string for style tags.
//
// This function can be called consecutively to extract all grapheme clusters
// from str, without returning any contained (parsed) tags. The return values
// are the first grapheme cluster, the remaining string, and the new state. Pass
// the remaining string and the returned state to the next call. If the rest
// string is empty, parsing is complete. Call the returned state's methods for
// boundary and cluster width information.
//
// The returned cluster may be empty if the given string consists of only
// (parsed) tags. The boundary and width information will be meaningless in
// this case but the style will describe the style at the end of the string.
//
// Pass nil for state on the first call. This will assume an initial style with
// [Styles.PrimitiveBackgroundColor] as the background color and
// [Styles.PrimaryTextColor] as the text color. If you want to start with a
// different style, you can set the state accordingly
// but you must then set [state.unisegState] to -1.
//
// There is no need to call uniseg.HasTrailingLineBreakInString on the last
// non-empty cluster as this function will do this for you and adjust the
// returned boundaries accordingly.
func step(str string, state *stepState, opts stepOptions) (cluster, rest string, newState *stepState) {
	// Set up initial state.
	if state == nil {
		state = &stepState{
			unisegState: -1,
			style:       tcell.StyleDefault.Background(Styles.PrimitiveBackgroundColor).Foreground(Styles.PrimaryTextColor),
		}
	}
	if state.unisegState < 0 {
		state.initialForeground = state.style.GetForeground()
		state.initialBackground = state.style.GetBackground()
		state.initialAttributes = state.style.GetAttributes()
	}
	if len(str) == 0 {
		newState = state
		return
	}

	// Get a grapheme cluster.
	preState := state.unisegState
	cluster, rest, state.boundaries, state.unisegState = uniseg.StepString(str, preState)
	state.grossLength = len(cluster)
	if rest == "" {
		if !uniseg.HasTrailingLineBreakInString(cluster) {
			state.boundaries &^= uniseg.MaskLine
		}
	}

	// Parse tags.
	if opts != stepOptionsNone {
		const (
			etNone int = iota
			etStart
			etChar
			etClosing
		)

		// Finite state machine for escaped tags.
		switch state.escapedTagState {
		case etStart:
			if cluster[0] == '[' || cluster[0] == ']' { // Invalid escaped tag.
				state.escapedTagState = etNone
			} else { // Other characters are allowed.
				state.escapedTagState = etChar
			}
		case etChar:
			switch cluster[0] {
			case ']': // In theory, this should not happen.
				state.escapedTagState = etNone
			case '[': // Starting closing sequence.
				// Swallow the first one.
				cluster, rest, state.boundaries, state.unisegState = uniseg.StepString(rest, preState)
				state.grossLength += len(cluster)
				if rest == "" {
					if !uniseg.HasTrailingLineBreakInString(cluster) {
						state.boundaries &^= uniseg.MaskLine
					}
				}
				if cluster[0] == ']' {
					state.escapedTagState = etNone
				} else {
					state.escapedTagState = etClosing
				}
			} // More characters. Remain in etChar.
		case etClosing:
			if cluster[0] != '[' {
				state.escapedTagState = etNone
			}
		}

		// Regular tags.
		if state.escapedTagState == etNone {
			if cluster[0] == '[' {
				// We've already opened a tag. Parse it.
				length, style := parseTag(str, state, opts)
				if length > 0 {
					state.style = style
					cluster, rest, state.boundaries, state.unisegState = uniseg.StepString(str[length:], preState)
					state.grossLength = len(cluster) + length
					if rest == "" {
						if !uniseg.HasTrailingLineBreakInString(cluster) {
							state.boundaries &^= uniseg.MaskLine
						}
					}
				}
				// Is this an escaped tag?
				if hasEscapedTagPrefix(str[length:]) {
					state.escapedTagState = etStart
				}
			}
			if len(rest) > 0 && rest[0] == '[' {
				// A tag might follow the cluster. If so, we need to fix the state
				// for the boundaries to be correct.
				if length, _ := parseTag(rest, state, opts); length > 0 {
					if len(rest) > length {
						_, l := utf8.DecodeRuneInString(rest[length:])
						cluster += rest[length : length+l]
					}
					var taglessRest string
					cluster, taglessRest, state.boundaries, state.unisegState = uniseg.StepString(cluster, preState)
					if taglessRest == "" {
						if !uniseg.HasTrailingLineBreakInString(cluster) {
							state.boundaries &^= uniseg.MaskLine
						}
					}
				}
			}
		}
	}

	newState = state
	return
}

// parseTag parses str for consecutive style tags, assuming that str starts with
// the opening bracket for the first tag. It returns the string length of all
// valid tags (0 if the first tag is not valid) and the updated style for valid
// tags (based on the provided state).
func parseTag(str string, state *stepState, opts stepOptions) (length int, style tcell.Style) {
	if opts == stepOptionsNone {
		return // No tags to parse.
	}

	// Automata states for parsing tags.
	const (
		tagStateNone = iota
		tagStateDoneTag
		tagStateStart
		tagStateEndForeground
		tagStateStartBackground
		tagStateNumericForeground
		tagStateNameForeground
		tagStateEndBackground
		tagStateStartAttributes
		tagStateNumericBackground
		tagStateNameBackground
		tagStateAttributes
		tagStateEndAttributes
		tagStateStartURL
		tagStateEndURL
		tagStateURL
	)

	var (
		tagState, tagLength int
		tempStr             strings.Builder
	)
	tStyle := state.style

	// Process state transitions.
	for len(str) > 0 {
		ch := str[0]
		str = str[1:]
		tagLength++

		// Transition.
		switch tagState {
		case tagStateNone:
			if ch == '[' { // Start of a tag.
				tagState = tagStateStart
			} else { // Not a tag. We're done.
				return
			}
		case tagStateStart:
			if ch != '"' && opts&stepOptionsStyle == 0 {
				return // Style tags are not allowed.
			}
			switch {
			case !isTagStartChar(ch): // Invalid style tag.
				return
			case ch == '-': // Reset foreground color.
				tStyle = tStyle.Foreground(state.initialForeground)
				tagState = tagStateEndForeground
			case ch == ':': // No foreground color.
				tagState = tagStateStartBackground
			default:
				tempStr.Reset()
				tempStr.WriteByte(ch)
				if ch == '#' { // Numeric foreground color.
					tagState = tagStateNumericForeground
				} else { // Letters or numbers.
					tagState = tagStateNameForeground
				}
			}
		case tagStateEndForeground:
			switch ch {
			case ']': // End of tag.
				tagState = tagStateDoneTag
			case ':':
				tagState = tagStateStartBackground
			default: // Invalid tag.
				return
			}
		case tagStateNumericForeground:
			if ch == ']' || ch == ':' {
				if tempStr.Len() != 7 { // Must be #rrggbb.
					return
				}
				tStyle = tStyle.Foreground(tcell.GetColor(tempStr.String()))
			}
			switch {
			case ch == ']': // End of tag.
				tagState = tagStateDoneTag
			case ch == ':': // Start of background color.
				tagState = tagStateStartBackground
			case isHexDigit(ch):
				tempStr.WriteByte(ch)
				tagState = tagStateNumericForeground
			default: // Invalid tag.
				return
			}
		case tagStateNameForeground:
			if ch == ']' || ch == ':' {
				name := tempStr.String()
				if name[0] >= '0' && name[0] <= '9' { // Must not start with a digit.
					return
				}
				tStyle = tStyle.Foreground(tcell.ColorNames[name])
			}
			switch {
			case !isTagNameCharOrEnd(ch): // Invalid tag.
				return
			case ch == ']': // End of tag.
				tagState = tagStateDoneTag
			case ch == ':': // Start of background color.
				tagState = tagStateStartBackground
			default: // Letters or numbers.
				tempStr.WriteByte(ch)
			}
		case tagStateStartBackground:
			switch {
			case !isTagBackgroundStartChar(ch): // Invalid style tag.
				return
			case ch == ']': // End of tag.
				tagState = tagStateDoneTag
			case ch == '-': // Reset background color.
				tStyle = tStyle.Background(state.initialBackground)
				tagState = tagStateEndBackground
			case ch == ':': // No background color.
				tagState = tagStateStartAttributes
			default:
				tempStr.Reset()
				tempStr.WriteByte(ch)
				if ch == '#' { // Numeric background color.
					tagState = tagStateNumericBackground
				} else { // Letters or numbers.
					tagState = tagStateNameBackground
				}
			}
		case tagStateEndBackground:
			switch ch {
			case ']': // End of tag.
				tagState = tagStateDoneTag
			case ':': // Start of attributes.
				tagState = tagStateStartAttributes
			default: // Invalid tag.
				return
			}
		case tagStateNumericBackground:
			if ch == ']' || ch == ':' {
				if tempStr.Len() != 7 { // Must be #rrggbb.
					return
				}
				tStyle = tStyle.Background(tcell.GetColor(tempStr.String()))
			}
			if ch == ']' { // End of tag.
				tagState = tagStateDoneTag
			} else if ch == ':' { // Start of attributes.
				tagState = tagStateStartAttributes
			} else if isHexDigit(ch) { // Hex digit.
				tempStr.WriteByte(ch)
				tagState = tagStateNumericBackground
			} else { // Invalid tag.
				return
			}
		case tagStateNameBackground:
			if ch == ']' || ch == ':' {
				name := tempStr.String()
				if name[0] >= '0' && name[0] <= '9' { // Must not start with a digit.
					return
				}
				tStyle = tStyle.Background(tcell.ColorNames[name])
			}
			switch {
			case !isTagNameCharOrEnd(ch): // Invalid tag.
				return
			case ch == ']': // End of tag.
				tagState = tagStateDoneTag
			case ch == ':': // Start of background color.
				tagState = tagStateStartAttributes
			default: // Letters or numbers.
				tempStr.WriteByte(ch)
			}
		case tagStateStartAttributes:
			switch {
			case ch == ']': // End of tag.
				tagState = tagStateDoneTag
			case ch == '-': // Reset attributes.
				tStyle = tStyle.Attributes(state.initialAttributes)
				// Underline is not part of AttrMask in tcell, so reset it explicitly.
				tStyle = tStyle.Underline(false)
				tagState = tagStateEndAttributes
			case ch == ':': // Start of URL.
				tagState = tagStateStartURL
			case isAttrTag(ch): // Attribute tag.
				tempStr.Reset()
				tempStr.WriteByte(ch)
				tagState = tagStateAttributes
			default: // Invalid tag.
				return
			}
		case tagStateAttributes:
			if ch == ']' || ch == ':' {
				flags := tempStr.String()
				a := tStyle.GetAttributes()
				for index := range len(flags) {
					ch := flags[index]
					switch {
					case ch == 'u':
						tStyle = tStyle.Underline(true)
					case ch == 'U':
						tStyle = tStyle.Underline(false)
					case ch >= 'a' && ch <= 'z':
						a |= attrMaskForUpper(ch - ('a' - 'A'))
					default:
						a &^= attrMaskForUpper(ch)
					}
				}
				tStyle = tStyle.Attributes(a)
			}
			switch {
			case ch == ']': // End of tag.
				tagState = tagStateDoneTag
			case ch == ':': // Start of URL.
				tagState = tagStateStartURL
			case isAttrTag(ch): // Attribute tag.
				tempStr.WriteByte(ch)
			default: // Invalid tag.
				return
			}
		case tagStateEndAttributes:
			switch ch {
			case ']': // End of tag.
				tagState = tagStateDoneTag
			case ':': // Start of URL.
				tagState = tagStateStartURL
			default: // Invalid tag.
				return
			}
		case tagStateStartURL:
			switch ch {
			case ']': // End of tag.
				tagState = tagStateDoneTag
			case '-': // Reset URL.
				tStyle = tStyle.Url("").UrlId("")
				tagState = tagStateEndURL
			default: // URL character.
				tempStr.Reset()
				tempStr.WriteByte(ch)
				tStyle = tStyle.UrlId(strconv.Itoa(int(rand.Uint32()))) // Generate a unique ID for this URL.
				tagState = tagStateURL
			}
		case tagStateEndURL:
			if ch == ']' { // End of tag.
				tagState = tagStateDoneTag
			} else { // Invalid tag.
				return
			}
		case tagStateURL:
			if ch == ']' { // End of tag.
				tStyle = tStyle.Url(tempStr.String())
				tagState = tagStateDoneTag
			} else { // URL character.
				tempStr.WriteByte(ch)
			}
		}

		// The last transition led to a tag end. Make the tag permanent.
		if tagState == tagStateDoneTag {
			length, style = tagLength, tStyle
			tagState = tagStateNone // Reset state.
		}
	}

	return
}

// TaggedStringWidth returns the width of the given string needed to print it on
// screen. The text may contain style tags which are not counted.
func TaggedStringWidth(text string) (width int) {
	var state *stepState
	for len(text) > 0 {
		_, text, state = step(text, state, stepOptionsStyle)
		width += state.Width()
	}
	return
}

// WordWrap splits a text such that each resulting line does not exceed the
// given screen width. Split points are determined using the algorithm described
// in [Unicode Standard Annex #14].
//
// This function considers style tags to have no width.
//
// [Unicode Standard Annex #14]: https://www.unicode.org/reports/tr14/
func WordWrap(text string, width int) (lines []string) {
	if width <= 0 {
		return
	}

	var (
		state                                              *stepState
		lineWidth, lineLength, lastOption, lastOptionWidth int
	)
	str := text
	for len(str) > 0 {
		// Parse the next character.
		_, str, state = step(str, state, stepOptionsStyle)
		cWidth := state.Width()

		// Would it exceed the line width?
		if lineWidth+cWidth > width {
			if lastOptionWidth == 0 {
				// No split point so far. Just split at the current position.
				lines = append(lines, text[:lineLength])
				text = text[lineLength:]
				lineWidth, lineLength, lastOption, lastOptionWidth = 0, 0, 0, 0
			} else {
				// Split at the last split point.
				lines = append(lines, text[:lastOption])
				text = text[lastOption:]
				lineWidth -= lastOptionWidth
				lineLength -= lastOption
				lastOption, lastOptionWidth = 0, 0
			}
		}

		// Move ahead.
		lineWidth += cWidth
		lineLength += state.GrossLength()

		// Check for split points.
		if lineBreak, optional := state.LineBreak(); lineBreak {
			if optional {
				// Remember this split point.
				lastOption = lineLength
				lastOptionWidth = lineWidth
			} else {
				// We must split here.
				lines = append(lines, strings.TrimRight(text[:lineLength], "\n\r"))
				text = text[lineLength:]
				lineWidth, lineLength, lastOption, lastOptionWidth = 0, 0, 0, 0
			}
		}
	}
	lines = append(lines, text)

	return
}

// Escape escapes the given text such that color tags are not
// recognized and substituted by the print functions of this package. For
// example, to include a tag-like string in a box title or in a TextView:
//
//	box.SetTitle(tview.Escape("[squarebrackets]"))
//	fmt.Fprint(textView, tview.Escape(`["quoted"]`))
func Escape(text string) string {
	return escapePattern.ReplaceAllString(text, "$1[]")
}

// Unescape unescapes text previously escaped with [Escape].
func Unescape(text string) string {
	return unescapePattern.ReplaceAllString(text, "$1]")
}

// stripTags strips style tags from the given string.
func stripTags(text string) string {
	var (
		str   strings.Builder
		state *stepState
	)
	for len(text) > 0 {
		var c string
		c, text, state = step(text, state, stepOptionsStyle)
		str.WriteString(c)
	}
	return str.String()
}
