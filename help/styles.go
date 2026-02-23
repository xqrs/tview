package help

import (
	"github.com/gdamore/tcell/v3"
)

type Styles struct {
	ShortKeyStyle       tcell.Style
	ShortDescStyle      tcell.Style
	ShortSeparatorStyle tcell.Style

	FullKeyStyle       tcell.Style
	FullDescStyle      tcell.Style
	FullSeparatorStyle tcell.Style

	EllipsisStyle tcell.Style
}

func DefaultStyles() Styles {
	dim := tcell.StyleDefault.Dim(true)
	normal := tcell.StyleDefault
	return Styles{
		ShortKeyStyle:       dim,
		ShortDescStyle:      normal,
		ShortSeparatorStyle: dim,
		FullKeyStyle:        dim,
		FullDescStyle:       normal,
		FullSeparatorStyle:  dim,
		EllipsisStyle:       dim,
	}
}
