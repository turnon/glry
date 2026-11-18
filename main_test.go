package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
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

// singleViewNow is how the single image view shows its current tag's
// photo.
func singleViewNow(t *testing.T, a *app) *tagView {
	t.Helper()
	view := a.currentTagView()
	if view == nil {
		t.Fatal("the single image view has no tag to show")
	}
	return view
}

// sidebarHas reports whether s shows in the left sidebar, not the gallery.
func sidebarHas(a *app, tt *ui.Tester, s string) bool {
	r, ok := tt.Find(s)
	return ok && r.X < a.sidebarWidth
}

func writePNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	writePNGSize(t, path, 40, 30, c)
}

// writePNGSize writes a plain picture of the size given, whose aspect its
// view fits.
func writePNGSize(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
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
	if len(a.items) != 4 || !a.items[0].dir || !a.items[1].dir || a.items[2].dir || a.items[3].dir {
		t.Fatalf("gallery items are %+v, want two folders before two images", a.items)
	}
	if a.photos[0].img == nil {
		t.Error("the first thumbnail did not load")
	}
	if !tt.HasText("2 images,") {
		t.Errorf("the size tag does not count the folder's images: %q", tt.Texts())
	}

	if err := tt.Click("Trips"); err != nil {
		t.Fatal(err)
	}
	if a.selected != trips || !tt.HasText("beach.png") {
		t.Errorf("choosing Trips shows folder %q and %q, want its image", a.selected, tt.Texts())
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
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("No images in this folder") {
		t.Errorf("an empty folder has no single image state: %q", tt.Texts())
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

	if a.mode != viewGallery {
		t.Fatalf("the window starts in view %d, want the gallery", a.mode)
	}
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSearch {
		t.Fatalf("clicking the magnifier shows view %d, want the search", a.mode)
	}
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSingle || !tt.HasText("alpha.png") {
		t.Fatalf("clicking the eye shows view %d with %q, want the first image", a.mode, tt.Texts())
	}
	if _, ok := tt.Find("Open"); ok {
		t.Error("the tree and its buttons still show in the single image view")
	}
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewGallery || !tt.HasText("alpha.png") || !tt.HasText("beta.png") {
		t.Fatalf("clicking the tree icon shows view %d with %q, want the gallery", a.mode, tt.Texts())
	}
}

func TestSingleViewOpensPhoto(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	// A double click opens that photo alone, with a tag for it.
	cell, ok := tt.Find("beta.png")
	if !ok {
		t.Fatalf("the image cell is not shown: %q", tt.Texts())
	}
	x, y := cell.X+cell.W/2, cell.Y+cell.H/2
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	if a.mode != viewSingle || a.single != 1 || !tt.HasText("beta.png") {
		t.Errorf("double clicking beta.png shows view %d image %d: %q", a.mode, a.single, tt.Texts())
	}
	if len(a.history) != 1 || a.history[0] != a.photos[1].path {
		t.Errorf("the history is %v, want beta.png alone", a.history)
	}

	// The eye opens the photo the gallery chose.
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	a.chosen = slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "alpha.png" })
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if a.single != 0 || !tt.HasText("alpha.png") {
		t.Errorf("the eye shows image %d: %q, want alpha.png", a.single, tt.Texts())
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

func TestClosingTags(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	// Show alpha, then beta from the gallery: each opens a tag.
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	a.chosen = slices.IndexFunc(a.items, func(it galleryItem) bool { return it.name == "beta.png" })
	tt.Frame()
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 2 || a.tag != 1 {
		t.Fatalf("the history is %v, tag %d, want both photos tagged at the second", a.history, a.tag)
	}

	// Each tag shows its close icon on its right.
	name, ok := tt.Find("alpha.png")
	if !ok {
		t.Fatalf("alpha.png's tag is not shown: %q", tt.Texts())
	}
	close, ok := tt.Find("Close alpha.png")
	if !ok {
		t.Fatalf("alpha.png's close icon is not shown: %q", tt.Texts())
	}
	if close.X < name.X+name.W-1 || close.Y > 50 {
		t.Errorf("the close icon is at %v, want the tag's right in the header", close)
	}

	// Closing another tag drops it, with its view, and leaves the photo
	// the view shows.
	if err := tt.Click("Close alpha.png"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 1 || a.history[0] != a.photos[1].path {
		t.Errorf("closing alpha.png's tag leaves %v, want beta.png alone", a.history)
	}
	if len(a.tagViews) != 1 {
		t.Errorf("%d tag views for %d tags", len(a.tagViews), len(a.history))
	}
	if a.tag != 0 || a.single != 1 {
		t.Errorf("closing alpha.png's tag shows image %d with tag %d, want beta.png", a.single, a.tag)
	}
	if _, ok := tt.Find("alpha.png"); ok {
		t.Errorf("the closed tag still shows: %q", tt.Texts())
	}

	// Escape closes the current tag, the last, and returns to the gallery.
	tt.Key(0, ui.KeyEscape)
	if len(a.history) != 0 || a.tag != -1 {
		t.Errorf("closing the last tag leaves %v with tag %d", a.history, a.tag)
	}
	if a.mode != viewGallery {
		t.Errorf("closing the last tag shows view %d, want the gallery", a.mode)
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
	if len(a.tagViews) != len(a.history) {
		t.Errorf("%d tag views for %d tags", len(a.tagViews), len(a.history))
	}
	if a.history[0] != a.photos[3].path {
		t.Errorf("the history starts at %q, want the first kept photo", a.history[0])
	}
}

func TestSingleImageZoomsAndPans(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	a := syncApp(root)
	var ctx *ui.Context
	tt := ui.NewTester(func(c *ui.Context) { ctx = c; a.view(c) }, 1000, 640)
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	room := a.singleRef.Bounds(ctx)
	if room.W <= 0 || room.H <= 0 {
		t.Fatalf("the single image view has no room: %v", room)
	}
	cx, cy := room.X+room.W/2, room.Y+room.H/2
	view := singleViewNow(t, a)

	if view.zoom != minSingleZoom || view.scroll.X != 0 || view.scroll.Y != 0 {
		t.Fatalf("the photo shows at %v scrolled to %v, want it fitting", view.zoom, view.scroll)
	}
	// Fitting, there is nowhere to scroll and no bar.
	tt.Press(cx-100, cy-100)
	tt.Move(cx-60, cy-60)
	tt.Release(cx-60, cy-60)
	if view.scroll.X != 0 || view.scroll.Y != 0 || view.scroll.MaxX != 0 || view.scroll.MaxY != 0 {
		t.Errorf("a fitting photo scrolls to %v, with max %v/%v", view.scroll, view.scroll.MaxX, view.scroll.MaxY)
	}

	// A notch zooms about the pointer: at the center the photo stays
	// centered in its content.
	tt.Wheel(cx, cy, 0, -1)
	if view.zoom != singleZoomStep {
		t.Fatalf("a notch zooms to %v, want %v", view.zoom, singleZoomStep)
	}
	cw, ch, _, _ := singleContentSize(a.singleSource(), view.zoom, room.W, room.H)
	if want := (cw - room.W) / 2; view.scroll.X != want {
		t.Errorf("zooming at the center scrolls to %v, want %v", view.scroll.X, want)
	}
	if want := (ch - room.H) / 2; view.scroll.Y != want {
		t.Errorf("zooming at the center scrolls to %v, want %v", view.scroll.Y, want)
	}
	// The content exceeds the room, so its scroll bars show and scroll.
	tt.Frame()
	if view.scroll.MaxX <= 0 || view.scroll.MaxY <= 0 {
		t.Fatalf("a zoomed photo does not overflow: max %v/%v", view.scroll.MaxX, view.scroll.MaxY)
	}
	tt.Move(room.X+room.W-6, cy)
	if got := tt.Cursor(); got != ui.CursorDefault {
		t.Errorf("the scroll bar at the room's edge has cursor %v, want the default", got)
	}
	tt.Move(cx, cy)

	// Off center, the photo moves the other way so what showed under the
	// pointer stays there, and a touchpad's scroll scrolls it.
	x0 := view.scroll.X
	tt.Wheel(cx+100, cy, 0, -1)
	if view.scroll.X <= x0 {
		t.Errorf("zooming right of the center scrolls to %v, want more than %v", view.scroll.X, x0)
	}
	x0, y0 := view.scroll.X, view.scroll.Y
	tt.Scroll(cx, cy, 0, 50)
	if view.scroll.Y != y0+50 {
		t.Errorf("a touchpad's scroll moves to %v, want %v", view.scroll.Y, y0+50)
	}

	// A drag moves it, as far as its zoom leaves past the room's edges.
	x0, y0 = view.scroll.X, view.scroll.Y
	tt.Press(cx+20, cy+20)
	tt.Move(cx-10, cy)
	tt.Release(cx-10, cy)
	if view.scroll.X != x0+30 || view.scroll.Y != y0+20 {
		t.Errorf("dragging (-30, -20) scrolls to %v/%v, want %v/%v", view.scroll.X, view.scroll.Y, x0+30, y0+20)
	}

	// A pinch zooms too, and the wheel stops at the limits.
	zoom := view.zoom
	tt.Pinch(cx, cy, 2)
	if view.zoom <= zoom {
		t.Errorf("a pinch left the zoom at %v", view.zoom)
	}
	for range 20 {
		tt.Wheel(cx, cy, 0, -1)
	}
	if view.zoom != maxSingleZoom {
		t.Errorf("zooming in ends at %v, want %v", view.zoom, maxSingleZoom)
	}
	for range 20 {
		tt.Wheel(cx, cy, 0, 1)
	}
	if view.zoom != minSingleZoom || view.scroll.X != 0 || view.scroll.Y != 0 {
		t.Errorf("zooming out leaves %v scrolled to %v, want it fitting", view.zoom, view.scroll)
	}

	// A double click fits the photo again.
	tt.Wheel(cx+80, cy+60, 0, -1)
	if view.zoom == minSingleZoom {
		t.Fatal("the wheel did not zoom the photo")
	}
	tt.ClickAt(cx, cy)
	tt.ClickAt(cx, cy)
	if view.zoom != minSingleZoom || view.scroll.X != 0 || view.scroll.Y != 0 {
		t.Errorf("a double click leaves %v scrolled to %v, want it fitting", view.zoom, view.scroll)
	}
}

func TestSingleImageZoomResetsOnArrows(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	tt.Wheel(700, 400, 0, -1)
	view := singleViewNow(t, a)
	if view.zoom == minSingleZoom {
		t.Fatal("the wheel did not zoom the photo")
	}
	// The arrows step to another image, which shows fitting.
	tt.Key(0, ui.KeyRight)
	if a.single != 1 {
		t.Fatalf("the right arrow shows image %d, want beta.png", a.single)
	}
	if got := singleViewNow(t, a); got.zoom != minSingleZoom || got.scroll.X != 0 || got.scroll.Y != 0 {
		t.Errorf("another photo shows at %v scrolled to %v, want it fitting", got.zoom, got.scroll)
	}
}

func TestSingleImageKeepsZoomPerTag(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	tt.Wheel(700, 400, 0, -1)
	view := singleViewNow(t, a)
	zoom, scroll := view.zoom, view.scroll.X
	if zoom == minSingleZoom {
		t.Fatal("the wheel did not zoom the photo")
	}

	// Leaving the view and coming back keeps where the photo was.
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if got := singleViewNow(t, a); got.zoom != zoom || got.scroll.X != scroll {
		t.Errorf("the photo shows at %v scrolled to %v again, want %v/%v", got.zoom, got.scroll.X, zoom, scroll)
	}

	// Another photo opens as another tag, fitting on its own: the first
	// tag keeps where its photo was.
	if err := tt.Click("File tree"); err != nil {
		t.Fatal(err)
	}
	cell, ok := tt.Find("beta.png")
	if !ok {
		t.Fatalf("the image cell is not shown: %q", tt.Texts())
	}
	x, y := cell.X+cell.W/2, cell.Y+cell.H/2
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	if a.single != 1 {
		t.Fatalf("double clicking beta.png shows image %d, want 1", a.single)
	}
	if len(a.tagViews) != 2 {
		t.Fatalf("%d tags for the two photos", len(a.tagViews))
	}
	if got := a.tagViews[0]; got.zoom != zoom || got.scroll.X != scroll {
		t.Errorf("alpha.png kept at %v scrolled to %v, want %v/%v", got.zoom, got.scroll.X, zoom, scroll)
	}
	if got := singleViewNow(t, a); got.zoom != minSingleZoom || got.scroll.X != 0 {
		t.Errorf("beta.png shows at %v scrolled to %v, want it fitting", got.zoom, got.scroll.X)
	}

	// Back to a tag, where its photo was is where it shows again.
	tt.Wheel(700, 400, 0, -1)
	tt.Wheel(700, 400, 0, -1)
	betaZoom := singleViewNow(t, a).zoom
	if err := tt.Click("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if got := singleViewNow(t, a); got.zoom != zoom || got.scroll.X != scroll {
		t.Errorf("alpha.png shows at %v scrolled to %v again, want %v/%v", got.zoom, got.scroll.X, zoom, scroll)
	}
	if err := tt.Click("beta.png"); err != nil {
		t.Fatal(err)
	}
	if got := singleViewNow(t, a); got.zoom != betaZoom {
		t.Errorf("beta.png shows at %v again, want %v", got.zoom, betaZoom)
	}
}

func TestSearchViewFindsImages(t *testing.T) {
	root := t.TempDir()
	found := filepath.Join(root, "beta.png")
	writePNG(t, found, color.RGBA{G: 255, A: 255})

	var got struct{ method, path, accept, query, scope, page string }
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = struct{ method, path, accept, query, scope, page string }{
			r.Method, r.URL.Path, r.Header.Get("Accept"),
			r.URL.Query().Get("query"), r.URL.Query().Get("scope"), r.URL.Query().Get("page"),
		}
		pages = append(pages, got.page)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"list":[{"path":%q,"score":0.75}],"total":4}`, found)
	}))
	defer srv.Close()

	a := syncApp(root)
	a.searchURL = srv.URL
	a.searchQuery, a.searchScope = "beta", root
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Submit"); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != "/search" || got.accept != "application/json" ||
		got.query != "beta" || got.scope != root || got.page != "1" {
		t.Errorf("the search sent %+v, want GET /search?query=beta&scope=%s&page=1", got, root)
	}
	if len(a.searchResults) != 1 || a.searchResults[0].photo.path != found {
		t.Fatalf("the search found %v, want %q", a.searchResults, found)
	}
	if !tt.HasText("0.750") || !tt.HasText("1/4 pages") {
		t.Errorf("the view does not show the result and its page: %q", tt.Texts())
	}

	// The arrows step through the service's pages.
	if err := tt.Click("Next page"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Previous page"); err != nil {
		t.Fatal(err)
	}
	if a.searchPage != 1 || !slices.Equal(pages, []string{"1", "2", "1"}) {
		t.Errorf("the arrows asked for pages %v and left page %d, want 1, 2, 1", pages, a.searchPage)
	}
}
