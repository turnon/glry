package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// indexTarget is a path added to the index, and whether a folder's images
// under it are part of that.
type indexTarget struct {
	path      string
	recursive bool
}

// startIndex adds path to the chosen search backend's index on a goroutine,
// counting the images it reports as it goes. A folder indexes its own images
// alone, or the images of the folders under it too when recursive. An index
// already running takes the path into its queue, so folders can be added one
// after another.
func (a *app) startIndex(path string, recursive bool) {
	if a.searcher == nil || path == "" {
		return
	}
	target := indexTarget{path: path, recursive: recursive}
	if a.indexRunning {
		a.indexQueue = append(a.indexQueue, target)
		return
	}
	a.beginIndex(target)
}

// beginIndex starts indexing target and queues the next path when it finishes.
func (a *app) beginIndex(target indexTarget) {
	a.indexGen++
	gen := a.indexGen
	a.indexPath = target.path
	a.indexCount = 0
	a.indexTotal = 0
	a.indexSkipped = 0
	a.indexRunning = true
	a.indexErr = ""
	progress, err := a.searcher.index(target.path, target.recursive)
	if err != nil {
		a.indexRunning = false
		a.indexErr = err.Error()
		a.indexQueue = nil
		return
	}
	a.async(func() {
		done := 0
		for p := range progress {
			n, total, skipped := p.done, p.total, p.skipped
			done = n
			a.post(func() {
				if gen != a.indexGen {
					return
				}
				a.indexCount = n
				a.indexTotal = total
				a.indexSkipped = skipped
			})
		}
		a.post(func() {
			if gen != a.indexGen {
				return
			}
			a.indexCount = done
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
	target := a.indexQueue[0]
	a.indexQueue = a.indexQueue[1:]
	a.beginIndex(target)
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

// indexMenuItems adds the items that index paths to a context menu: Index
// adds each folder's own images, and, for a folder, Index Recursively adds
// the images of the folders under it too.
func (a *app) indexMenuItems(m *ui.Menu, paths []string, dir bool) {
	if m.Item("Index").Chosen() {
		for _, p := range paths {
			a.startIndex(p, false)
		}
	}
	if dir && m.Item("Index Recursively").Chosen() {
		for _, p := range paths {
			a.startIndex(p, true)
		}
	}
}

// indexMenuItem adds the items that index path to a context menu, dir
// telling that path is a folder.
func (a *app) indexMenuItem(m *ui.Menu, path string, dir bool) {
	a.indexMenuItems(m, []string{path}, dir)
}

// indexGridMenuItem adds the items that index path to a folders view
// cell's context menu, dir telling that path is a folder. When path is one
// of the chosen items, it indexes all of them, in the grid's order.
func (a *app) indexGridMenuItem(m *ui.Menu, path string, dir bool) {
	a.indexMenuItems(m, a.indexTargets(path), dir)
}

// indexTargets returns the paths the Index menu items of path apply to:
// the chosen items when path is one of them, else path alone.
func (a *app) indexTargets(path string) []string {
	if a.folderPick.selection.Len() == 0 || !a.folderPick.selection.Has(path) {
		return []string{path}
	}
	paths := make([]string, 0, a.folderPick.selection.Len())
	for _, item := range a.items {
		if a.folderPick.selection.Has(item.path) {
			paths = append(paths, item.path)
		}
	}
	if len(paths) == 0 {
		return []string{path}
	}
	return paths
}

// indexNotice tells how far a running index got, or why it stopped. An index
// that finished has nothing left to tell, unless it left images out of the
// index, which the notice counts. failed says the notice is an error.
func (a *app) indexNotice() (notice, tooltip string, failed bool) {
	if a.indexPath == "" {
		return "", "", false
	}
	skipped := ""
	if n := a.indexSkipped; n > 0 {
		skipped = fmt.Sprintf(", %d skipped", n)
	}
	switch {
	case a.indexRunning:
		notice = fmt.Sprintf("Indexing %d/%d", a.indexCount, a.indexTotal)
		if n := len(a.indexQueue); n > 0 {
			notice += fmt.Sprintf(" and %d queued", n)
		}
		notice += skipped
		tooltip = fmt.Sprintf("Indexing %s… %s", rootName(a.indexPath), imageCount(a.indexTotal))
		if n := len(a.indexQueue); n > 0 {
			tooltip += fmt.Sprintf(", %d queued", n)
		}
		tooltip += skipped
		return notice, tooltip, false
	case a.indexErr != "":
		notice = fmt.Sprintf("Can't index %s", rootName(a.indexPath))
		tooltip = fmt.Sprintf("Can't index %s: %s", rootName(a.indexPath), a.indexErr)
		return notice, tooltip, true
	case a.indexSkipped > 0:
		// A finished index has nothing to tell but the images it left out.
		notice = fmt.Sprintf("Indexed %s%s", imageCount(a.indexTotal), skipped)
		tooltip = fmt.Sprintf("Indexed %s: %s%s", rootName(a.indexPath), imageCount(a.indexTotal), skipped)
		return notice, tooltip, false
	}
	return "", "", false
}
