package main

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// gridItems writes the images the folders view shows and returns the app
// showing them.
func gridItems(t *testing.T, names ...string) (*app, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	for i, name := range names {
		writePNG(t, filepath.Join(root, name), indexColor(i+1))
	}
	a := syncApp(root)
	return a, ui.NewTester(a.view, 1000, 640)
}

// labelCenter returns the middle of the text s, as the last frame shows it.
func labelCenter(t *testing.T, tt *ui.Tester, s string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q", s)
	}
	return r.X + r.W/2, r.Y + r.H/2
}

func TestCmdClickChoosesSeveralImages(t *testing.T) {
	a, tt := gridItems(t, "alpha.png", "beta.png", "gamma.png")
	for _, name := range []string{"alpha.png", "gamma.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if a.folderPick.selection.Len() != 2 || !a.folderPick.selection.Has(a.items[0].path) || !a.folderPick.selection.Has(a.items[2].path) {
		t.Fatalf("chose %d items, want alpha.png and gamma.png: %+v", a.folderPick.selection.Len(), a.items)
	}
	if a.chosen != 2 {
		t.Errorf("the item last chosen is %d, want gamma.png", a.chosen)
	}
}

func TestShiftClickChoosesTheRange(t *testing.T) {
	a, tt := gridItems(t, "alpha.png", "beta.png", "gamma.png")
	if err := tt.Click("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Shift, "gamma.png"); err != nil {
		t.Fatal(err)
	}
	if a.folderPick.selection.Len() != 3 {
		t.Fatalf("chose %d items, want the three between alpha.png and gamma.png", a.folderPick.selection.Len())
	}
	for _, item := range a.items {
		if !a.folderPick.selection.Has(item.path) {
			t.Errorf("%q is not chosen", item.name)
		}
	}
}

func TestDragChoosesTheImagesInItsRectangle(t *testing.T) {
	a, tt := gridItems(t, "alpha.png", "beta.png", "gamma.png")
	x, y := labelCenter(t, tt, "alpha.png")
	to, _ := labelCenter(t, tt, "gamma.png")
	tt.Press(x, y)
	tt.Move(to, y+8)
	tt.Release(to, y+8)
	if a.folderPick.selection.Len() != 3 {
		t.Fatalf("the drag chose %d images, want 3", a.folderPick.selection.Len())
	}
	for _, item := range a.items {
		if !a.folderPick.selection.Has(item.path) {
			t.Errorf("%q is not chosen", item.name)
		}
	}
}

func TestIndexAppliesToTheChosenImages(t *testing.T) {
	root := t.TempDir()
	for i, name := range []string{"alpha.png", "beach.png", "sunset.png"} {
		writePNG(t, filepath.Join(root, name), indexColor(i+1))
	}
	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(root, "db.sqlite")
	a := syncApp(root)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	tt := ui.NewTester(a.view, 1000, 640)
	for _, name := range []string{"alpha.png", "beach.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.RightClick("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Index"); err != nil {
		t.Fatal(err)
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
	if count != 2 {
		t.Errorf("Index added %d images, want the two chosen", count)
	}
}

func TestIndexMenuOfAnUnchosenImageIndexesItAlone(t *testing.T) {
	root := t.TempDir()
	for i, name := range []string{"alpha.png", "beach.png", "sunset.png"} {
		writePNG(t, filepath.Join(root, name), indexColor(i+1))
	}
	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(root, "db.sqlite")
	a := syncApp(root)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.ClickWith(ui.Cmd, "alpha.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Cmd, "beach.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.RightClick("sunset.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Index"); err != nil {
		t.Fatal(err)
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
		t.Errorf("Index added %d images, want sunset.png alone", count)
	}
}

func TestDragFromEmptySpaceChoosesTheImagesInItsRectangle(t *testing.T) {
	a, tt := gridItems(t, "alpha.png", "beta.png", "gamma.png")
	x, y := labelCenter(t, tt, "alpha.png")
	to, _ := labelCenter(t, tt, "gamma.png")
	// From below the row of images, up across all three.
	tt.Press(x, y+120)
	tt.Move(to, y)
	tt.Release(to, y)
	if a.folderPick.selection.Len() != 3 {
		t.Fatalf("the drag from empty space chose %d images, want 3", a.folderPick.selection.Len())
	}
}

func TestOpenMenuOpensTheChosenImages(t *testing.T) {
	a, tt := gridItems(t, "alpha.png", "beta.png", "gamma.png")
	for _, name := range []string{"alpha.png", "gamma.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.RightClick("alpha.png"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	open, index := slices.Index(menu, "Open"), slices.Index(menu, "Index")
	if open < 0 || index < 0 {
		t.Fatalf("the image's menu is %q, want Open and Index", menu)
	}
	if open > index {
		t.Errorf("the image's menu is %q, want Open above Index", menu)
	}
	if err := tt.ChooseMenuItem("Open"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSingle {
		t.Fatalf("Open left the folders view showing")
	}
	if len(a.singlePhotos) != 2 || a.singlePhoto() == nil || a.singlePhoto().name != "alpha.png" {
		t.Fatalf("Open shows %d images starting at %v, want alpha.png and gamma.png", len(a.singlePhotos), a.singlePhoto())
	}
	if len(a.history) != 2 || a.tag != 0 {
		t.Fatalf("Open made %d tags with tag %d current, want alpha.png and gamma.png", len(a.history), a.tag)
	}
	if a.history[0] != a.items[0].path || a.history[1] != a.items[2].path {
		t.Errorf("Open made the tags %q, want alpha.png and gamma.png", a.history)
	}
	a.selectTag(1)
	if p := a.singlePhoto(); p == nil || p.name != "gamma.png" {
		t.Errorf("the second chosen image shows %v, want gamma.png", p)
	}
}

func TestOpenMenuOfOneImageStepsThroughTheFolder(t *testing.T) {
	a, tt := gridItems(t, "alpha.png", "beta.png", "gamma.png")
	if err := tt.RightClick("beta.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSingle || a.singlePhoto() == nil || a.singlePhoto().name != "beta.png" {
		t.Fatalf("Open shows %v, want beta.png", a.singlePhoto())
	}
	if len(a.singlePhotos) != len(a.photos) {
		t.Errorf("Open steps through %d images, want the folder's %d", len(a.singlePhotos), len(a.photos))
	}
	if len(a.history) != 1 {
		t.Errorf("Open made %d tags, want one", len(a.history))
	}
}

// searchGridItems makes a search view showing the images named, as a
// search that found them would.
func searchGridItems(t *testing.T, names ...string) (*app, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	results := make([]searchResult, len(names))
	for i, name := range names {
		path := filepath.Join(root, name)
		writePNG(t, path, indexColor(i+1))
		results[i] = searchResult{photo: &photo{path: path, name: name}, score: float64(len(names)-i) / 10}
	}
	a := syncApp(root)
	a.mode = viewSearch
	a.searchResults = results
	a.searchTotal = len(results)
	a.searchPageSize = len(results)
	a.searchPick = gridPick{pivot: -1, handles: make([]ui.Handle, len(results))}
	tt := ui.NewTester(a.view, 1000, 640)
	tt.Frame()
	return a, tt
}

func TestCmdClickChoosesSeveralSearchResults(t *testing.T) {
	a, tt := searchGridItems(t, "alpha.png", "beta.png", "gamma.png")
	for _, name := range []string{"alpha.png", "gamma.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if a.searchPick.selection.Len() != 2 {
		t.Fatalf("chose %d results, want alpha.png and gamma.png", a.searchPick.selection.Len())
	}
	if !a.searchPick.selection.Has(a.searchResults[0].photo.path) ||
		!a.searchPick.selection.Has(a.searchResults[2].photo.path) {
		t.Errorf("the search chose from %d results, want alpha.png and gamma.png", len(a.searchResults))
	}
}

func TestDragChoosesTheSearchResultsInItsRectangle(t *testing.T) {
	a, tt := searchGridItems(t, "alpha.png", "beta.png", "gamma.png")
	x, y := labelCenter(t, tt, "alpha.png")
	to, _ := labelCenter(t, tt, "gamma.png")
	tt.Press(x, y)
	tt.Move(to, y+8)
	tt.Release(to, y+8)
	if a.searchPick.selection.Len() != 3 {
		t.Fatalf("the drag chose %d results, want 3", a.searchPick.selection.Len())
	}
}

func TestOpenMenuOpensTheChosenSearchResults(t *testing.T) {
	a, tt := searchGridItems(t, "alpha.png", "beta.png", "gamma.png")
	for _, name := range []string{"alpha.png", "gamma.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.RightClick("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); slices.Index(menu, "Open") < 0 {
		t.Fatalf("the result's menu is %q, want an Open item", menu)
	}
	if err := tt.ChooseMenuItem("Open"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSingle || a.singlePhoto() == nil || a.singlePhoto().name != "alpha.png" {
		t.Fatalf("Open shows %v in view %d, want alpha.png alone", a.singlePhoto(), a.mode)
	}
	if len(a.singlePhotos) != 2 {
		t.Fatalf("Open steps through %d results, want the two chosen", len(a.singlePhotos))
	}
	if len(a.history) != 2 || a.tag != 0 {
		t.Fatalf("Open made %d tags with tag %d current, want alpha.png and gamma.png", len(a.history), a.tag)
	}
	if a.history[0] != a.searchResults[0].photo.path || a.history[1] != a.searchResults[2].photo.path {
		t.Errorf("Open made the tags %q, want alpha.png and gamma.png", a.history)
	}
}
