package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		// The rows' width follows their labels, which the previous frame
		// laid out: apply the width measured from them, and ask for the
		// frame that measures the rows this one builds.
		width := a.treeRowWidth(c)
		if width != a.treeRowW {
			a.treeRowW = width
			c.Invalidate()
		}
		outline := ui.Outline(c, &a.tree, []string{a.root}, a.children, func(dir string) {
			a.treeRow(c, dir)
		}).Grow(1).Padding(6).RowWidth(width)
		if outline.Changed() && a.dirRow >= 0 && a.dirRow < a.tree.Rows() {
			a.selectDir(a.tree.Item(a.dirRow))
			c.Invalidate()
		}
		a.applyPendingTreeSelection(c)
	})
}

func (a *app) treeRow(c *ui.Context, dir string) {
	t := c.Theme()
	icon := t.TextMuted
	if dir == a.selected {
		// The chosen row draws on the accent, where only its text shows.
		icon = t.AccentText
	}
	row := ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center)
	row.Children(func() {
		ui.Icon(c, folderIcon).Size(treeIconSize, treeIconSize).TextColor(icon)
		ui.Text(c, rootName(dir)).SingleLine().ContextMenu(func(m *ui.Menu) { a.indexMenuItem(m, dir) })
	})
	row.ContextMenu(func(m *ui.Menu) { a.indexMenuItem(m, dir) })
}

// treeIconSize is the size of a tree row's folder icon, and treeIconGap
// the room after it.
const (
	treeIconSize float32 = 14
	treeIconGap  float32 = 6
)

// treeRowWidth is the least width of the tree's rows that shows every
// label whole: the outline scrolls sideways only when one does not fit
// the sidebar. The room around a label is that of a row: its padding,
// its indentation and arrow, and the folder icon.
func (a *app) treeRowWidth(c *ui.Context) float32 {
	t := c.Theme()
	// The outline's indent, Space(4) while OutlineState.Indent is zero.
	indent := t.Space(4)
	pad, gap := t.Space(2), t.Space(1)
	arrow := t.Space(4)
	widest := float32(0)
	for i := 0; i < a.tree.Rows(); i++ {
		depth := float32(a.tree.Depth(i))
		w := 2*pad + arrow + treeIconSize + treeIconGap + gap
		if depth > 0 {
			// Indented rows give the indent and the arrow their gap too.
			w += depth*indent + gap
		}
		w += a.treeLabelWidth(c, rootName(a.tree.Item(i)))
		widest = max(widest, w)
	}
	// A little more than measured, so that rounding never trims a label.
	return widest + 1
}

// treeLabelWidth is how wide name draws in a tree row, measured once.
func (a *app) treeLabelWidth(c *ui.Context, name string) float32 {
	if w, ok := a.nameWidths[name]; ok {
		return w
	}
	w, _ := c.MeasureText(0, ui.Span{Text: name})
	a.nameWidths[name] = w
	return w
}

func (a *app) folders(c *ui.Context) {
	t := c.Theme()
	m := a.folderGrid()
	a.folderPick.built = a.folderPick.built[:0]
	ui.Column(c).Fill().Children(func() {
		switch {
		case a.foldersErr != nil:
			a.gridMessage(c, "Can't read this folder", a.foldersErr.Error(), false)
		case a.foldersLoading:
			a.gridMessage(c, "Loading images…", "", true)
		case len(a.items) == 0:
			a.gridMessage(c, "This folder is empty", "Choose another folder in the sidebar.", false)
		default:
			a.grid.Key = func(i int) any {
				if i >= 0 && i < len(a.items) {
					return a.items[i].path
				}
				return i
			}
			a.grid.Selected = &a.chosen
			a.grid.Selection = &a.folderPick.selection
			a.grid.Label = func(i int) string {
				if i >= 0 && i < len(a.items) {
					return a.items[i].name
				}
				return ""
			}
			grid := ui.GridView(c, &a.grid, len(a.items), a.cellWidth, a.cellHeight, func(i int) {
				a.itemCell(c, m, i)
			}).Grow(1).Padding(12)
			grid.HandleInput(func(ev ui.InputEvent) bool { return a.gridInput(c, m, &a.grid.Handle, ev) })
			grid.DrawOver(func(p *ui.Painter, r ui.Rect) { a.paintGridDrag(p, r, &a.folderPick) })
			if a.revealChosen >= 0 {
				a.revealChosenIntoView(c)
			}
			if grid.Submitted() {
				a.activateItem(c)
			}
		}
	})
	if !a.foldersLoading && a.foldersErr == nil && a.selected != "" {
		// An overlay keeps the tag in the window's corner while the
		// grid scrolls and resizes.
		ui.Overlay(c, func() {
			ui.Badge(c, a.imagesTag()).Absolute().Bottom(0).Right(0).
				Padding(3, 8).Radius(8, 0, 0, 0).
				Background(t.Surface).TextColor(t.TextMuted).Tooltip("Images and their total size")
		})
	}
}

// imagesTag says how many images the folder shows and how large they are.
func (a *app) imagesTag() string {
	var total int64
	for _, p := range a.photos {
		total += p.size
	}
	return fmt.Sprintf("%s, %s", imageCount(len(a.photos)), byteSize(total))
}

func (a *app) gridMessage(c *ui.Context, title, detail string, loading bool) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		if loading {
			ui.Spinner(c)
		} else {
			ui.Icon(c, pictureIcon).Size(42, 42).TextColor(t.TextMuted)
		}
		ui.Text(c, title).TextColor(t.TextMuted)
		if detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// folderGrid is the folders view's grid as choosing items needs it.
func (a *app) folderGrid() gridModel {
	return gridModel{
		pick:     &a.folderPick,
		selected: &a.chosen,
		count:    func() int { return len(a.items) },
		key: func(i int) string {
			if i >= 0 && i < len(a.items) {
				return a.items[i].path
			}
			return ""
		},
		open: func(c *ui.Context, i int) {
			a.chosen = i
			a.activateItem(c)
		},
	}
}

func (a *app) itemCell(c *ui.Context, m gridModel, i int) {
	if i < 0 || i >= len(a.items) {
		return
	}
	a.folderPick.built = append(a.folderPick.built, i)
	item := a.items[i]
	if item.dir {
		a.folderCell(c, m, i, item.path, item.name)
		return
	}
	if item.photo != nil {
		a.photoCell(c, m, i, item.photo)
	}
}

// cellMetrics adapts a cell's padding and icon to its configured size.
func (a *app) cellMetrics() (pad, gap, iconSize float32, showName bool) {
	pad, gap, iconSize = 8, 6, 32
	if compact := a.cellWidth < 90 || a.cellHeight < 90; compact {
		side := min(a.cellWidth, a.cellHeight)
		pad = max(1, min(6, side/12))
		gap = max(1, min(4, side/16))
		iconSize = max(8, min(32, side*0.55))
	}
	showName = a.cellHeight >= 34 && a.cellWidth >= 44
	return
}

func (a *app) folderCell(c *ui.Context, m gridModel, i int, path, name string) {
	t := c.Theme()
	pad, gap, iconSize, showName := a.cellMetrics()
	cell := ui.Column(c).Fill().Padding(pad).Gap(gap)
	a.bindGridCell(c, m, cell, i)
	cell.Children(func() {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Icon(c, folderIcon).Size(iconSize, iconSize).TextColor(t.Accent)
		})
		if showName {
			ui.Text(c, name).FontSize(12).SingleLine().TextAlign(ui.Center).FillWidth().Tooltip(name)
		}
	})
	cell.ContextMenu(func(m *ui.Menu) { a.indexGridMenuItem(m, path) })
}

func (a *app) photoCell(c *ui.Context, m gridModel, i int, p *photo) {
	if p == nil {
		return
	}
	t := c.Theme()
	gen := a.foldersGen
	pad, gap, iconSize, showName := a.cellMetrics()
	showMeta := a.cellHeight >= 64 && a.cellWidth >= 60
	cell := ui.Column(c).Fill().Padding(pad).Gap(gap)
	a.bindGridCell(c, m, cell, i)
	cell.Children(func() {
		if p.img != nil {
			ui.Image(c, p.img).Grow(1).Fit(ui.Contain).MinHeight(0)
		} else {
			// A guarded job: only the cells the grid builds, which are
			// those in view, ask for their thumbnail, and only once.
			a.requestThumb(func() bool { return gen == a.foldersGen }, p)
			ui.Column(c).Grow(1).Center().Children(func() {
				if p.state == thumbFailed && showName {
					ui.Text(c, "Can't open").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Icon(c, pictureIcon).Size(iconSize, iconSize).TextColor(t.TextMuted)
				}
			})
		}
		if showMeta {
			ui.Column(c).Gap(1).FillWidth().Children(func() {
				ui.Text(c, photoDimensions(p)).FontSize(10).TextColor(t.TextMuted).SingleLine().TextAlign(ui.Center)
				ui.Text(c, byteSize(p.size)).FontSize(10).TextColor(t.TextMuted).SingleLine().TextAlign(ui.Center)
			})
		}
		if showName {
			ui.Text(c, p.name).FontSize(12).SingleLine().TextAlign(ui.Center).FillWidth().Tooltip(p.name)
		}
	})
	cell.ContextMenu(func(m *ui.Menu) {
		a.openPhotoMenuItem(c, m, p.path)
		a.indexGridMenuItem(m, p.path)
	})
}

// openPhotoMenuItem adds the item that opens path, or the images chosen
// with it, in the single image view.
func (a *app) openPhotoMenuItem(c *ui.Context, m *ui.Menu, path string) {
	if m.Item("Open").Chosen() {
		a.openPhoto(c, path)
	}
}

// bindGridCell gives the cell of item i its stable handle, which a drag
// uses as its place in the window, and the input that chooses items.
func (a *app) bindGridCell(c *ui.Context, m gridModel, cell ui.Element, i int) {
	if i < 0 || i >= len(m.pick.handles) {
		return
	}
	cell.Bind(&m.pick.handles[i])
	cell.HandleInput(func(ev ui.InputEvent) bool {
		return a.gridCellInput(c, m, i, &m.pick.handles[i], ev)
	})
}

// photoDimensions names a photo's width and height in pixels.
func photoDimensions(p *photo) string {
	if p.width > 0 && p.height > 0 {
		return fmt.Sprintf("%d × %d", p.width, p.height)
	}
	return "— × —"
}

func imageCount(n int) string {
	if n == 1 {
		return "1 image"
	}
	return fmt.Sprintf("%d images", n)
}

// activateItem opens the chosen folders item: a folder shows its
// contents, an image shows alone in the single image view.
func (a *app) activateItem(c *ui.Context) {
	if a.chosen < 0 || a.chosen >= len(a.items) {
		return
	}
	item := a.items[a.chosen]
	if item.dir {
		a.selectDir(item.path)
		c.Invalidate()
		return
	}
	if item.photo != nil {
		a.showPhoto(item.photo)
		c.Invalidate()
	}
}

// showFolders returns to the tree and the selected folder's images. A
// photo the single image view shows brings its folder, as a search result
// opened elsewhere does, and is chosen among its images.
func (a *app) showFolders() {
	if a.mode == viewFolders {
		return
	}
	// Without a tag the view came back from its last one, whose folder
	// the folders view still shows.
	shown := ""
	if a.mode == viewSingle && a.tag >= 0 {
		if p := a.singlePhoto(); p != nil {
			shown = p.path
		}
	}
	a.mode = viewFolders
	a.clearSingle()
	if shown != "" {
		a.revealPhoto(shown)
	}
}

// revealPhoto shows path's folder in the folders view and chooses the
// image among its items, waiting for the folder to load when it is not the
// one showing. A folder past the root has no tree row to show, so the
// folders view keeps the folder it shows.
func (a *app) revealPhoto(path string) {
	dir := filepath.Dir(path)
	if !underRoot(a.root, dir) {
		return
	}
	if dir == a.selected {
		if a.choosePhotoPath(path) {
			a.revealChosen, a.revealLaid = a.chosen, false
		}
		return
	}
	a.pendingPhotoPath = path
	a.selectDir(dir)
}

// choosePhotoPath chooses the folders view's item at path, when the folder
// shows it, and reports whether it did. The item is the grid's only
// choice, as a click makes it, so that the grid shows it chosen.
func (a *app) choosePhotoPath(path string) bool {
	for i, item := range a.items {
		if item.photo != nil && item.path == path {
			a.chosen = i
			a.folderPick.selection.Clear()
			a.folderPick.selection.Add(path)
			a.folderPick.pivot = i
			return true
		}
	}
	return false
}

// revealChosenIntoView scrolls the folders view's grid to the item a photo
// brought there: on the frame after the folder loaded, once the grid had
// its own width to lay the items out at rather than the window's.
func (a *app) revealChosenIntoView(c *ui.Context) {
	if !a.revealLaid {
		a.revealLaid = true
		c.Invalidate()
		return
	}
	a.grid.ScrollIntoView(a.revealChosen)
	a.revealChosen, a.revealLaid = -1, false
	c.Invalidate() // the grid scrolls as it lays out next
}

// underRoot reports whether dir is root or inside it.
func underRoot(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
