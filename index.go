package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// startIndex adds path to the chosen search backend's index on a goroutine,
// counting the images it reports as it goes. An index already running takes
// the path into its queue, so folders can be added one after another.
func (a *app) startIndex(path string) {
	if a.searcher == nil || path == "" {
		return
	}
	if a.indexRunning {
		a.indexQueue = append(a.indexQueue, path)
		return
	}
	a.beginIndex(path)
}

// beginIndex starts indexing path and queues the next path when it finishes.
func (a *app) beginIndex(path string) {
	a.indexGen++
	gen := a.indexGen
	a.indexPath = path
	a.indexCount = 0
	a.indexRunning = true
	a.indexErr = ""
	progress, err := a.searcher.index(path)
	if err != nil {
		a.indexRunning = false
		a.indexErr = err.Error()
		a.indexQueue = nil
		return
	}
	a.async(func() {
		n := 0
		for range progress {
			n++
			done := n
			a.post(func() {
				if gen != a.indexGen {
					return
				}
				a.indexCount = done
			})
		}
		a.post(func() {
			if gen != a.indexGen {
				return
			}
			a.indexCount = n
			a.indexRunning = false
			if a.indexErr != "" {
				a.indexQueue = nil
				return
			}
			a.startNextIndex()
		})
	})
}

// startNextIndex begins the first folder queued while an index ran.
func (a *app) startNextIndex() {
	if a.indexRunning || len(a.indexQueue) == 0 {
		return
	}
	path := a.indexQueue[0]
	a.indexQueue = a.indexQueue[1:]
	a.beginIndex(path)
}

// indexFailed stops the index the view shows, saying why it did.
func (a *app) indexFailed(err error) {
	a.post(func() {
		if !a.indexRunning {
			return
		}
		a.indexRunning = false
		a.indexErr = err.Error()
	})
}

// indexMenuItem adds the item that indexes path to a context menu.
func (a *app) indexMenuItem(m *ui.Menu, path string) {
	if m.Item("Index").Chosen() {
		a.startIndex(path)
	}
}

// indexStatus shows how far the last index got, at the bottom of a view.
func (a *app) indexStatus(c *ui.Context) {
	if a.indexPath == "" {
		return
	}
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(8, 24).Gap(10).AlignItems(ui.Center).Children(func() {
		switch {
		case a.indexRunning:
			ui.Progress(c, -1).Width(120)
			detail := fmt.Sprintf("Indexing %s… %s", rootName(a.indexPath), imageCount(a.indexCount))
			if n := len(a.indexQueue); n > 0 {
				detail += fmt.Sprintf(", %d queued", n)
			}
			ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted).SingleLine()
		case a.indexErr != "":
			ui.Text(c, fmt.Sprintf("Can't index %s: %s", rootName(a.indexPath), a.indexErr)).
				FontSize(12).TextColor(t.Danger).SingleLine().Tooltip(a.indexErr)
		default:
			ui.Text(c, fmt.Sprintf("Indexed %s in %s", imageCount(a.indexCount), rootName(a.indexPath))).
				FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
	})
}
