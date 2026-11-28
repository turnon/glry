package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/turnon/glry/searcher"
)

func galleryRightClick(t *testing.T, a *app, tt *ui.Tester, name string) {
	t.Helper()
	for _, info := range tt.AccessAll() {
		if info.Name != name || info.Bounds.X < a.sidebarWidth {
			continue
		}
		tt.RightClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		return
	}
	t.Fatalf("the folders view does not show %q: %q", name, tt.Texts())
}

// trashForTest makes a move paths to the trash by recording them and
// deleting them, as the system does, without the running app Shell needs.
func trashForTest(a *app, moved *[]string) {
	a.trash = func(path string) error {
		*moved = append(*moved, path)
		return os.RemoveAll(path)
	}
}

// insertIndexedPaths adds an image row at each path to the database, as an
// index would have.
func insertIndexedPaths(t *testing.T, dbPath string, paths ...string) {
	t.Helper()
	if err := ensureSQLiteDB(dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vector := make([]float32, searcher.VectorDims)
	vector[0] = 1
	for _, path := range paths {
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			filepath.Base(path), filepath.Dir(path), path, vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
}

// indexedPaths lists the paths the database holds.
func indexedPaths(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`select dir, name from images`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var dir, name string
		if err := rows.Scan(&dir, &name); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}

// The Remove item moves the image to the trash and takes its row out of
// the database, leaving the other images in place.
func TestRemoveFromAppImageMenu(t *testing.T) {
	root := t.TempDir()
	alpha, beta := filepath.Join(root, "alpha.png"), filepath.Join(root, "beta.png")
	writePNG(t, alpha, indexColor(1))
	writePNG(t, beta, indexColor(2))

	dbPath := filepath.Join(root, "db.sqlite")
	insertIndexedPaths(t, dbPath, alpha, beta)
	a := syncApp(root)
	sqliteAt(a, dbPath)
	var trashed []string
	trashForTest(a, &trashed)
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.RightClick("alpha.png"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	index, remove := slices.Index(menu, "Index"), slices.Index(menu, "Remove")
	if index < 0 || remove < 0 {
		t.Fatalf("the image's menu is %q, want Index and Remove", menu)
	}
	if index > remove {
		t.Errorf("the image's menu is %q, want Index above Remove", menu)
	}
	if err := tt.ChooseMenuItem("Remove"); err != nil {
		t.Fatal(err)
	}
	if a.removeErr != "" {
		t.Fatalf("removing the image failed: %s", a.removeErr)
	}
	if !slices.Contains(trashed, alpha) {
		t.Errorf("Remove trashed %q, want %q", trashed, alpha)
	}
	if tt.HasText("alpha.png") {
		t.Error("the folders view still shows the removed image")
	}
	if got, want := indexedPaths(t, dbPath), []string{beta}; !slices.Equal(got, want) {
		t.Errorf("the database holds %q, want %q", got, want)
	}
}

// The Remove item of a tree row moves the folder to the trash and takes its
// images, and those in the folders under it, out of the database.
func TestRemoveFromAppFolderTreeMenu(t *testing.T) {
	root := t.TempDir()
	trips := filepath.Join(root, "Trips")
	beach := filepath.Join(trips, "Beach")
	if err := os.MkdirAll(beach, 0o755); err != nil {
		t.Fatal(err)
	}
	sunset := filepath.Join(trips, "sunset.png")
	sand := filepath.Join(beach, "sand.png")
	other := filepath.Join(root, "other.png")
	writePNG(t, sunset, indexColor(3))
	writePNG(t, sand, indexColor(4))
	writePNG(t, other, indexColor(5))

	dbPath := filepath.Join(root, "db.sqlite")
	insertIndexedPaths(t, dbPath, sunset, sand, other)
	a := syncApp(root)
	sqliteAt(a, dbPath)
	var trashed []string
	trashForTest(a, &trashed)
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.RightClick("Trips"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	index, recursively, remove := slices.Index(menu, "Index"),
		slices.Index(menu, "Index Recursively"), slices.Index(menu, "Remove")
	if index < 0 || recursively < 0 || remove < 0 {
		t.Fatalf("the folder's menu is %q, want Index, Index Recursively and Remove", menu)
	}
	if remove < recursively {
		t.Errorf("the folder's menu is %q, want Remove below Index Recursively", menu)
	}
	if err := tt.ChooseMenuItem("Remove"); err != nil {
		t.Fatal(err)
	}
	if a.removeErr != "" {
		t.Fatalf("removing the folder failed: %s", a.removeErr)
	}
	if !slices.Contains(trashed, trips) {
		t.Errorf("Remove trashed %q, want %q", trashed, trips)
	}
	if tt.HasText("Trips") {
		t.Error("the tree still shows the removed folder")
	}
	if got, want := indexedPaths(t, dbPath), []string{other}; !slices.Equal(got, want) {
		t.Errorf("the database holds %q, want %q", got, want)
	}
}

// The Remove item of a gallery folder cell moves that folder to the trash,
// as the tree row's does.
func TestRemoveFromAppGalleryFolderMenu(t *testing.T) {
	root := t.TempDir()
	trips := filepath.Join(root, "Trips")
	if err := os.Mkdir(trips, 0o755); err != nil {
		t.Fatal(err)
	}
	sunset := filepath.Join(trips, "sunset.png")
	other := filepath.Join(root, "other.png")
	writePNG(t, sunset, indexColor(6))
	writePNG(t, other, indexColor(7))

	dbPath := filepath.Join(root, "db.sqlite")
	insertIndexedPaths(t, dbPath, sunset, other)
	a := syncApp(root)
	sqliteAt(a, dbPath)
	var trashed []string
	trashForTest(a, &trashed)
	tt := ui.NewTester(a.view, 1000, 640)
	galleryRightClick(t, a, tt, "Trips")
	if menu := tt.Menu(); !slices.Contains(menu, "Remove") {
		t.Fatalf("the gallery folder's menu is %q, want a Remove item", menu)
	}
	if err := tt.ChooseMenuItem("Remove"); err != nil {
		t.Fatal(err)
	}
	if a.removeErr != "" {
		t.Fatalf("removing the folder failed: %s", a.removeErr)
	}
	if !slices.Contains(trashed, trips) {
		t.Errorf("Remove trashed %q, want %q", trashed, trips)
	}
	if tt.HasText("Trips") {
		t.Error("the folders view still shows the removed folder")
	}
	if got, want := indexedPaths(t, dbPath), []string{other}; !slices.Equal(got, want) {
		t.Errorf("the database holds %q, want %q", got, want)
	}
}

// The Remove item takes every chosen image to the trash, not just the one
// right-clicked.
func TestRemoveAppliesToTheChosenImages(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha.png")
	beach := filepath.Join(root, "beach.png")
	sunset := filepath.Join(root, "sunset.png")
	writePNG(t, alpha, indexColor(1))
	writePNG(t, beach, indexColor(2))
	writePNG(t, sunset, indexColor(3))

	dbPath := filepath.Join(root, "db.sqlite")
	insertIndexedPaths(t, dbPath, alpha, beach, sunset)
	a := syncApp(root)
	sqliteAt(a, dbPath)
	var trashed []string
	trashForTest(a, &trashed)
	tt := ui.NewTester(a.view, 1000, 640)
	for _, name := range []string{"alpha.png", "beach.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.RightClick("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alpha, beach} {
		if !slices.Contains(trashed, path) {
			t.Errorf("Remove trashed %q, want %q", trashed, path)
		}
		if tt.HasText(filepath.Base(path)) {
			t.Errorf("the folders view still shows the removed image %q", filepath.Base(path))
		}
	}
	if got, want := indexedPaths(t, dbPath), []string{sunset}; !slices.Equal(got, want) {
		t.Errorf("the database holds %q, want %q", got, want)
	}
}
