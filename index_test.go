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
	if err := os.Mkdir(trips, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(trips, "beach.png"), indexColor(4))

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
	if menu := tt.Menu(); !slices.Contains(menu, "Index") {
		t.Fatalf("the folder's menu is %q, want an Index item", menu)
	}
	if err := tt.ChooseMenuItem("Index"); err != nil {
		t.Fatal(err)
	}
	if a.indexCount != 1 {
		t.Errorf("the menu indexed %d images, want 1", a.indexCount)
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
	if menu := tt.Menu(); !slices.Contains(menu, "Index") {
		t.Fatalf("the image's menu is %q, want an Index item", menu)
	}
	if err := tt.ChooseMenuItem("Index"); err != nil {
		t.Fatal(err)
	}
	if a.indexCount != 1 {
		t.Errorf("the menu indexed %d images, want 1", a.indexCount)
	}
}

// queueIndexSearcher reports the path each index starts and finishes when the
// test lets it.
type queueIndexSearcher struct {
	started chan string
	finish  chan struct{}
}

func (s *queueIndexSearcher) search(query, scope string, page int) ([]searchResult, int, error) {
	return nil, 0, nil
}

func (s *queueIndexSearcher) index(path string) (chan struct{}, error) {
	progress := make(chan struct{})
	s.started <- path
	go func() {
		<-s.finish
		close(progress)
	}()
	return progress, nil
}

func TestIndexQueuesFoldersWhileRunning(t *testing.T) {
	a := newApp(t.TempDir())
	searcher := &queueIndexSearcher{
		started: make(chan string, 4),
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

	a.startIndex("first")
	if got := <-searcher.started; got != "first" {
		t.Fatalf("the index started %q, want first", got)
	}
	a.startIndex("second")
	if a.indexPath != "first" || len(a.indexQueue) != 1 || a.indexQueue[0] != "second" {
		t.Fatalf("the second folder queued as %q behind %q", a.indexQueue, a.indexPath)
	}
	select {
	case got := <-searcher.started:
		t.Fatalf("the index started %q before first finished", got)
	default:
	}

	searcher.finish <- struct{}{}
	<-done
	if got := <-searcher.started; got != "second" {
		t.Fatalf("the index started %q after first, want second", got)
	}
	if a.indexPath != "second" || len(a.indexQueue) != 0 {
		t.Errorf("the index shows %q with %q queued, want second alone", a.indexPath, a.indexQueue)
	}

	searcher.finish <- struct{}{}
	<-done
	if a.indexRunning || a.indexPath != "second" {
		t.Errorf("the index is running %v on %q after both finished", a.indexRunning, a.indexPath)
	}
}
