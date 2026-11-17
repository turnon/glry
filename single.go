package main

import (
	"image"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

// singleView shows one photo of the folder, as large as the window allows.
func (a *app) singleView(c *ui.Context) {
	t := c.Theme()
	p := a.singlePhoto()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if len(a.history) > 0 {
			// The tags fill the header's height and touch, as the tabs
			// of a tab bar do.
			ui.Row(c).AlignItems(ui.Stretch).Wrap().Children(func() {
				a.historyTags(c)
			})
			ui.Divider(c)
		}
		switch {
		case p == nil:
			a.galleryMessage(c, "No images in this folder", "Choose a folder with images in the file tree.", false)
			return
		case a.singleErr != nil:
			ui.Column(c).Fill().Center().Gap(8).Children(func() {
				ui.Icon(c, pictureIcon).Size(42, 42).TextColor(t.TextMuted)
				ui.Text(c, "Can't open this image").TextColor(t.TextMuted)
				ui.Text(c, a.singleErr.Error()).FontSize(12).TextColor(t.TextMuted)
			})
			return
		}
		src := a.singleImg
		if src == nil {
			src = p.img // the thumbnail, while the larger one decodes
		}
		ui.Column(c).Fill().Center().Padding(0, 12, 12, 12).Children(func() {
			if src != nil {
				ui.Image(c, src).Grow(1).FillWidth().Fit(ui.Contain).MinHeight(0)
			} else {
				ui.Spinner(c)
			}
		})
	})
	if p != nil {
		c.OnWindowShortcut(0, ui.KeyLeft, func() { a.stepSingle(-1) })
		c.OnWindowShortcut(0, ui.KeyRight, func() { a.stepSingle(1) })
	}
}

// historyTags are the photos the single image view opened, on the left of
// its header, as tags that show one again. A tag outlives the folder the
// view shows, so opening an image elsewhere keeps the ones opened before.
func (a *app) historyTags(c *ui.Context) {
	for j, path := range a.history {
		j, name := j, filepath.Base(path)
		c.Key(j)
		btn := ui.Button(c, name).FontSize(12).BorderWidth(0).MaxWidth(140).
			Padding(6, 14).Radius(0).Tooltip(name).
			OnClick(func() { a.selectTag(j) })
		if j == a.tag {
			btn.Selected(true)
		}
	}
}

// showPhoto shows p alone in the single image view.
func (a *app) showPhoto(p *photo) {
	for i, candidate := range a.photos {
		if candidate == p {
			a.showPhotoAt(i)
			return
		}
	}
}

// showPhotoAt opens photos[i] in the single image view from the gallery
// or the rail: its tag becomes the current one, added or found, and the
// photo is chosen in the gallery.
func (a *app) showPhotoAt(i int) {
	if i < 0 || i >= len(a.photos) {
		return
	}
	p := a.photos[i]
	onTag := a.tag >= 0 && a.tag < len(a.history) && a.history[a.tag] == p.path
	if !onTag {
		at := -1
		for j := len(a.history) - 1; j >= 0; j-- {
			if a.history[j] == p.path {
				at = j
				break
			}
		}
		if at >= 0 {
			a.tag = at
		} else {
			a.addTag(p)
		}
	}
	a.setSingle(i)
}

// showInTag shows photos[i] in the single image view, renaming the tag
// the view is on rather than opening another. The arrows step through the
// folder's images this way, even when another tag shows one of them.
func (a *app) showInTag(i int) {
	if i < 0 || i >= len(a.photos) {
		return
	}
	if a.tag < 0 || a.tag >= len(a.history) {
		a.addTag(a.photos[i])
	} else {
		a.history[a.tag] = a.photos[i].path
	}
	a.setSingle(i)
}

// setSingle shows photos[i] in the single image view, and chooses it in
// the gallery.
func (a *app) setSingle(i int) {
	p := a.photos[i]
	a.mode = viewSingle
	a.single = i
	for j, item := range a.items {
		if item.path == p.path {
			a.chosen = j
			break
		}
	}
	a.loadSingle()
}

// addTag adds p to the header as the current tag, letting go of the
// oldest tag beyond historyLimit.
func (a *app) addTag(p *photo) {
	a.history = append(a.history, p.path)
	if len(a.history) > historyLimit {
		a.history = a.history[1:]
	}
	a.tag = len(a.history) - 1
}

// selectTag makes tag j the current one, showing its photo. A tag of
// another folder shows that folder first, and the photo once it loads.
func (a *app) selectTag(j int) {
	if j < 0 || j >= len(a.history) {
		return
	}
	a.tag = j
	if dir := filepath.Dir(a.history[j]); dir != a.selected {
		a.pendingTag = j
		a.selectDir(dir)
		return
	}
	if i := a.historyPhotoAt(j); i >= 0 {
		a.setSingle(i)
		return
	}
	a.pendingTag = j // the folder may still be loading
}

// historyPhotoAt is the index in photos of the photo tag j shows, or -1
// when the folder does not hold it.
func (a *app) historyPhotoAt(j int) int {
	if j < 0 || j >= len(a.history) {
		return -1
	}
	for i, p := range a.photos {
		if p.path == a.history[j] {
			return i
		}
	}
	return -1
}

// clearSingle drops the image the single image view decoded, and any
// decode still on its way no longer matters.
func (a *app) clearSingle() {
	a.singleGen++
	a.singlePath = ""
	a.singleImg = nil
	a.singleErr = nil
	a.singleLoading = false
}

// showSingle shows one image alone, the gallery's chosen photo when it is
// one and the folder's first otherwise.
func (a *app) showSingle() {
	if a.mode == viewSingle {
		return
	}
	a.mode = viewSingle
	if i := max(a.chosenPhoto(), 0); i < len(a.photos) {
		a.showPhotoAt(i)
		return
	}
	a.loadSingle() // an empty folder has nothing to show
}

// stepSingle shows the image delta places from the one shown, within the
// folder's photos, in the tag that shows it.
func (a *app) stepSingle(delta int) {
	n := a.single + delta
	if n < 0 || n >= len(a.photos) {
		return
	}
	a.showInTag(n)
}

// chosenPhoto is the index in photos of the chosen gallery item, or -1
// when it chose a folder or nothing.
func (a *app) chosenPhoto() int {
	if a.chosen < 0 || a.chosen >= len(a.items) {
		return -1
	}
	path := a.items[a.chosen].path
	for i, p := range a.photos {
		if p.path == path {
			return i
		}
	}
	return -1
}

// singlePhoto is the photo the single image view shows, or nil.
func (a *app) singlePhoto() *photo {
	if a.single < 0 || a.single >= len(a.photos) {
		return nil
	}
	return a.photos[a.single]
}

// loadSingle decodes the photo the single image view shows, large enough
// for the window, on a goroutine. The photograph already in the gallery
// shows meanwhile, when the grid had asked for its thumbnail.
func (a *app) loadSingle() {
	if a.single < 0 || a.single >= len(a.photos) {
		a.single = 0
	}
	p := a.singlePhoto()
	if p == nil {
		a.clearSingle()
		return
	}
	if p.path == a.singlePath && (a.singleLoading || a.singleImg != nil) {
		return // already decoded, or being decoded
	}
	a.singlePath = p.path
	a.singleGen++
	gen := a.singleGen
	a.singleImg = nil
	a.singleErr = nil
	a.singleLoading = true
	path := p.path
	a.async(func() {
		var (
			svg           *ui.SVG
			img           image.Image
			width, height int
			err           error
		)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			err = rerr
		} else if isSVG(path) {
			svg, err = ui.ParseSVG(data)
			if err == nil {
				if w, h := ui.ImageSize(svg); w > 0 && h > 0 {
					width, height = int(w+0.5), int(h+0.5)
				}
			}
		} else {
			img, width, height, err = decodeThumb(data, singleMaxPixels)
		}
		a.post(func() {
			if gen != a.singleGen {
				return // another image was chosen meanwhile
			}
			a.singleLoading = false
			if err != nil {
				log.Printf("decoding %s: %v", path, err)
				a.singleErr = err
				return
			}
			if svg != nil {
				a.singleImg = svg
			} else if img != nil {
				a.singleImg = ui.NewBitmap(img)
			}
			if p.width == 0 || p.height == 0 {
				p.width, p.height = width, height
			}
		})
	})
}
