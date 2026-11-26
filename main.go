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
	configIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2Z"/><circle cx="12" cy="12" r="3"/></svg>`))
	pictureIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="8.5" cy="9.5" r="1.5"/><path d="m4 17 4.5-4.5L13 17l2.5-2.5L20 19"/></svg>`))
	treeIcon    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M20 10a1 1 0 0 0 1-1V6a1 1 0 0 0-1-1h-2.5a1 1 0 0 1-.8-.4l-.9-1.2A1 1 0 0 0 15 3h-2a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1Z"/><path d="M20 21a1 1 0 0 0 1-1v-3a1 1 0 0 0-1-1h-2.5a1 1 0 0 1-.8-.4l-.9-1.2a1 1 0 0 0-.8-.4h-2a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1Z"/><path d="M3 5a2 2 0 0 0 2 2h3"/><path d="M3 3v13a2 2 0 0 0 2 2h3"/></svg>`))
	searchIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.2-3.2"/></svg>`))
	eyeIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg>`))
	closeIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`))
)

// railWidth is the width of the bar that chooses the view.
const railWidth = 48

// singleMaxPixels is the longest side of the bitmap the single image view
// decodes: sharp on a large window without holding a photo's full size in
// memory.
const singleMaxPixels = 2048

// viewMode is what the window shows beside the rail: the folders view,
// the search, one image alone, or the config.
type viewMode uint8

const (
	viewFolders viewMode = iota
	viewSearch
	viewSingle
	viewConfig
)

// thumbMaxPixels is the longest side of the bitmaps the grid decodes:
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

// folderItem is an item of the folders view: a subfolder or an image of
// the selected folder.
type folderItem struct {
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
	// nameWidths caches how wide a tree row's label draws, for the row
	// width that lets the tree scroll sideways when one does not fit.
	nameWidths map[string]float32
	// treeRowW is the row width the tree last applied, so that the frame
	// showing wider rows asks for another to measure them.
	treeRowW float32
	dirRow   int
	// pendingTreePath is a folder the folders view chose, which the tree
	// selects once its row is built. pendingPhotoPath is a photo the single
	// image view was on, which the folders view shows with its folder and
	// chooses among its items once they load. revealChosen is that item,
	// which the grid scrolls into view, and revealLaid tells that the grid
	// offered it the width it had before the folder loaded, so that the
	// scroll waits for the width it has now.
	pendingTreePath  string
	pendingPhotoPath string
	revealChosen     int
	revealLaid       bool

	// items is the folders view's grid: the selected folder's
	// subdirectories first, then its images. photos is those images
	// alone, for the size tag. foldersGen tells the results of a folder
	// left meanwhile to drop themselves.
	items          []folderItem
	photos         []*photo
	thumbOrder     []*photo
	foldersGen     uint64
	foldersLoading bool
	foldersErr     error
	grid           ui.GridState
	chosen         int

	// folderPick and searchPick hold each grid's choice of several items,
	// as Cmd-click and a drag over it choose them.
	folderPick gridPick
	searchPick gridPick

	// mode chooses between the folders view, the search, the single
	// image view and the config. singlePhotos is the images the single
	// view's arrows step through, and single the index into them: the
	// selected folder's photos, or the search results' when a result
	// opened the view there, so that the folders view keeps its own.
	// singleImg is the photo's decoded bitmap, which singleGen tells the
	// results of an image left meanwhile to drop.
	mode          viewMode
	singlePhotos  []*photo
	single        int
	singleImg     ui.ImageSource
	singleGen     uint64
	singlePath    string
	singleErr     error
	singleLoading bool

	// tagViews is how the single image view shows the photo of each
	// history tag, zoomed and scrolled, in step with history. singleRef is
	// the room the photo has, and singleDragging tells that the pointer
	// moves the photo, from singleFrom.
	tagViews       []tagView
	singleRef      ui.Handle
	singleDragging bool
	singleFrom     ui.Point

	// singleDirPhotos is the images of the directory the single image
	// view's photo is in, for its footer; singleDir names that directory
	// and singleDirGen tells the scan of a directory left meanwhile to
	// drop.
	singleDir       string
	singleDirPhotos []*photo
	singleDirGen    uint64

	// history is the path of each photo the single image view's header
	// tags, in the order they opened, and tag is the index of the tag the
	// view is on. The arrows rename that tag instead of adding one, so two
	// tags can show the same photo. Tags outlive the folder the view shows;
	// pendingTag is the tag whose photo a loading folder has yet to show.
	// tagScroll is how far the header's tags scrolled sideways, in the room
	// tagsRef measures, and tagShown the tag they last brought into view.
	history    []string
	tag        int
	pendingTag int
	tagScroll  ui.ScrollState
	tagsRef    ui.Handle
	tagShown   int

	// searcher is the backend the search view asks: gmbd's HTTP service or
	// the SQLite vector database. gmbdURL is that service's address, and
	// sqlitePath the database, with embedURL/embedModel the service that
	// embeds a query for the sqlite way. searchQuery and searchScope are its
	// inputs, and searchPage the page it asks next. searchResults is what the
	// search has found so far, with searchTotal how many it counted in all,
	// and searchGen tells the answer of a search run meanwhile to drop.
	// searchingMore and searchedNoMore tell that another page is on its
	// way and that the backend has no more. searchScroll keeps the results'
	// place, and searchGrid and searchChosen are the grid and its choice.
	searcher       searcher
	gmbdURL        string
	sqlitePath     string
	embedURL       string
	embedModel     string
	searchQuery    string
	searchScope    string
	searchPage     int
	searchResults  []searchResult
	searchTotal    int
	searchErr      string
	searchLoading  bool
	searchingMore  bool
	searchedNoMore bool
	searchGen      uint64
	searchScroll   ui.ScrollState
	searchAtY      float32 // the scroll offset loading more last considered
	searchLaid     int     // len(searchResults) the grid last laid out, -1 before
	searchGrid     ui.GridState
	searchChosen   int

	// searchGroup tells that the results show grouped by directory. The
	// groups hold each directory's loaded results, most matched first, and
	// searchGroupFlat is those results in the order the rows show them.
	// searchGroupRows keeps each directory's sideways place and the batch
	// it shows, searchGroupList keeps the rows' place, and
	// searchGroupChosen is the grouped image chosen. searchScoreAsc tells
	// that a lower score matches better, as the sqlite way's distance does.
	searchGroup       bool
	searchScoreAsc    bool
	searchGroups      []searchGroup
	searchGroupFlat   []int
	searchGroupChosen int
	searchGroupRows   map[string]*searchDirRow
	searchGroupList   ui.ListState

	// indexPath is the image or folder the last index added, indexCount how
	// many images it handled, and indexTotal how many it has to handle.
	// indexSkipped is how many of those it left out of the index.
	// indexRunning tells whether it is still going, and indexQueue holds the
	// paths, with whether each reaches the folders under it, added while it
	// runs. indexErr says why it could not start or stopped, and indexGen
	// tells the progress of an index started meanwhile to drop.
	indexPath    string
	indexCount   int
	indexTotal   int
	indexSkipped int
	indexQueue   []indexTarget
	indexRunning bool
	indexErr     string
	indexGen     uint64

	// removePath is the image or folder the last Remove moved to the trash,
	// removeRunning tells whether it is still going, and removeErr says why
	// it stopped. removeGen tells the result of a removal started meanwhile
	// to drop, and removePending the paths a frame still has to take out of
	// the folders view and the tree.
	removePath    string
	removeRunning bool
	removeErr     string
	removeGen     uint64
	removePending []removeTarget

	// config is the config view's form, in config.json's shape, and
	// configPath is where Save writes it. configErr says what Save could
	// not read; configStatus says what Save did, and configStatusErr
	// whether it failed.
	config          configFile
	configPath      string
	configErr       string
	configStatus    string
	configStatusErr bool

	sidebarWidth float32
	cellWidth    float32
	cellHeight   float32
	thumbSlots   chan struct{}

	// async runs slow work off the main thread. Tests make it run inline.
	async func(func())

	// trash moves a path to the trash. Tests replace it, as the real one
	// needs the running app.
	trash func(string) error

	// pickImage asks the user for an image to search by, as the native file
	// dialog does. Tests replace it, as the dialog needs the running app.
	pickImage func(*mygo.Window) ([]string, error)
}

func newApp(root string) *app {
	d := defaultSettings()
	a := &app{
		root:              root,
		selected:          root,
		dirs:              map[string][]string{},
		dirsLoading:       map[string]bool{},
		nameWidths:        map[string]float32{},
		treeRowW:          -1,
		chosen:            -1,
		revealChosen:      -1,
		folderPick:        gridPick{pivot: -1},
		searchPick:        gridPick{pivot: -1},
		tag:               -1,
		pendingTag:        -1,
		tagShown:          -1,
		gmbdURL:           gmbdEndpoint,
		sqlitePath:        defaultSQLitePath(),
		embedURL:          d.embedURL,
		embedModel:        d.embedModel,
		searchPage:        1,
		searchLaid:        -1,
		searchChosen:      -1,
		searchGroupChosen: -1,
		searchGroupRows:   map[string]*searchDirRow{},
		sidebarWidth:      250,
		thumbSlots:        make(chan struct{}, 4),
		trash:             mygo.Shell.TrashItem,
		pickImage: func(parent *mygo.Window) ([]string, error) {
			return mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent:  parent,
				Title:   "Search by image",
				Message: "Choose an image to find similar ones",
				Filters: []mygo.FileFilter{{Name: "Images", Extensions: embeddingImageExtensions}},
			})
		},
	}
	a.setSearcher(d.searchWay)
	a.setCellSize(d.cell)
	a.config = defaultConfigFile()
	a.async = func(fn func()) { go fn() }
	return a
}

// setCellSize sets the size of a grid cell.
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
				a.buildFolderItems()
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
	if !underRoot(a.root, dir) {
		return
	}
	rel, err := filepath.Rel(a.root, dir)
	if err != nil {
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

// applyPendingTreeSelection selects the folder the folders view chose, now that
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
	a.foldersGen++
	gen := a.foldersGen
	a.items = nil
	a.photos = nil
	a.thumbOrder = nil
	a.grid = ui.GridState{}
	a.chosen = -1
	a.revealChosen, a.revealLaid = -1, false
	a.folderPick = gridPick{pivot: -1}
	a.foldersErr = nil
	a.foldersLoading = dir != ""
	if dir == "" {
		return
	}
	// The folder left shows no image, though its tags stay, showing the
	// photos opened so far.
	a.singlePhotos = nil
	a.single = 0
	a.clearSingle()
	a.async(func() {
		dirs, photos, err := scanFolder(dir)
		a.post(func() {
			if gen != a.foldersGen {
				return // another folder was chosen meanwhile
			}
			a.foldersLoading = false
			if err != nil {
				a.foldersErr = err
				return
			}
			a.dirs[dir] = dirs
			a.tree.Generation++
			a.photos = photos
			a.buildFolderItems()
			a.chosen = -1
			if path := a.pendingPhotoPath; path != "" {
				a.pendingPhotoPath = ""
				if a.choosePhotoPath(path) {
					a.revealChosen, a.revealLaid = a.chosen, false
				}
			}
			j := a.pendingTag
			a.pendingTag = -1
			if a.mode == viewSingle && (j >= 0 || a.singleShowsFolder(dir)) {
				// The view waited for the folder: show the photo its tag
				// chose, else the one the current tag shows, else the
				// folder's first image.
				if a.historyPhotoAt(j) < 0 {
					j = a.tag
				}
				if i := a.historyPhotoAt(j); i >= 0 {
					a.tag = j
					a.setSingle(i)
				} else if len(a.photos) > 0 {
					a.showPhotoAt(0)
				} else {
					a.loadSingle()
				}
			}
		})
	})
}

// buildFolderItems puts the selected folder's subdirectories before its
// images.
func (a *app) buildFolderItems() {
	dirs := a.dirs[a.selected]
	a.items = make([]folderItem, 0, len(dirs)+len(a.photos))
	for _, dir := range dirs {
		a.items = append(a.items, folderItem{path: dir, name: rootName(dir), dir: true})
	}
	for _, p := range a.photos {
		a.items = append(a.items, folderItem{path: p.path, name: p.name, photo: p})
	}
	a.folderPick.handles = make([]ui.Handle, len(a.items))
	a.folderPick.built = a.folderPick.built[:0]
}

// requestThumb decodes a photo's thumbnail on a goroutine, once. The grid
// asks only for the items it builds, which are those in view, so a large
// folder decodes the thumbnails the user sees rather than all of them.
// valid says whether the result still matters when it arrives.
func (a *app) requestThumb(valid func() bool, p *photo) {
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
			if !valid() {
				return // the folder or search changed while it decoded
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

// view builds the window: the rail that chooses the view, and beside it
// the folders view, the search, one image alone, or the config.
func (a *app) view(c *ui.Context) {
	a.applyRemoved()
	ui.Row(c).Fill().Children(func() {
		a.rail(c)
		ui.Divider(c)
		switch a.mode {
		case viewSearch:
			a.searchView(c)
		case viewSingle:
			a.singleView(c)
		case viewConfig:
			a.configView(c)
		default:
			ui.Split(c, &a.sidebarWidth, func() { a.sidebar(c) }, func() { a.folders(c) }).Fill().SplitReset(250)
		}
	})
}

// rail is the narrow bar of icons that chooses what the window shows.
func (a *app) rail(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(railWidth).FillHeight().Padding(8, 0).Gap(6).AlignItems(ui.Center).Background(t.Background).Children(func() {
		a.railButton(c, "Folders", "Show folders and their images", treeIcon, a.mode == viewFolders, a.showFolders)
		a.railButton(c, "Search", "Search the folder's images", searchIcon, a.mode == viewSearch, a.showSearch)
		a.railButton(c, "Single image", "Show one image at a time", eyeIcon, a.mode == viewSingle, a.showSingle)
		a.railButton(c, "Config", "Edit the config", configIcon, a.mode == viewConfig, a.showConfig)
	})
}

// railButton is one icon of the rail, drawn as chosen when its view is the
// one showing. A button of the rail has no face of its own, so that the
// rail is its icons and the view showing; it takes the theme's face only
// under the pointer.
func (a *app) railButton(c *ui.Context, label, tooltip string, icon *ui.SVG, active bool, onClick func()) {
	t := c.Theme()
	btn := ui.ButtonBase(c).Label(label).Tooltip(tooltip).Size(32, 32).
		Radius(t.Radius).OnClick(onClick)
	btn.Children(func() {
		ui.Icon(c, icon).Size(18, 18)
	})
	btn.Selected(active)
	if !active {
		btn.TextColor(t.TextMuted)
		switch {
		case btn.Pressed():
			btn.Background(t.SurfacePressed)
		case btn.Hovered():
			btn.Background(t.SurfaceHover)
		}
	}
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

// defaultRoot starts at a folder given on the command line, else the
// user's home.
func defaultRoot() string {
	if len(os.Args) > 1 {
		if abs, err := filepath.Abs(os.Args[1]); err == nil {
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				return abs
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func main() {
	a := newApp(defaultRoot())
	a.loadSettings(configPath())
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
