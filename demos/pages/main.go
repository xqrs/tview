// Demo code for the Pages primitive.
package main

import (
	"fmt"

	"github.com/rivo/tview"
)

const pageCount = 5

func main() {
	app := tview.NewApplication()
	pages := tview.NewPages()
	for page := 0; page < pageCount; page++ {
		func(page int) {
			pages.AddPage(tview.NewModal().
					SetText(fmt.Sprintf("This is page %d. Choose where to go next.", page+1)).
					AddButtons([]string{"Next", "Quit"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						if buttonIndex == 0 {
							pages.SwitchToPage(pages.GetPages()[(page+1)%pageCount])
						} else {
							app.Stop()
						}
					}),
				false,
				page == 0)
		}(page)
	}
	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		panic(err)
	}
}
