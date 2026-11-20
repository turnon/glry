package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
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
	"strings"
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

// sidebarHas reports whether s shows in the left sidebar, not the folders
// view.
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
		t.Fatalf("folders items are %+v, want two folders before two images", a.items)
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
		t.Errorf("the folders view kept the root's images: %q", tt.Texts())
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

func TestRailSwitchesViews(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, filepath.Join(root, "beta.png"), color.RGBA{G: 255, A: 255})
	a := syncApp(root)
	tt := ui.NewTester(a.view, 1000, 640)

	if a.mode != viewFolders {
		t.Fatalf("the window starts in view %d, want the folders view", a.mode)
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
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewFolders || !tt.HasText("alpha.png") || !tt.HasText("beta.png") {
		t.Fatalf("clicking the folders button shows view %d with %q, want its images", a.mode, tt.Texts())
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

	// The eye opens the photo the folders view chose.
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	a.chosen = slices.IndexFunc(a.items, func(it folderItem) bool { return it.name == "alpha.png" })
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
	alpha := slices.IndexFunc(a.items, func(it folderItem) bool { return it.name == "alpha.png" })
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
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Trips"); err != nil {
		t.Fatal(err)
	}
	beach := slices.IndexFunc(a.items, func(it folderItem) bool { return it.name == "beach.png" })
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

	// Show alpha, then beta from the folders view: each opens a tag.
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	beta := slices.IndexFunc(a.items, func(it folderItem) bool { return it.name == "beta.png" })
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

	// Show alpha, then beta from the folders view: each opens a tag.
	if err := tt.Click("Single image"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	a.chosen = slices.IndexFunc(a.items, func(it folderItem) bool { return it.name == "beta.png" })
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

	// Escape closes the current tag, the last, and returns to the folders view.
	tt.Key(0, ui.KeyEscape)
	if len(a.history) != 0 || a.tag != -1 {
		t.Errorf("closing the last tag leaves %v with tag %d", a.history, a.tag)
	}
	if a.mode != viewFolders {
		t.Errorf("closing the last tag shows view %d, want the folders view", a.mode)
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
	if err := tt.Click("Folders"); err != nil {
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
	if err := tt.Click("Folders"); err != nil {
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
	a.gmbdURL = srv.URL
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

func TestFoldersAndSearchKeepOwnState(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha.png")
	beta := filepath.Join(root, "beta.png")
	writePNG(t, alpha, color.RGBA{R: 255, A: 255})
	writePNG(t, beta, color.RGBA{G: 255, A: 255})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"list":[{"path":%q,"score":0.6},{"path":%q,"score":0.5}],"total":2}`,
			alpha, beta)
	}))
	defer srv.Close()

	// Both views list images, each with the item it chose.
	a := syncApp(root)
	a.gmbdURL = srv.URL
	a.searchQuery = "beta"
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.Click("alpha.png"); err != nil {
		t.Fatal(err)
	}
	if got := a.items[a.chosen].name; got != "alpha.png" {
		t.Fatalf("the folders view chose %q, want alpha.png", got)
	}

	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Submit"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("beta.png"); err != nil {
		t.Fatal(err)
	}
	if a.searchChosen != 1 {
		t.Fatalf("the search chose result %d, want beta.png's, the second", a.searchChosen)
	}
	if a.chosen != 0 || a.items[a.chosen].name != "alpha.png" {
		t.Errorf("the search changed the folders view's choice to %q", a.items[a.chosen].name)
	}

	// Back in the folders view, each view keeps its own choice.
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	if a.searchChosen != 1 || a.chosen != 0 || a.items[a.chosen].name != "alpha.png" {
		t.Errorf("the folders view shows choices %d and %d, want each view to keep its own",
			a.chosen, a.searchChosen)
	}
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if a.searchChosen != 1 || !tt.HasText("beta.png") {
		t.Errorf("the search view lost its choice %d: %q", a.searchChosen, tt.Texts())
	}
}

func TestSearchResultKeepsFoldersView(t *testing.T) {
	root := t.TempDir()
	trips := filepath.Join(root, "Trips")
	if err := os.Mkdir(trips, 0o755); err != nil {
		t.Fatal(err)
	}
	beach := filepath.Join(trips, "beach.png")
	sunset := filepath.Join(trips, "sunset.png")
	writePNG(t, filepath.Join(root, "alpha.png"), color.RGBA{R: 255, A: 255})
	writePNG(t, beach, color.RGBA{B: 255, A: 255})
	writePNG(t, sunset, color.RGBA{G: 255, B: 255, A: 255})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"list":[{"path":%q,"score":0.6},{"path":%q,"score":0.5}],"total":2}`,
			beach, sunset)
	}))
	defer srv.Close()

	a := syncApp(root)
	a.gmbdURL = srv.URL
	a.searchQuery = "beach"
	tt := ui.NewTester(a.view, 1000, 640)
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Submit"); err != nil {
		t.Fatal(err)
	}

	// Doubling onto a result shows it alone, stepping through the results.
	cell, ok := tt.Find("beach.png")
	if !ok {
		t.Fatalf("the result is not shown: %q", tt.Texts())
	}
	x, y := cell.X+cell.W/2, cell.Y+cell.H/2
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	if a.mode != viewSingle {
		t.Fatalf("double clicking a result shows view %d, want the single image", a.mode)
	}
	if p := a.singlePhoto(); p == nil || p.name != "beach.png" {
		t.Fatalf("the view shows %v, want beach.png", p)
	}
	if a.selected != root {
		t.Errorf("the folders view shows folder %q, want %q", a.selected, root)
	}
	if len(a.photos) != 1 || a.photos[0].name != "alpha.png" {
		t.Errorf("the folders view's images are %v, want alpha.png alone", a.photos)
	}

	tt.Key(0, ui.KeyRight)
	if p := a.singlePhoto(); p == nil || p.name != "sunset.png" {
		t.Errorf("the right arrow shows %v, want the next result", p)
	}
	tt.Key(0, ui.KeyRight)
	if p := a.singlePhoto(); p == nil || p.name != "sunset.png" {
		t.Errorf("the arrow past the last result shows %v", p)
	}

	// The folders view still lists the folder it showed.
	if err := tt.Click("Folders"); err != nil {
		t.Fatal(err)
	}
	if a.selected != root || !tt.HasText("alpha.png") || tt.HasText("beach.png") {
		t.Errorf("the folders view shows folder %q: %q", a.selected, tt.Texts())
	}
}

func TestSQLiteSearchFindsImages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := ensureSQLiteDB(dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(dir, "Images")
	for i := 0; i < 60; i++ {
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1 - float32(i)/1000
		_, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%02d.png", i), imageDir, "digest", vectorJSON(vector))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var got struct {
		model      string
		input      []string
		dimensions int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("reading the embed request: %v", err)
		}
		got.model, got.input, got.dimensions = body.Model, body.Input, body.Dimensions
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, vectorJSON(vector))
	}))
	defer srv.Close()

	a := syncApp(dir)
	a.searchWay = searchWaySQLite
	a.sqlitePath = dbPath
	a.embedURL = srv.URL
	a.embedModel = "test-embed"
	a.searchQuery = "red car"
	a.searchScope = ""
	a.searchPage = 1
	a.runSearch()

	if got.model != "test-embed" || got.dimensions != sqliteVectorDims ||
		len(got.input) != 1 || got.input[0] != queryEmbeddingPrefix+"red car" {
		t.Errorf("the query was embedded as %+v", got)
	}
	if len(a.searchResults) != searchPageSize {
		t.Fatalf("page 1 found %d images, want %d", len(a.searchResults), searchPageSize)
	}
	if first, last := a.searchResults[0].photo.name, a.searchResults[len(a.searchResults)-1].photo.name; first != "a00.png" || last != "a19.png" {
		t.Errorf("page 1 shows %s..%s, want a00..a19", first, last)
	}
	if a.searchTotal != 60 {
		t.Errorf("the search counted %d images, want 60", a.searchTotal)
	}

	a.searchPage = 2
	a.runSearch()
	if len(a.searchResults) != searchPageSize {
		t.Fatalf("page 2 found %d images, want %d", len(a.searchResults), searchPageSize)
	}
	if first, last := a.searchResults[0].photo.name, a.searchResults[len(a.searchResults)-1].photo.name; first != "a20.png" || last != "a39.png" {
		t.Errorf("page 2 shows %s..%s, want a20..a39", first, last)
	}
}

func TestSQLiteSearchFiltersScopeInSQL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := ensureSQLiteDB(dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	aDir := filepath.Join(dir, "A")
	bDir := filepath.Join(dir, "B")
	for i := 0; i < 6; i++ {
		imageDir := aDir
		if i%2 == 1 {
			imageDir = bDir
		}
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1 - float32(i)/100
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%d.png", i), imageDir, "digest", vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, vectorJSON(vector))
	}))
	defer srv.Close()

	a := syncApp(dir)
	a.searchWay = searchWaySQLite
	a.sqlitePath = dbPath
	a.embedURL = srv.URL
	a.searchQuery = "anything"
	a.searchScope = bDir
	a.searchPage = 1
	a.runSearch()

	if len(a.searchResults) != 3 {
		t.Fatalf("the scoped search found %d images, want 3", len(a.searchResults))
	}
	for _, result := range a.searchResults {
		if got := filepath.Dir(result.photo.path); got != bDir {
			t.Errorf("%s is outside the scope %s", result.photo.path, bDir)
		}
	}
	if a.searchTotal != 3 {
		t.Errorf("the scoped search counted %d images, want 3", a.searchTotal)
	}

	// The scope is a regular expression, so it can pick the dirs by their
	// names rather than by their whole path.
	a.searchScope = `B$`
	a.runSearch()
	if len(a.searchResults) != 3 || a.searchTotal != 3 {
		t.Fatalf("the %q scope found %d of %d images, want 3 of 3", a.searchScope,
			len(a.searchResults), a.searchTotal)
	}
	for _, result := range a.searchResults {
		if got := filepath.Dir(result.photo.path); got != bDir {
			t.Errorf("%s is outside the %q scope", result.photo.path, a.searchScope)
		}
	}

	// A scope that is not a regular expression explains why nothing ran.
	a.searchScope = "("
	a.runSearch()
	if !strings.Contains(a.searchErr, "not a regular expression") {
		t.Errorf("the %q scope failed with %q", a.searchScope, a.searchErr)
	}
}

// The scope is applied after the vector table takes its k nearest rows, so
// a scope whose images all rank past the first page's k must still fill it.
func TestSQLiteSearchScopePastKNearest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := ensureSQLiteDB(dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	aDir := filepath.Join(dir, "A")
	bDir := filepath.Join(dir, "B")
	// The first 40 images are the nearest and lie in A; the scoped ones
	// in B all rank past k = searchPageSize.
	for i := 0; i < 60; i++ {
		imageDir := aDir
		if i >= 40 {
			imageDir = bDir
		}
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1 - float32(i)/100
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%02d.png", i), imageDir, "digest", vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, vectorJSON(vector))
	}))
	defer srv.Close()

	a := syncApp(dir)
	a.searchWay = searchWaySQLite
	a.sqlitePath = dbPath
	a.embedURL = srv.URL
	a.searchQuery = "anything"
	a.searchScope = bDir
	a.searchPage = 1
	a.runSearch()

	if len(a.searchResults) != searchPageSize {
		t.Fatalf("page 1 found %d images, want %d", len(a.searchResults), searchPageSize)
	}
	if first, last := a.searchResults[0].photo.name, a.searchResults[len(a.searchResults)-1].photo.name; first != "a40.png" || last != "a59.png" {
		t.Errorf("page 1 shows %s..%s, want a40..a59", first, last)
	}
	for _, result := range a.searchResults {
		if got := filepath.Dir(result.photo.path); got != bDir {
			t.Errorf("%s is outside the scope %s", result.photo.path, bDir)
		}
	}
	if a.searchTotal != 20 {
		t.Errorf("the scoped search counted %d images, want 20", a.searchTotal)
	}

	// The scope holds one page, so the next one is empty rather than a
	// repeat of the rows past its end.
	a.searchPage = 2
	a.runSearch()
	if len(a.searchResults) != 0 {
		t.Errorf("page 2 of a 20-image scope found %d images, want 0", len(a.searchResults))
	}
}

// A scope too wide to filter inside the KNN query is narrowed after it, so
// its images must still fill a page when they all rank past that k.
func TestSQLiteSearchWideScopePastKNearest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := ensureSQLiteDB(dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 100 near images in A, then one image in each of 20 dirs the scope
	// holds: more dirs than filter inside the KNN query, all ranked past
	// the nearest rows a first pass takes.
	for i := 0; i < 120; i++ {
		imageDir := filepath.Join(dir, "A")
		if i >= 100 {
			imageDir = filepath.Join(dir, "Album", fmt.Sprintf("d%02d", i-100))
		}
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1 - float32(i)/400
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%03d.png", i), imageDir, "digest", vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vector := make([]float32, sqliteVectorDims)
		vector[0] = 1
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, vectorJSON(vector))
	}))
	defer srv.Close()

	a := syncApp(dir)
	a.searchWay = searchWaySQLite
	a.sqlitePath = dbPath
	a.embedURL = srv.URL
	a.searchQuery = "anything"
	a.searchScope = "Album"
	a.searchPage = 1
	a.runSearch()

	if len(a.searchResults) != 20 {
		t.Fatalf("the wide scope found %d images, want 20", len(a.searchResults))
	}
	if first, last := a.searchResults[0].photo.name, a.searchResults[len(a.searchResults)-1].photo.name; first != "a100.png" || last != "a119.png" {
		t.Errorf("the wide scope shows %s..%s, want a100..a119", first, last)
	}
	if a.searchTotal != 20 {
		t.Errorf("the wide scope counted %d images, want 20", a.searchTotal)
	}
}

// A scope no dir matches is no images, not a query with an empty dir list.
func TestSQLiteSearchScopeMatchesNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := ensureSQLiteDB(dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, sqliteVectorDims)
	vector[0] = 1
	if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
		"a.png", filepath.Join(dir, "A"), "digest", vectorJSON(vector)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, vectorJSON(vector))
	}))
	defer srv.Close()

	a := syncApp(dir)
	a.searchWay = searchWaySQLite
	a.sqlitePath = dbPath
	a.embedURL = srv.URL
	a.searchQuery = "anything"
	a.searchScope = "no such folder"
	a.searchPage = 1
	a.runSearch()

	if a.searchErr != "" {
		t.Fatalf("the empty scope failed: %s", a.searchErr)
	}
	if len(a.searchResults) != 0 || a.searchTotal != 0 {
		t.Errorf("the empty scope found %d of %d images, want none", len(a.searchResults), a.searchTotal)
	}
}
