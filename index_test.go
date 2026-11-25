package main

import (
	"database/sql"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// indexColor gives each image a distinct color, so that tests can tell them
// apart.
func indexColor(i int) color.Color {
	return color.RGBA{R: uint8(40 * i), G: uint8(20 * i), B: uint8(10 * i), A: 255}
}

func TestIndexFromAppFolderTreeMenu(t *testing.T) {
	dir := t.TempDir()
	trips := filepath.Join(dir, "Trips")
	nested := filepath.Join(trips, "Beach")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(trips, "sunset.png"), indexColor(3))
	writePNG(t, filepath.Join(nested, "beach.png"), indexColor(4))

	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(dir, "db.sqlite")
	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.RightClick("Trips"); err != nil {
		t.Fatal(err)
	}
	items := tt.Menu()
	index, recursively := slices.Index(items, "Index"), slices.Index(items, "Index Recursively")
	if index < 0 || recursively < 0 {
		t.Fatalf("the folder's menu is %q, want Index and Index Recursively", items)
	}
	if index > recursively {
		t.Errorf("the folder's menu is %q, want Index above Index Recursively", items)
	}
	if err := tt.ChooseMenuItem("Index"); err != nil {
		t.Fatal(err)
	}
	if a.indexCount != 1 {
		t.Errorf("the menu indexed %d images, want the folder's own 1", a.indexCount)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("the database holds %d images, want 1", count)
	}

	if err := tt.RightClick("Trips"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Index Recursively"); err != nil {
		t.Fatal(err)
	}
	if a.indexCount != 2 {
		t.Errorf("the menu indexed %d images, want the folder's 2", a.indexCount)
	}
	if err := db.QueryRow(`select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("the database holds %d images, want 2", count)
	}
}

func TestIndexFromAppGalleryImageMenu(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "beach.png"), indexColor(5))

	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(dir, "db.sqlite")
	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.RightClick("beach.png"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	if !slices.Contains(menu, "Index") {
		t.Fatalf("the image's menu is %q, want an Index item", menu)
	}
	if slices.Contains(menu, "Index Recursively") {
		t.Errorf("the image's menu is %q, want no Index Recursively item", menu)
	}
	if err := tt.ChooseMenuItem("Index"); err != nil {
		t.Fatal(err)
	}
	if a.indexCount != 1 {
		t.Errorf("the menu indexed %d images, want 1", a.indexCount)
	}
}

// queueIndexSearcher reports each path an index starts on, with whether it
// reaches the folders under it, and finishes when the test lets it.
type queueIndexSearcher struct {
	started chan indexTarget
	finish  chan struct{}
}

func (s *queueIndexSearcher) search(query, scope string, page int) ([]searchResult, int, error) {
	return nil, 0, nil
}

func (s *queueIndexSearcher) index(path string, recursive bool) (chan indexProgress, error) {
	progress := make(chan indexProgress)
	s.started <- indexTarget{path: path, recursive: recursive}
	go func() {
		<-s.finish
		close(progress)
	}()
	return progress, nil
}

func (s *queueIndexSearcher) remove(path string) error { return nil }

// galleryRightClick right-clicks the folders view's cell named name, not
// the tree row that shows the same folder.
func TestIndexQueuesFoldersWhileRunning(t *testing.T) {
	a := newApp(t.TempDir())
	searcher := &queueIndexSearcher{
		started: make(chan indexTarget, 4),
		finish:  make(chan struct{}),
	}
	a.searcher = searcher
	done := make(chan struct{}, 4)
	a.async = func(fn func()) {
		go func() {
			fn()
			done <- struct{}{}
		}()
	}

	a.startIndex("first", true)
	if got := <-searcher.started; got.path != "first" || !got.recursive {
		t.Fatalf("the index started %q recursively %v, want first recursively", got.path, got.recursive)
	}
	a.startIndex("second", false)
	if a.indexPath != "first" || len(a.indexQueue) != 1 || a.indexQueue[0] != (indexTarget{path: "second"}) {
		t.Fatalf("the second folder queued as %+v behind %q", a.indexQueue, a.indexPath)
	}
	select {
	case got := <-searcher.started:
		t.Fatalf("the index started %+v before first finished", got)
	default:
	}

	searcher.finish <- struct{}{}
	<-done
	if got := <-searcher.started; got.path != "second" || got.recursive {
		t.Fatalf("the index started %q recursively %v after first, want second alone", got.path, got.recursive)
	}
	if a.indexPath != "second" || len(a.indexQueue) != 0 {
		t.Errorf("the index shows %q with %+v queued, want second alone", a.indexPath, a.indexQueue)
	}

	searcher.finish <- struct{}{}
	<-done
	if a.indexRunning || a.indexPath != "second" {
		t.Errorf("the index is running %v on %q after both finished", a.indexRunning, a.indexPath)
	}
}

// The search view tells a running index and the results shown together in
// its bottom-right corner, rather than in a status bar, says nothing about
// an index once it has finished, but still counts the images it left out.
func TestSearchViewTellsIndexInCorner(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha.png")
	writePNG(t, alpha, color.RGBA{R: 255, A: 255})
	a := syncApp(root)
	a.showSearch()
	a.searchResults = []searchResult{{photo: &photo{path: alpha, name: "alpha.png"}, score: 0.5}}
	a.searchTotal = 1
	a.searchedNoMore = true
	a.indexPath = filepath.Join(root, "Trips")
	a.indexCount = 2
	a.indexTotal = 12
	a.indexSkipped = 1
	a.indexRunning = true
	a.indexQueue = []indexTarget{{path: "Another"}}
	tt := ui.NewTester(a.view, 1000, 640)
	tt.Frame()

	corner, ok := tt.Find("Indexing 2/12 and 1 queued, 1 skipped. Showing 1/1")
	if !ok {
		t.Fatalf("the view does not tell that it is indexing: %q", tt.Texts())
	}
	if corner.X < 500 || corner.Y < 500 {
		t.Errorf("the index notice is at %v, want the bottom-right corner", corner)
	}

	a.indexRunning = false
	a.indexSkipped = 0
	tt.Frame()
	if tt.HasText("Indexing") || tt.HasText("Indexed") {
		t.Errorf("the view still tells about the finished index: %q", tt.Texts())
	}
	if _, ok := tt.Find("Showing 1/1"); !ok {
		t.Errorf("the view does not show the result count alone: %q", tt.Texts())
	}

	// An index that left images out keeps telling how many it skipped.
	a.indexSkipped = 2
	tt.Frame()
	if _, ok := tt.Find("Indexed 12 images, 2 skipped. Showing 1/1"); !ok {
		t.Errorf("the view does not count the images the index skipped: %q", tt.Texts())
	}

	a.indexErr = "database is locked"
	tt.Frame()
	if _, ok := tt.Find("Can't index Trips. Showing 1/1"); !ok {
		t.Errorf("the view does not say why the index stopped: %q", tt.Texts())
	}
}
