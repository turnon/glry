package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Gap(8).Padding(10, 12).Justify(ui.Center).Children(func() {
			ui.Button(c, "").Label("Open").Tooltip("Open a folder").BorderWidth(0).OnClick(a.chooseFolder).Children(func() {
				ui.Icon(c, openIcon).Size(16, 16)
			})
			ui.Button(c, "").Label("Refresh").Tooltip("Refresh the folder").BorderWidth(0).OnClick(a.reload).Children(func() {
				ui.Icon(c, refreshIcon).Size(16, 16)
			})
		})
		ui.Divider(c)
		outline := ui.Outline(c, &a.tree, []string{a.root}, a.children, func(dir string) {
			a.treeRow(c, dir)
		}).Grow(1).Padding(6)
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
		icon = t.Accent
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, folderIcon).Size(14, 14).TextColor(icon)
		ui.Text(c, rootName(dir)).SingleLine()
	})
}

func (a *app) gallery(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		switch {
		case a.galleryErr != nil:
			a.galleryMessage(c, "Can't read this folder", a.galleryErr.Error(), false)
		case a.galleryLoading:
			a.galleryMessage(c, "Loading images…", "", true)
		case len(a.items) == 0:
			a.galleryMessage(c, "This folder is empty", "Choose another folder in the sidebar.", false)
		default:
			a.grid.Key = func(i int) any {
				if i >= 0 && i < len(a.items) {
					return a.items[i].path
				}
				return i
			}
			a.grid.Selected = &a.chosen
			a.grid.Label = func(i int) string {
				if i >= 0 && i < len(a.items) {
					return a.items[i].name
				}
				return ""
			}
			grid := ui.GridView(c, &a.grid, len(a.items), a.cellWidth, a.cellHeight, func(i int) {
				a.itemCell(c, i)
			}).Grow(1).Padding(12)
			if grid.Submitted() {
				a.activateItem(c)
			}
		}
	})
	if !a.galleryLoading && a.galleryErr == nil && a.selected != "" {
		// An overlay keeps the tag in the window's corner while the
		// gallery scrolls and resizes.
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

func (a *app) galleryMessage(c *ui.Context, title, detail string, loading bool) {
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

func (a *app) itemCell(c *ui.Context, i int) {
	if i < 0 || i >= len(a.items) {
		return
	}
	item := a.items[i]
	if item.dir {
		a.folderCell(c, item.name)
		return
	}
	if item.photo != nil {
		a.photoCell(c, item.photo)
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

func (a *app) folderCell(c *ui.Context, name string) {
	t := c.Theme()
	pad, gap, iconSize, showName := a.cellMetrics()
	ui.Column(c).Fill().Padding(pad).Gap(gap).Children(func() {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Icon(c, folderIcon).Size(iconSize, iconSize).TextColor(t.Accent)
		})
		if showName {
			ui.Text(c, name).FontSize(12).SingleLine().TextAlign(ui.Center).FillWidth().Tooltip(name)
		}
	})
}

func (a *app) photoCell(c *ui.Context, p *photo) {
	if p == nil {
		return
	}
	t := c.Theme()
	gen := a.galleryGen
	pad, gap, iconSize, showName := a.cellMetrics()
	showMeta := a.cellHeight >= 64 && a.cellWidth >= 60
	ui.Column(c).Fill().Padding(pad).Gap(gap).Children(func() {
		if p.img != nil {
			ui.Image(c, p.img).Grow(1).Fit(ui.Contain).MinHeight(0)
		} else {
			// A guarded job: only the cells the grid builds, which are
			// those in view, ask for their thumbnail, and only once.
			a.requestThumb(gen, p)
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

// activateItem opens the chosen gallery item: a folder shows its
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

// showGallery returns to the tree and the folder's gallery.
func (a *app) showGallery() {
	if a.mode == viewGallery {
		return
	}
	a.mode = viewGallery
	a.clearSingle()
}
