package main

import (
	"image"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

// The single image view zooms its photo from fitting its box to
// maxSingleZoom times that, a step of singleZoomStep a notch of a wheel,
// and moves it around within the box while zoomed.
const (
	minSingleZoom  = 1
	maxSingleZoom  = 8
	singleZoomStep = 1.25
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
		src := a.singleSource()
		// Grow takes the room the header leaves, so that the photo's
		// box ends inside the window rather than past it, where its
		// scroll bars would not show. It touches the window's edges.
		ui.Column(c).Grow(1).MinHeight(0).Center().Children(func() {
			a.singlePhotoBox(c, src)
		})
	})
	if p != nil {
		c.OnWindowShortcut(0, ui.KeyLeft, func() { a.stepSingle(-1) })
		c.OnWindowShortcut(0, ui.KeyRight, func() { a.stepSingle(1) })
	}
	if a.tag >= 0 && a.tag < len(a.history) {
		c.OnWindowShortcut(0, ui.KeyEscape, func() { a.closeTag(a.tag) })
	}
}

// singleSource is the image the single image view shows: the decoded
// photo, or its thumbnail while the larger one decodes.
func (a *app) singleSource() ui.ImageSource {
	p := a.singlePhoto()
	if p == nil {
		return nil
	}
	if a.singleImg != nil {
		return a.singleImg
	}
	return p.img
}

// tagView is how the single image view showed the photo of one history
// tag: zoomed and scrolled, kept with the tag so that going back to it
// shows the same part again. Stepping to another image with the arrows or
// a double click fits it.
type tagView struct {
	zoom   float32
	scroll ui.ScrollState
}

// fitTagView fits the tag's photo to its room again: no zoom, and
// nothing out of sight to scroll to.
func fitTagView(v *tagView) {
	v.zoom = minSingleZoom
	v.scroll.X, v.scroll.Y = 0, 0
	v.scroll.MaxX, v.scroll.MaxY = 0, 0
}

// currentTagView is how the view shows the current tag's photo, or nil
// when no tag shows one.
func (a *app) currentTagView() *tagView {
	if a.tag < 0 || a.tag >= len(a.tagViews) {
		return nil
	}
	return &a.tagViews[a.tag]
}

// singlePhotoBox shows the photo in the room the view gives it, and
// takes the wheel, the fingers and the pointer that zoom and move it.
// Zoomed past the room, the photo scrolls in a container whose scroll
// bars show how much of it is out of sight.
func (a *app) singlePhotoBox(c *ui.Context, src ui.ImageSource) {
	room := a.singleRef.Bounds(c)
	view := a.currentTagView()
	box := ui.Box(c).Fill().MinHeight(0).Center().Bind(&a.singleRef).
		Cursor(ui.CursorGrab).
		HandleInput(func(ev ui.InputEvent) bool { return a.singleInput(c, ev) })
	if a.singleDragging {
		box.Cursor(ui.CursorGrabbing)
	}
	box.Children(func() {
		switch {
		case src == nil:
			ui.Spinner(c)
		case view == nil || view.zoom <= minSingleZoom || room.W <= 0 || room.H <= 0:
			// Fitting the room, as a photo opens: the first frame of the
			// window has no room to read yet.
			ui.Image(c, src).Fill().Fit(ui.Contain).MinHeight(0)
		default:
			a.zoomedPhoto(c, src, view.zoom, &view.scroll, room.W, room.H)
		}
	})
}

// zoomedPhoto shows the photo larger than the room w by h, in a scroll
// container tracked by s, whose content is the photo and the room where
// it is smaller.
func (a *app) zoomedPhoto(c *ui.Context, src ui.ImageSource, zoom float32, s *ui.ScrollState, w, h float32) {
	iw, ih := singleImageSize(src, zoom, w, h)
	ui.ScrollBoth(c).Fill().TrackScroll(s).KeepScrollbars(true).
		Children(func() {
			ui.Column(c).Shrink(0).Size(max(iw, w), max(ih, h)).Center().
				Children(func() {
					ui.Image(c, src).Size(iw, ih).Fit(ui.Contain)
				})
		})
}

// singleInput zooms the photo with the wheel and a pinch, and moves it
// with a drag of the pointer, so that a zoomed photo watches any part of
// itself. A touchpad's scroll scrolls the container around it, and the
// bars of that container drag it.
func (a *app) singleInput(c *ui.Context, ev ui.InputEvent) bool {
	view := a.currentTagView()
	if view == nil {
		return false
	}
	b := a.singleRef.Bounds(c)
	w, h := b.W, b.H
	switch ev.Kind {
	case ui.InputZoom:
		a.zoomSingleAt(view, ev.X, ev.Y, ev.Scale, w, h)
		return true
	case ui.InputScroll:
		switch {
		case ev.Precise: // the scroll container around the photo scrolls
			return false
		case ev.DY < 0: // a notch zooms in, the pointer's way
			a.zoomSingleAt(view, ev.X, ev.Y, singleZoomStep, w, h)
		case ev.DY > 0:
			a.zoomSingleAt(view, ev.X, ev.Y, 1/singleZoomStep, w, h)
		default:
			return false
		}
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		if ev.Clicks >= 2 {
			fitTagView(view) // a double click fits the photo again
		}
		a.singleDragging = true
		a.singleFrom = ui.Point{X: ev.X, Y: ev.Y}
		return true
	case ui.InputPointerMove:
		if !a.singleDragging || ev.Button != 0 {
			return false
		}
		a.panSingle(view, ev.X-a.singleFrom.X, ev.Y-a.singleFrom.Y, w, h)
		a.singleFrom = ui.Point{X: ev.X, Y: ev.Y}
		return true
	case ui.InputPointerUp:
		if !a.singleDragging {
			return false
		}
		a.singleDragging = false
		return true
	}
	return false
}

// zoomSingleAt zooms the tag's photo by factor about the point x, y of
// its room, w by h, keeping what shows there in place as the photo
// scrolls in its content.
func (a *app) zoomSingleAt(v *tagView, x, y, factor, w, h float32) {
	if factor <= 0 || w <= 0 || h <= 0 {
		return
	}
	zoom := min(max(v.zoom*factor, minSingleZoom), maxSingleZoom)
	if zoom == v.zoom {
		return
	}
	src := a.singleSource()
	cw, ch, iw, ih := singleContentSize(src, v.zoom, w, h)
	// The point shows at x + scrollX of the content, and ox from the
	// start of the photo, which is centered where it is smaller.
	ox, oy := (cw-iw)/2, (ch-ih)/2
	cx, cy := x+v.scroll.X, y+v.scroll.Y
	f := zoom / v.zoom
	v.zoom = zoom
	if zoom <= minSingleZoom {
		fitTagView(v)
		return
	}
	cw, ch, iw, ih = singleContentSize(src, zoom, w, h)
	scrollTag(v,
		(cw-iw)/2+(cx-ox)*f-x,
		(ch-ih)/2+(cy-oy)*f-y,
		cw, ch, w, h,
	)
}

// panSingle moves the tag's photo by dx, dy DIPs, as far as its zoom has
// room.
func (a *app) panSingle(v *tagView, dx, dy, w, h float32) {
	if v.zoom <= minSingleZoom {
		fitTagView(v)
		return
	}
	cw, ch, _, _ := singleContentSize(a.singleSource(), v.zoom, w, h)
	scrollTag(v, v.scroll.X-dx, v.scroll.Y-dy, cw, ch, w, h)
}

// scrollTag scrolls the tag's photo to x, y, as far as the content
// around it, cw by ch, reaches past the room w by h.
func scrollTag(v *tagView, x, y, cw, ch, w, h float32) {
	v.scroll.X = min(max(x, 0), max(cw-w, 0))
	v.scroll.Y = min(max(y, 0), max(ch-h, 0))
}

// singleContentSize is the size of the photo showing at src, zoomed, and
// of the content around it: the room w by h where the photo is smaller
// on an axis, as zoomedPhoto lays them out.
func singleContentSize(src ui.ImageSource, zoom, w, h float32) (cw, ch, iw, ih float32) {
	iw, ih = singleImageSize(src, zoom, w, h)
	return max(iw, w), max(ih, h), iw, ih
}

// singleImageSize is the size the photo showing at src has, zoomed, in
// the room w by h.
func singleImageSize(src ui.ImageSource, zoom, w, h float32) (float32, float32) {
	fw, fh := singleFitSize(src, w, h)
	return fw * zoom, fh * zoom
}

// singleFitSize is the size the photo showing at src fits the room w by h
// at, before it zooms.
func singleFitSize(src ui.ImageSource, w, h float32) (float32, float32) {
	iw, ih := ui.ImageSize(src)
	if iw <= 0 || ih <= 0 {
		return w, h // its proportions are not known yet
	}
	s := min(w/iw, h/ih)
	return iw * s, ih * s
}

// resetSingleZoom fits the current tag's photo to its room again, as
// stepping to another image and a double click do. Other tags keep where
// they were.
func (a *app) resetSingleZoom() {
	if v := a.currentTagView(); v != nil {
		fitTagView(v)
	}
	a.singleDragging = false
}

// historyTags are the photos the single image view opened, on the left of
// its header, as tags that show one again. A tag outlives the folder the
// view shows, so opening an image elsewhere keeps the ones opened before.
// Its close icon on the right drops it.
func (a *app) historyTags(c *ui.Context) {
	for j, path := range a.history {
		j, name := j, filepath.Base(path)
		on := j == a.tag
		c.Key(j)
		ui.Row(c).AlignItems(ui.Stretch).Gap(0).Children(func() {
			ui.Button(c, name).FontSize(12).BorderWidth(0).MaxWidth(140).
				Padding(6, 14).Radius(0).Tooltip(name).
				Selected(on).
				OnClick(func() { a.selectTag(j) })
			ui.Button(c, "").Label("Close "+name).Tooltip("Close this tag").
				BorderWidth(0).Padding(6, 8).Radius(0).
				Selected(on).
				OnClick(func() { a.closeTag(j) }).
				Children(func() {
					ui.Icon(c, closeIcon).Size(10, 10)
				})
		})
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
// the gallery. It shows it as the current tag showed it: only stepping to
// another image with the arrows fits it again.
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

// closeTag drops tag j from the header and the view kept with it.
// Closing the current tag shows the one opened before it, or the one
// after it when it was the first, and returns to the gallery when none
// is left. Closing another tag leaves the photo the view shows.
func (a *app) closeTag(j int) {
	if j < 0 || j >= len(a.history) {
		return
	}
	a.history = append(a.history[:j], a.history[j+1:]...)
	a.tagViews = append(a.tagViews[:j], a.tagViews[j+1:]...)
	switch {
	case a.pendingTag == j:
		a.pendingTag = -1
	case a.pendingTag > j:
		a.pendingTag--
	}
	switch {
	case len(a.history) == 0:
		a.tag = -1
		a.singleDragging = false
		a.showGallery()
	case j == a.tag:
		a.selectTag(max(j-1, 0))
	case a.tag > j:
		a.tag--
	}
}

// addTag adds p to the header as the current tag, letting go of the
// oldest tag beyond historyLimit and of the view kept with it.
func (a *app) addTag(p *photo) {
	a.history = append(a.history, p.path)
	a.tagViews = append(a.tagViews, tagView{zoom: minSingleZoom})
	if len(a.history) > historyLimit {
		a.history = a.history[1:]
		a.tagViews = a.tagViews[1:]
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
	a.singleDragging = false // the drag moved this tag's photo, not the next
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
// decode still on its way no longer matters. The zoom stays for the
// photo shown next.
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
// folder's photos, in the tag that shows it, fitting it again: the
// arrows are what let go of a zoom, as another photo has other things to
// watch.
func (a *app) stepSingle(delta int) {
	n := a.single + delta
	if n < 0 || n >= len(a.photos) {
		return
	}
	a.resetSingleZoom()
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
