package files

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writePNG writes a plain picture of the size given.
func writePNG(t *testing.T, path string, w, h int, c color.Color) {
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

func TestScanSkipsHiddenAndNonImages(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "photo.png"), 4, 3, color.RGBA{A: 255})
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	images, err := ScanImages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].Name != "photo.png" {
		t.Errorf("scanned %v, want photo.png alone", images)
	}
	if images[0].Size <= 0 || images[0].Path != filepath.Join(root, "photo.png") {
		t.Errorf("scanned %+v, want its path and size", images[0])
	}
	dirs, err := ScanDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Errorf("scanned hidden directories: %v", dirs)
	}
}

func TestScanFolderSortsEachList(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "Beta.png"), 4, 3, color.RGBA{A: 255})
	writePNG(t, filepath.Join(root, "alpha.png"), 4, 3, color.RGBA{A: 255})
	if err := os.Mkdir(filepath.Join(root, "zulu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Able"), 0o755); err != nil {
		t.Fatal(err)
	}

	dirs, images, err := ScanFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || filepath.Base(dirs[0]) != "Able" || filepath.Base(dirs[1]) != "zulu" {
		t.Errorf("scanned dirs %v, want Able then zulu", dirs)
	}
	if len(images) != 2 || images[0].Name != "alpha.png" || images[1].Name != "Beta.png" {
		t.Errorf("scanned images %v, want alpha.png then Beta.png", images)
	}
}

func TestDecodeThumbFitsMaxPixels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wide.png")
	writePNG(t, path, 400, 200, color.RGBA{R: 255, A: 255})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	img, w, h, err := DecodeThumb(data, 100)
	if err != nil {
		t.Fatal(err)
	}
	if w != 400 || h != 200 {
		t.Errorf("original is %dx%d, want 400x200", w, h)
	}
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Errorf("thumbnail is %dx%d, want 100x50", b.Dx(), b.Dy())
	}

	img, _, _, err = DecodeThumb(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 200 {
		t.Errorf("unbounded thumbnail is %dx%d, want 400x200", b.Dx(), b.Dy())
	}
}
