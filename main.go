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
	folderIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7.5A1.5 1.5 0 0 1 4.5 6H9l2 2h8.5A1.5 1.5 0 0 1 21 9.5v8A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5z"/></svg>`))
	openIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2"/></svg>`))
	refreshIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 0 1-15.5 6.2L3 16"/><path d="M3 21v-5h5"/><path d="M3 12a9 9 0 0 1 15.5-6.2L21 8"/><path d="M21 3v5h-5"/></svg>`))
	pictureIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="8.5" cy="9.5" r="1.5"/><path d="m4 17 4.5-4.5L13 17l2.5-2.5L20 19"/></svg>`))
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

// app is the viewer: the directory tree on the left, the folder's images
// on the right. Its fields are read and changed on the main thread, except
// for state posted back through Window.Update.
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

// activateItem opens the chosen gallery item: a folder shows its contents,
// an image opens in the system's default app.
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
	if err := mygo.Shell.OpenPath(item.path); err != nil {
		log.Printf("opening %s: %v", item.path, err)
	}
}

// view builds the window: the tree and the gallery, side by side.
func (a *app) view(c *ui.Context) {
	ui.Split(c, &a.sidebarWidth, func() { a.sidebar(c) }, func() { a.gallery(c) }).Fill().SplitReset(250)
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
