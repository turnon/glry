package main

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// removeTarget is a path Remove moves to the trash, and whether path is a
// folder, whose contents go with it.
type removeTarget struct {
	path string
	dir  bool
}

// removeMenuItems adds the item that moves targets to the trash to a
// context menu.
func (a *app) removeMenuItems(c *ui.Context, m *ui.Menu, targets []removeTarget) {
	if m.Item("Remove").Chosen() {
		a.startRemove(c, targets)
	}
}

// removeMenuItem adds the item that moves path to the trash to a context
// menu, dir telling that path is a folder.
func (a *app) removeMenuItem(c *ui.Context, m *ui.Menu, path string, dir bool) {
	a.removeMenuItems(c, m, []removeTarget{{path: path, dir: dir}})
}

// removeGridMenuItem adds the item that moves path to the trash to a
// folders view cell's context menu, dir telling that path is a folder. When
// path is one of the chosen items, it removes all of them, in the grid's
// order.
func (a *app) removeGridMenuItem(c *ui.Context, m *ui.Menu, path string, dir bool) {
	a.removeMenuItems(c, m, a.removeTargets(path, dir))
}

// removeTargets returns the paths the Remove menu item of path applies to:
// the chosen items when path is one of them, else path alone, each told
// apart from a folder.
func (a *app) removeTargets(path string, dir bool) []removeTarget {
	alone := []removeTarget{{path: path, dir: dir}}
	if a.folderPick.selection.Len() == 0 || !a.folderPick.selection.Has(path) {
		return alone
	}
	targets := make([]removeTarget, 0, a.folderPick.selection.Len())
	for _, item := range a.items {
		if a.folderPick.selection.Has(item.path) {
			targets = append(targets, removeTarget{path: item.path, dir: item.dir})
		}
	}
	if len(targets) == 0 {
		return alone
	}
	return targets
}

// startRemove moves targets to the trash on a goroutine and takes them out
// of the index too, reporting why it could not where the view shows it.
func (a *app) startRemove(c *ui.Context, targets []removeTarget) {
	if len(targets) == 0 {
		return
	}
	// The removal takes the notice from an index that is not running.
	if !a.indexRunning {
		a.indexPath = ""
		a.indexErr = ""
		a.indexSkipped = 0
	}
	a.removeGen++
	gen := a.removeGen
	a.removePath = targets[0].path
	a.removeRunning = true
	a.removeErr = ""
	trash := a.trash
	if trash == nil {
		trash = mygo.Shell.TrashItem
	}
	searcher := a.searcher
	services := c.Services()
	a.async(func() {
		var (
			removed []removeTarget
			err     error
		)
		for _, target := range targets {
			if terr := trash(target.path); terr != nil {
				if err == nil {
					err = terr
				}
				continue
			}
			removed = append(removed, target)
			if searcher == nil {
				continue
			}
			if rerr := searcher.Remove(target.path); rerr != nil && err == nil {
				err = rerr
			}
		}
		a.post(func() {
			if gen != a.removeGen {
				return
			}
			a.removeRunning = false
			if err != nil {
				a.removeErr = err.Error()
			}
			a.removePending = append(a.removePending, removed...)
			services.Invalidate() // the frame after shows the folder without them
		})
	})
}

// applyRemoved takes the paths earlier Removes moved to the trash out of the
// folders view and the tree, before a frame reads them. A menu acts while a
// frame builds, so the view changes on the frame after, when what it reads
// is settled.
func (a *app) applyRemoved() {
	if len(a.removePending) == 0 {
		return
	}
	removed := a.removePending
	a.removePending = nil
	a.forgetRemoved(removed)
}

// forgetRemoved takes the paths Remove moved to the trash out of the folders
// view and the tree, and shows the folder again without them. The paths are
// no longer on disk.
func (a *app) forgetRemoved(removed []removeTarget) {
	if len(removed) == 0 {
		return
	}
	gone := make(map[string]bool, len(removed))
	var goneDirs []string
	for _, target := range removed {
		gone[target.path] = true
		a.folderPick.selection.Remove(target.path)
		if target.dir {
			goneDirs = append(goneDirs, target.path)
		}
	}
	if gone[a.root] {
		// The tree's root went: keep showing the folder it was in.
		a.setRoot(filepath.Dir(a.root))
		return
	}
	for _, dir := range goneDirs {
		delete(a.dirs, dir)
		delete(a.dirsLoading, dir)
		for cached := range a.dirs {
			if underRoot(dir, cached) {
				delete(a.dirs, cached)
			}
		}
		for cached := range a.dirsLoading {
			if underRoot(dir, cached) {
				delete(a.dirsLoading, cached)
			}
		}
		var open []string
		for item := range a.tree.Open.All() {
			if item == dir || underRoot(dir, item) {
				open = append(open, item)
			}
		}
		for _, item := range open {
			a.tree.Open.Remove(item)
		}
	}
	// Forget the removed folders among their parents' cached children.
	for _, dir := range goneDirs {
		parent := filepath.Dir(dir)
		kids, ok := a.dirs[parent]
		if !ok {
			continue
		}
		kept := kids[:0]
		for _, kid := range kids {
			if kid != dir {
				kept = append(kept, kid)
			}
		}
		a.dirs[parent] = kept
	}
	if folder := a.folderAfterRemoved(goneDirs); folder != a.selected {
		a.selectDir(folder)
		return
	}
	photos := a.photos[:0]
	for _, p := range a.photos {
		if !gone[p.path] {
			photos = append(photos, p)
		}
	}
	a.photos = photos
	chosenPath := ""
	if a.chosen >= 0 && a.chosen < len(a.items) {
		chosenPath = a.items[a.chosen].path
	}
	a.buildFolderItems()
	a.chosen = -1
	for i, item := range a.items {
		if item.path == chosenPath {
			a.chosen = i
			break
		}
	}
	a.folderPick.pivot = a.chosen
	a.revealChosen, a.revealLaid = -1, false
	a.tree.Generation++
}

// folderAfterRemoved returns the folder the folders view shows once the
// folders in goneDirs went to the trash: the selected folder when it stayed,
// else its nearest ancestor that did.
func (a *app) folderAfterRemoved(goneDirs []string) string {
	path := a.selected
	for {
		held := false
		for _, dir := range goneDirs {
			if path == dir || underRoot(dir, path) {
				held = true
				break
			}
		}
		if !held {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return a.selected
		}
		path = parent
	}
}

// removeNotice tells how far a running removal got, or why it stopped. A
// removal that finished has nothing left to tell. failed says the notice is
// an error.
func (a *app) removeNotice() (notice, tooltip string, failed bool) {
	if a.removeRunning {
		return fmt.Sprintf("Removing %s", rootName(a.removePath)),
			fmt.Sprintf("Moving %s to the Trash…", rootName(a.removePath)), false
	}
	if a.removeErr != "" {
		return fmt.Sprintf("Can't remove %s", rootName(a.removePath)),
			fmt.Sprintf("Can't remove %s: %s", rootName(a.removePath), a.removeErr), true
	}
	return "", "", false
}
