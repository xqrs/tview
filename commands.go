package tview

// Command is a side effect requested by a primitive during input handling.
// Commands are executed by the Application event loop.
type Command any

// BatchCommand groups multiple commands into a single command.
type BatchCommand []Command

// AppendCommand appends next to current and returns a merged command value.
// It flattens nested BatchCommand values.
func AppendCommand(current Command, next Command) Command {
	if next == nil {
		return current
	}
	if current == nil {
		return next
	}

	var batch BatchCommand
	switch c := current.(type) {
	case BatchCommand:
		batch = append(batch, c...)
	default:
		batch = append(batch, c)
	}

	switch n := next.(type) {
	case BatchCommand:
		batch = append(batch, n...)
	default:
		batch = append(batch, n)
	}
	return batch
}

type SetFocusCommand struct {
	Target Primitive
}

// RedrawCommand requests a redraw at the end of the current event.
type RedrawCommand struct{}

// QuitCommand requests stopping the application event loop.
type QuitCommand struct{}

// SetTitleCommand requests updating the terminal title.
type SetTitleCommand string

type SetClipboardCommand string

type GetClipboardCommand struct{}

// ConsumeEventCommand stops further propagation of the current input event.
type ConsumeEventCommand struct{}
