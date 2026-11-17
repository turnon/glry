package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	folderIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7.5A1.5 1.5 0 0 1 4.5 6H9l2 2h8.5A1.5 1.5 0 0 1 21 9.5v8A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5z"/></svg>`))
	openIcon         = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2"/></svg>`))
	refreshIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 0 1-15.5 6.2L3 16"/><path d="M3 21v-5h5"/><path d="M3 12a9 9 0 0 1 15.5-6.2L21 8"/><path d="M21 3v5h-5"/></svg>`))
	pictureIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="8.5" cy="9.5" r="1.5"/><path d="m4 17 4.5-4.5L13 17l2.5-2.5L20 19"/></svg>`))
	treeIcon         = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M20 10a1 1 0 0 0 1-1V6a1 1 0 0 0-1-1h-2.5a1 1 0 0 1-.8-.4l-.9-1.2A1 1 0 0 0 15 3h-2a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1Z"/><path d="M20 21a1 1 0 0 0 1-1v-3a1 1 0 0 0-1-1h-2.5a1 1 0 0 1-.8-.4l-.9-1.2a1 1 0 0 0-.8-.4h-2a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1Z"/><path d="M3 5a2 2 0 0 0 2 2h3"/><path d="M3 3v13a2 2 0 0 0 2 2h3"/></svg>`))
	searchIcon       = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.2-3.2"/></svg>`))
	chevronLeftIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>`))
	chevronRightIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`))
)

// historyLimit is how many photos the single image view's header tags
// hold, letting go of the ones shown first.
const historyLimit = 5

// railWidth is the width of the bar that chooses the view.
const railWidth = 48

// singleMaxPixels is the longest side of the bitmap the single image view
// decodes: sharp on a large window without holding a photo's full size in
// memory.
const singleMaxPixels = 2048

// viewMode is what the window shows beside the rail: the folder's tree
// and gallery, or one image alone.
type viewMode uint8

const (
	viewGallery viewMode = iota
	viewSingle
)

// thumbMaxPixels is the longest side of the bitmaps the gallery decodes:
// enough for a cell on a high-density display, small enough that a folder
// of photos does not hold their full size in memory.
const thumbMaxPixels = 512

// thumbCacheLimit is how many thumbnails the app keeps. Scrolling far
// through a large folder lets go of the ones loaded first rather than
// holding every image seen.
const thumbCacheLimit = 240

// thumbState is where a photo's thumbnail is in its loading.
type thumbState uint8

const (
	thumbIdle thumbState = iota
	thumbLoading
	thumbReady
	thumbFailed
)

// photo is an image in the folder shown, with its thumbnail once decoded.
type photo struct {
	path, name string
	size       int64
	width      int
	height     int
	state      thumbState
	img        ui.ImageSource
}

// galleryItem is a folder or an image of the selected folder's gallery.
type galleryItem struct {
	path, name string
	dir        bool
	photo      *photo
}

// app is the viewer: a rail chooses between the folder's tree and images
// and one image alone. Its fields are read and changed on the main thread,
// except for state posted back through Window.Update.
type app struct {
	win *mygo.Window

	// root is the directory the tree starts at, and selected the folder
	// whose images show.
	root     string
	selected string

	// dirs caches each directory's subdirectories; a nil slice is a
	// directory without any. dirsLoading keeps a directory from being
	// scanned twice while it loads.
	dirs        map[string][]string
	dirsLoading map[string]bool
	tree        ui.OutlineState[string]
	dirRow      int
	// pendingTreePath is a folder the gallery chose, which the tree
	// selects once its row is built.
	pendingTreePath string

	// items is the gallery: the selected folder's subdirectories first,
	// then its images. photos is those images alone, for the size tag.
	// galleryGen tells the results of a folder left meanwhile to drop
	// themselves.
	items          []galleryItem
	photos         []*photo
	thumbOrder     []*photo
	galleryGen     uint64
	galleryLoading bool
	galleryErr     error
	grid           ui.GridState
	chosen         int

	// mode chooses between the tree and gallery and the single image
	// view; single is the index into photos that view shows, and
	// singleImg its decoded bitmap, which singleGen tells the results of
	// an image left meanwhile to drop.
	mode          viewMode
	single        int
	singleImg     ui.ImageSource
	singleGen     uint64
	singlePath    string
	singleErr     error
	singleLoading bool

	// history is the path of each photo the single image view's header
	// tags, in the order they opened, and tag is the index of the tag the
	// view is on. The arrows rename that tag instead of adding one, so two
	// tags can show the same photo.
	history []string
	tag     int

	sidebarWidth float32
	cellWidth    float32
	cellHeight   float32
	thumbSlots   chan struct{}

	// async runs slow work off the main thread. Tests make it run inline.
	async func(func())
}

func newApp(root string) *app {
	a := &app{
		root:         root,
		selected:     root,
		dirs:         map[string][]string{},
		dirsLoading:  map[string]bool{},
		chosen:       -1,
		tag:          -1,
		sidebarWidth: 250,
		thumbSlots:   make(chan struct{}, 4),
	}
	a.setCellSize(defaultCellSize())
	a.async = func(fn func()) { go fn() }
	return a
}

// setCellSize sets the size of a gallery cell.
func (a *app) setCellSize(size cellSize) {
	a.cellWidth = size.width
	a.cellHeight = size.height
}

// start loads the tree's root and its images. Call it once the window is
// set, so that results from goroutines reach the main thread through it.
func (a *app) start() { a.setRoot(a.root) }

// post runs fn on the main thread and redraws the window.
func (a *app) post(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

// setRoot makes dir the tree's root, as choosing another folder does.
func (a *app) setRoot(dir string) {
	if dir == "" {
		return
	}
	a.root = dir
	a.selected = dir
	a.dirRow = 0
	a.pendingTreePath = ""
	a.dirs = map[string][]string{}
	a.dirsLoading = map[string]bool{}
	a.tree = ui.OutlineState[string]{}
	a.tree.List.Selected = &a.dirRow
	a.tree.List.Label = func(i int) string {
		if i < 0 || i >= a.tree.Rows() {
			return ""
		}
		return filepath.Base(a.tree.Item(i))
	}
	a.tree.Open.Add(dir)
	a.tree.Generation = 1
	a.loadChildren(dir)
	a.loadImages()
}

// children gives the outline a directory's subdirectories. A directory
// not scanned yet shows an arrow, so it can be opened, and is scanned as
// it opens.
func (a *app) children(dir string) []string {
	if kids, ok := a.dirs[dir]; ok {
		return kids
	}
	if dir == a.root || a.tree.Open.Has(dir) {
		a.loadChildren(dir)
		if kids, ok := a.dirs[dir]; ok {
			return kids
		}
	}
	return []string{} // not scanned: it may have children
}

// loadChildren scans a directory's subdirectories on a goroutine.
func (a *app) loadChildren(dir string) {
	if _, ok := a.dirs[dir]; ok || a.dirsLoading[dir] {
		return
	}
	a.dirsLoading[dir] = true
	a.async(func() {
		kids, err := scanDirs(dir)
		if err != nil {
			log.Printf("reading %s: %v", dir, err)
			kids = nil
		}
		a.post(func() {
			delete(a.dirsLoading, dir)
			a.dirs[dir] = kids
			a.tree.Generation++
			if dir == a.selected {
				a.buildGalleryItems()
			}
		})
	})
}

// selectDir shows dir's subdirectories and images, opening it in the tree.
func (a *app) selectDir(dir string) {
	if dir == "" {
		return
	}
	a.tree.Open.Add(dir)
	a.revealPath(dir)
	if dir == a.selected {
		return
	}
	a.selected = dir
	a.loadImages()
}

// revealPath opens dir's ancestors under the root and asks the tree to
// select it once its row is built.
func (a *app) revealPath(dir string) {
	a.pendingTreePath = dir
	a.tree.Open.Add(a.root)
	if dir == a.root {
		return
	}
	rel, err := filepath.Rel(a.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	cur := a.root
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		a.tree.Open.Add(cur)
		a.loadChildren(cur)
	}
}

// applyPendingTreeSelection selects the folder the gallery chose, now that
// the outline has built its rows.
func (a *app) applyPendingTreeSelection(c *ui.Context) {
	if a.pendingTreePath == "" {
		return
	}
	for i := 0; i < a.tree.Rows(); i++ {
		if a.tree.Item(i) != a.pendingTreePath {
			continue
		}
		a.pendingTreePath = ""
		if a.dirRow != i {
			a.dirRow = i
			a.tree.List.ScrollIntoView(i)
			c.Invalidate()
		}
		return
	}
}

// loadImages scans the selected folder for subdirectories and images on a
// goroutine.
func (a *app) loadImages() {
	dir := a.selected
	a.galleryGen++
	gen := a.galleryGen
	a.items = nil
	a.photos = nil
	a.thumbOrder = nil
	a.grid = ui.GridState{}
	a.chosen = -1
	a.galleryErr = nil
	a.galleryLoading = dir != ""
	if dir == "" {
		return
	}
	// The photos of the folder left show no more, nor does its image.
	a.single = 0
	a.history = nil
	a.tag = -1
	a.clearSingle()
	a.async(func() {
		dirs, photos, err := scanGallery(dir)
		a.post(func() {
			if gen != a.galleryGen {
				return // another folder was chosen meanwhile
			}
			a.galleryLoading = false
			if err != nil {
				a.galleryErr = err
				return
			}
			a.dirs[dir] = dirs
			a.tree.Generation++
			a.photos = photos
			a.buildGalleryItems()
			a.chosen = -1
			if a.mode == viewSingle {
				// The view waited for the folder: show its first image.
				if len(a.photos) > 0 {
					a.showPhotoAt(0)
				} else {
					a.loadSingle()
				}
			}
		})
	})
}

// buildGalleryItems puts the selected folder's subdirectories before its
// images.
func (a *app) buildGalleryItems() {
	dirs := a.dirs[a.selected]
	a.items = make([]galleryItem, 0, len(dirs)+len(a.photos))
	for _, dir := range dirs {
		a.items = append(a.items, galleryItem{path: dir, name: rootName(dir), dir: true})
	}
	for _, p := range a.photos {
		a.items = append(a.items, galleryItem{path: p.path, name: p.name, photo: p})
	}
}

// reload scans the selected folder and its subdirectories again.
func (a *app) reload() {
	if a.selected == "" {
		return
	}
	delete(a.dirs, a.selected)
	a.tree.Generation++
	a.loadChildren(a.selected)
	a.loadImages()
}

// requestThumb decodes a photo's thumbnail on a goroutine, once. The grid
// asks only for the items it builds, which are those in view, so a large
// folder decodes the thumbnails the user sees rather than all of them.
func (a *app) requestThumb(gen uint64, p *photo) {
	if p == nil || p.state != thumbIdle {
		return
	}
	p.state = thumbLoading
	path := p.path
	a.async(func() {
		a.thumbSlots <- struct{}{}
		defer func() { <-a.thumbSlots }()

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
			img, width, height, err = decodeThumb(data, thumbMaxPixels)
		}
		a.post(func() {
			if gen != a.galleryGen {
				return // the folder changed while it decoded
			}
			if err != nil {
				log.Printf("decoding %s: %v", path, err)
				p.state = thumbFailed
				return
			}
			p.state = thumbReady
			p.width, p.height = width, height
			if svg != nil {
				p.img = svg
			} else if img != nil {
				p.img = ui.NewBitmap(img)
			}
			a.rememberThumb(p)
		})
	})
}

// rememberThumb keeps the thumbnails loaded latest, letting go of the
// oldest beyond the cache limit. A cell still showing one of those asks
// for it again in the next frame.
func (a *app) rememberThumb(p *photo) {
	a.thumbOrder = append(a.thumbOrder, p)
	if len(a.thumbOrder) <= thumbCacheLimit {
		return
	}
	old := a.thumbOrder[0]
	a.thumbOrder[0] = nil
	a.thumbOrder = a.thumbOrder[1:]
	if old != p {
		old.img = nil
		old.state = thumbIdle
	}
}

// chooseFolder asks for another tree root, on a goroutine so that the
// native dialog's wait does not hold up the view.
func (a *app) chooseFolder() {
	win := a.win
	if win == nil {
		return
	}
	start := a.root
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       "Choose a folder",
			DefaultPath: start,
			Directory:   true,
			ButtonLabel: "Open",
		})
		if err != nil {
			log.Printf("choosing a folder: %v", err)
			return
		}
		if len(paths) == 0 {
			return
		}
		a.post(func() { a.setRoot(paths[0]) })
	}()
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

// selectTag makes tag j the current one, showing its photo.
func (a *app) selectTag(j int) {
	if j < 0 || j >= len(a.history) {
		return
	}
	p := a.historyPhoto(a.history[j])
	if p == nil {
		return
	}
	for i, candidate := range a.photos {
		if candidate == p {
			a.tag = j
			a.setSingle(i)
			return
		}
	}
}

// historyPhoto is the photo at path among the folder's, or nil.
func (a *app) historyPhoto(path string) *photo {
	for _, p := range a.photos {
		if p.path == path {
			return p
		}
	}
	return nil
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

// showGallery returns to the tree and the folder's gallery.
func (a *app) showGallery() {
	if a.mode == viewGallery {
		return
	}
	a.mode = viewGallery
	a.clearSingle()
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

// view builds the window: the rail that chooses the view, and beside it
// the folder's tree and gallery, or one image alone.
func (a *app) view(c *ui.Context) {
	ui.Row(c).Fill().Children(func() {
		a.rail(c)
		ui.Divider(c)
		if a.mode == viewSingle {
			a.singleView(c)
			return
		}
		ui.Split(c, &a.sidebarWidth, func() { a.sidebar(c) }, func() { a.gallery(c) }).Fill().SplitReset(250)
	})
}

// rail is the narrow bar of icons that chooses what the window shows.
func (a *app) rail(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(railWidth).FillHeight().Padding(8, 0).Gap(6).AlignItems(ui.Center).Background(t.Background).Children(func() {
		a.railButton(c, "File tree", "Show the file tree and gallery", treeIcon, a.mode == viewGallery, a.showGallery)
		a.railButton(c, "Single image", "Show one image at a time", searchIcon, a.mode == viewSingle, a.showSingle)
	})
}

// railButton is one icon of the rail, drawn as chosen when its view is the
// one showing.
func (a *app) railButton(c *ui.Context, label, tooltip string, icon *ui.SVG, active bool, onClick func()) {
	t := c.Theme()
	btn := ui.Button(c, "").Label(label).Tooltip(tooltip).BorderWidth(0).Size(32, 32).
		OnClick(onClick)
	btn.Children(func() {
		ui.Icon(c, icon).Size(18, 18)
	})
	btn.Selected(active)
	if !active {
		btn.TextColor(t.TextMuted)
	}
}

// singleView shows one photo of the folder, as large as the window allows.
func (a *app) singleView(c *ui.Context) {
	t := c.Theme()
	p := a.singlePhoto()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if len(a.history) > 0 {
			// The tags fill the header's height and touch, as the tabs
			// of a tab bar do.
			ui.Row(c).Padding(0, 10).AlignItems(ui.Stretch).Wrap().Children(func() {
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
		ui.Column(c).Fill().Center().Padding(12).Children(func() {
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
// its header, as tags that show one again.
func (a *app) historyTags(c *ui.Context) {
	for j, path := range a.history {
		p := a.historyPhoto(path)
		if p == nil {
			continue
		}
		j, name := j, p.name
		c.Key(j)
		btn := ui.Button(c, name).FontSize(12).BorderWidth(0).MaxWidth(140).
			Padding(6, 14).Radius(0).Tooltip(name).
			OnClick(func() { a.selectTag(j) })
		if j == a.tag {
			btn.Selected(true)
		}
	}
}

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

// rootName is the name a directory shows by: its last element, or itself
// at a root such as "/".
func rootName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return path
	}
	return name
}

// byteSize formats a size in bytes, as 12.4 MB.
func byteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(1024), 0
	for n >= div*1024 && exp < 3 {
		div *= 1024
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGT"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGT"[exp])
}

func imageCount(n int) string {
	if n == 1 {
		return "1 image"
	}
	return fmt.Sprintf("%d images", n)
}

// defaultRoot starts at a folder given on the command line, else the
// user's Pictures, else their home.
func defaultRoot() string {
	if len(os.Args) > 1 {
		if abs, err := filepath.Abs(os.Args[1]); err == nil {
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				return abs
			}
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		for _, name := range []string{"Pictures", "Photos"} {
			dir := filepath.Join(home, name)
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				return dir
			}
		}
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func main() {
	a := newApp(defaultRoot())
	a.setCellSize(loadConfig(configPath()))
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "glry",
			Width:     1040,
			Height:    700,
			MinWidth:  640,
			MinHeight: 420,
			// Opens where the user left it last time.
			StateKey: "main",
			// The window shows the interface MyGo draws, not a web page.
			Content: ui.View(a.view),
		})
		a.start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
