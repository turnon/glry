package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// syncApp makes the app do its file work inline, as a test can wait for.
func syncApp(root string) *app {
	a := newApp(root)
	a.async = func(fn func()) { fn() }
	a.start()
	return a
}

// sidebarHas reports whether s shows in the left sidebar, not the gallery.
func sidebarHas(a *app, tt *ui.Tester, s string) bool {
	r, ok := tt.Find(s)
	return ok && r.X < a.sidebarWidth
}

func writePNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestViewShowsFolderImages(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	trips := filepath.Join(root, "Trips")
	if err := os.Mkdir(trips, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(trips, "beach.png"), color.RGBA{B: 255, A: 255})
	empty := filepath.Join(root, "Empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	for _, name := range []string{"alpha.png", "beta.png", "Trips", "Empty"} {
		if !tt.HasText(name) {
			t.Fatalf("%q is not shown: %q", name, tt.Texts())
		}
	}
	for _, button := range []string{"Open", "Refresh"} {
		if _, ok := tt.Find(button); !ok {
			t.Errorf("the sidebar's %s icon button is not shown", button)
		}
	}
	if a.chosen != -1 {
		t.Errorf("a gallery item is chosen at start: %d", a.chosen)
	}
	if len(a.photos) != 2 {
		t.Fatalf("%d photos in the root, want 2", len(a.photos))
	}
	if len(a.items) != 4 || !a.items[0].dir || !a.items[1].dir || a.items[2].dir || a.items[3].dir {
		t.Fatalf("gallery items are %+v, want two folders before two images", a.items)
	}
	// The cells in view decoded their thumbnails.
	if len(a.photos) > 0 && a.photos[0].img == nil {
		t.Error("the first thumbnail did not load")
	}
	if a.photos[0].state != thumbReady {
		t.Errorf("the first thumbnail is in state %d, want ready", a.photos[0].state)
	}
	if a.photos[0].width != 40 || a.photos[0].height != 30 {
		t.Errorf("the first photo is %d×%d, want 40×30", a.photos[0].width, a.photos[0].height)
	}
	if !tt.HasText("40 × 30") {
		t.Errorf("the cell does not show the photo's dimensions: %q", tt.Texts())
	}
	dims, ok := tt.Find("40 × 30")
	if !ok {
		t.Fatal("the dimensions line is not shown")
	}
	size, ok := tt.Find(byteSize(a.photos[0].size))
	if !ok {
		t.Fatal("the size line is not shown")
	}
	if dims.Y >= size.Y {
		t.Errorf("dimensions (%v) are not above the size (%v)", dims, size)
	}
	if !tt.HasText("2 images,") {
		t.Errorf("the size tag does not count the folder's images: %q", tt.Texts())
	}
	if tag, ok := tt.Find(a.imagesTag()); !ok || tag.X+tag.W < 985 || tag.Y+tag.H < 630 {
		t.Errorf("the size tag is not in the gallery's bottom right corner: %v, shown %v", tag, ok)
	}

	if err := tt.Click("Trips"); err != nil {
		t.Fatal(err)
	}
	if a.selected != trips {
		t.Fatalf("the chosen folder is %q, want %q", a.selected, trips)
	}
	if !tt.HasText("beach.png") {
		t.Errorf("Trips' image is not shown: %q", tt.Texts())
	}
	if !tt.HasText("1 image,") {
		t.Errorf("the size tag does not count one image: %q", tt.Texts())
	}
	if tt.HasText("alpha.png") || tt.HasText("beta.png") {
		t.Errorf("the gallery kept the root's images: %q", tt.Texts())
	}

	if err := tt.Click("Empty"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("This folder is empty") {
		t.Errorf("the empty folder shows no empty state: %q", tt.Texts())
	}
	if !tt.HasText("0 images, 0 B") {
		t.Errorf("the empty folder's tag is wrong: %q", tt.Texts())
	}
}

func TestByteSize(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{12 * 1024 * 1024, "12.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	} {
		if got := byteSize(tc.n); got != tc.want {
			t.Errorf("byteSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestTreeLoadsChildrenWhenOpened(t *testing.T) {
	root := t.TempDir()
	trips := filepath.Join(root, "Trips")
	italy := filepath.Join(trips, "Italy")
	if err := os.MkdirAll(italy, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(italy, "colosseum.png"), color.RGBA{R: 200, G: 120, A: 255})

	a := syncApp(root)
	tt := ui.NewTester(a.view, 900, 600)
	if sidebarHas(a, tt, "Italy") {
		t.Fatalf("a closed folder's child shows in the tree: %q", tt.Texts())
	}
	if err := tt.Click("Trips"); err != nil {
		t.Fatal(err)
	}
	if !sidebarHas(a, tt, "Italy") {
		t.Fatalf("choosing Trips did not show Italy in the tree: %q", tt.Texts())
	}
	if err := tt.Click("Italy"); err != nil {
		t.Fatal(err)
	}
	if a.selected != italy {
		t.Fatalf("the chosen folder is %q, want %q", a.selected, italy)
	}
	if !tt.HasText("colosseum.png") {
		t.Errorf("Italy's image is not shown: %q", tt.Texts())
	}
}

func TestScanSkipsHiddenAndNonImages(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "photo.png"), color.RGBA{A: 255})
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	photos, err := scanImages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(photos) != 1 || photos[0].name != "photo.png" {
		t.Errorf("scanned %v, want photo.png alone", photos)
	}
	dirs, err := scanDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Errorf("scanned hidden directories: %v", dirs)
	}
}

func TestGalleryUsesCellSize(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "a.png"), color.RGBA{A: 255})
	a := syncApp(root)
	a.setCellSize(cellSize{width: 120, height: 90})
	tt := ui.NewTester(a.view, 1000, 640)
	cell, ok := tt.Find("a.png")
	if !ok {
		t.Fatal("the image cell is not shown")
	}
	if cell.H != 90 || cell.W < 120 {
		t.Errorf("the cell is %v, want a height of 90 and at least 120 wide", cell)
	}
}

func TestRailSwitchesViews(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	// The rail stands at the window's left edge, before the tree.
	rail, ok := tt.Find("File tree")
	if !ok {
		t.Fatalf("the rail's file tree icon is not shown: %q", tt.Texts())
	}
	if rail.X+rail.W > railWidth {
		t.Errorf("the rail's file tree icon is at %v, past the rail's %d DIPs", rail, railWidth)
	}
	if tree, ok := tt.Find("Open"); !ok || tree.X < railWidth {
		t.Errorf("the sidebar is at %v, want it right of the rail", tree)
	}
	if a.mode != viewGallery {
		t.Fatalf("the window starts in view %d, want the gallery", a.mode)
	}

	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSingle {
		t.Fatalf("clicking the magnifier shows view %d, want the single image", a.mode)
	}
	if _, ok := tt.Find("Open"); ok {
		t.Error("the tree and its buttons still show in the single image view")
	}
	if _, ok := tt.Find("Next image"); ok {
		t.Error("the single image view still shows its next button")
	}
	if !tt.HasText("alpha.png") {
		t.Errorf("the single image view does not tag the image shown: %q", tt.Texts())
	}
	if a.singleImg == nil {
		t.Fatal("the single image view did not decode the first photo")
	}

	tt.Key(0, ui.KeyRight)
	if a.single != 1 {
		t.Errorf("the right arrow shows image %d, want the second", a.single)
	}
	if len(a.history) != 1 || a.history[0] != a.photos[1].path {
		t.Errorf("the arrow opened a tag instead of renaming one: %v", a.history)
	}
	if tt.HasText("alpha.png") || !tt.HasText("beta.png") {
		t.Errorf("the tag did not change from alpha.png to beta.png: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyRight)
	if a.single != 1 || len(a.history) != 1 {
		t.Errorf("the right arrow moved past the last image: %d, %v", a.single, a.history)
	}
	tt.Key(0, ui.KeyLeft)
	if a.single != 0 || len(a.history) != 1 || a.history[0] != a.photos[0].path {
		t.Errorf("the left arrow shows image %d, history %v, want the first", a.single, a.history)
	}
	if !tt.HasText("alpha.png") {
		t.Errorf("the tag did not change back to alpha.png: %q", tt.Texts())
	}

	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewGallery {
		t.Fatalf("clicking the tree icon shows view %d, want the gallery", a.mode)
	}
	if !tt.HasText("alpha.png") || !tt.HasText("beta.png") {
		t.Errorf("the gallery did not come back: %q", tt.Texts())
	}
}

func TestSingleViewShowsChosenPhoto(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	beta := slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "beta.png" })
	a.chosen = beta
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if a.single != 1 || !tt.HasText("beta.png") {
		t.Errorf("the single image view shows image %d, want beta.png: %q", a.single, tt.Texts())
	}
}

func TestDoubleClickPhotoShowsSingleView(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	cell, ok := tt.Find("beta.png")
	if !ok {
		t.Fatalf("the image cell is not shown: %q", tt.Texts())
	}
	x, y := cell.X+cell.W/2, cell.Y+cell.H/2
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)

	if a.mode != viewSingle {
		t.Fatalf("double clicking a photo shows view %d, want the single image", a.mode)
	}
	if a.single != 1 {
		t.Errorf("double clicking beta.png shows image %d, want 1", a.single)
	}
	if !tt.HasText("beta.png") {
		t.Errorf("the single image view does not tag beta.png: %q", tt.Texts())
	}
	if len(a.history) != 1 || a.history[0] != a.photos[1].path {
		t.Errorf("the history is %v, want beta.png alone", a.history)
	}
}

func TestSingleViewKeepsHistoryTags(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	writePNG(t, filepath.Join(root, "gamma.png"), color.RGBA{B: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	// Show alpha, then beta from the gallery: each opens a tag.
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 1 || a.history[0] != a.photos[0].path {
		t.Fatalf("the history is %v, want alpha.png alone", a.history)
	}
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	beta := slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "beta.png" })
	a.chosen = beta
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 2 || a.history[0] != a.photos[0].path || a.history[1] != a.photos[1].path {
		t.Fatalf("the history is %v, want alpha.png then beta.png", a.history)
	}
	if a.tag != 1 {
		t.Errorf("the current tag is %d, want beta.png's", a.tag)
	}

	// The photos opened are tagged on the left of the header.
	tag, ok := tt.Find("alpha.png")
	if !ok {
		t.Fatalf("the history tag is not shown: %q", tt.Texts())
	}
	if tag.X > 200 || tag.Y > 50 {
		t.Errorf("the history tag is at %v, want the header's left", tag)
	}
	if err := tt.Click("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if a.single != 0 || a.tag != 0 {
		t.Errorf("the tag shows image %d with tag %d, want alpha.png's tag", a.single, a.tag)
	}

	// The tags stay when the gallery takes the window and gives it back.
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if a.single != 0 {
		t.Errorf("coming back shows image %d, want alpha.png", a.single)
	}
	if _, ok := tt.Find("beta.png"); !ok {
		t.Errorf("the history tags went away with the gallery: %q", tt.Texts())
	}
}

func TestTagsSurviveOpeningAnotherFolder(t *testing.T) {
	root := t.TempDir()
	trips := filepath.Join(root, "Trips")
	if err := os.Mkdir(trips, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(trips, "beach.png"), color.RGBA{B: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	// Open alpha.png: it becomes the only tag.
	alpha := slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "alpha.png" })
	a.chosen = alpha
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 1 || a.history[0] != a.photos[0].path {
		t.Fatalf("the history is %v, want alpha.png alone", a.history)
	}

	// Choosing another folder and opening its image adds a tag rather
	// than clearing the ones opened before.
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Trips"); err != nil {
		t.Fatal(err)
	}
	beach := slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "beach.png" })
	a.chosen = beach
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 2 {
		t.Fatalf("opening an image in another folder cleared the tags: %v", a.history)
	}
	if base := filepath.Base(a.history[0]); base != "alpha.png" {
		t.Errorf("the first tag is %q, want alpha.png", base)
	}
	if base := filepath.Base(a.history[1]); base != "beach.png" {
		t.Errorf("the second tag is %q, want beach.png", base)
	}
	if !tt.HasText("alpha.png") || !tt.HasText("beach.png") {
		t.Errorf("the header does not show both tags: %q", tt.Texts())
	}

	// The old tag goes back to its folder and shows its photo.
	if err := tt.Click("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if a.selected != root {
		t.Errorf("choosing alpha.png's tag shows folder %q, want %q", a.selected, root)
	}
	if a.single < 0 || a.single >= len(a.photos) || a.photos[a.single].name != "alpha.png" {
		t.Fatalf("choosing alpha.png's tag shows image %d: %v", a.single, a.photos)
	}
	if a.tag != 0 {
		t.Errorf("the current tag is %d, want alpha.png's", a.tag)
	}
}

func TestArrowsRenameCurrentTagOnly(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	writePNG(t, filepath.Join(root, "gamma.png"), color.RGBA{B: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	// Show alpha, then beta from the gallery: each opens a tag.
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	beta := slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "beta.png" })
	a.chosen = beta
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 2 || a.tag != 1 {
		t.Fatalf("the history is %v, tag %d, want both photos tagged at the second", a.history, a.tag)
	}

	// Stepping onto a photo another tag shows renames the current tag,
	// not that tag.
	tt.Key(0, ui.KeyLeft)
	if a.single != 0 {
		t.Errorf("the left arrow shows image %d, want alpha.png", a.single)
	}
	if len(a.history) != 2 || a.tag != 1 || a.history[1] != a.photos[0].path {
		t.Errorf("the current tag did not change to alpha.png: tag %d, %v", a.tag, a.history)
	}
	if !tt.HasText("alpha.png") {
		t.Errorf("the current tag's name is not shown: %q", tt.Texts())
	}

	// The arrows keep renaming the same tag as they step.
	tt.Key(0, ui.KeyRight)
	if a.single != 1 || a.tag != 1 || a.history[1] != a.photos[1].path {
		t.Errorf("the right arrow shows image %d, tag %d, %v", a.single, a.tag, a.history)
	}
	tt.Key(0, ui.KeyRight)
	if a.single != 2 || a.tag != 1 || a.history[1] != a.photos[2].path {
		t.Errorf("the right arrow shows image %d, tag %d, %v", a.single, a.tag, a.history)
	}
	if _, ok := tt.Find("beta.png"); ok {
		t.Errorf("the renamed tag still shows beta.png: %q", tt.Texts())
	}
}

func TestHistoryTagsKeepTheLatest(t *testing.T) {
	a := newApp(t.TempDir())
	for i := 0; i < historyLimit+3; i++ {
		p := &photo{path: filepath.Join("/photos", fmt.Sprintf("%d.png", i))}
		a.photos = append(a.photos, p)
		a.addTag(p)
	}
	if len(a.history) != historyLimit {
		t.Fatalf("the history holds %d photos, want %d", len(a.history), historyLimit)
	}
	if a.tag != historyLimit-1 {
		t.Errorf("the current tag is %d, want the last", a.tag)
	}
	if a.history[0] != a.photos[3].path {
		t.Errorf("the history starts at %q, want the first kept photo", a.history[0])
	}
}

func TestSingleViewWithoutImages(t *testing.T) {
	root := t.TempDir()
	a := syncApp(root)
	tt := ui.NewTester(a.view, 900, 600)
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("No images in this folder") {
		t.Errorf("an empty folder has no single image state: %q", tt.Texts())
	}
}
