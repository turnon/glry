package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
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
