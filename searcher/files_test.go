package searcher

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The embedding service takes only the formats it lists; the viewer's SVGs
// and other files must not reach it.
func TestEmbeddingImageFormats(t *testing.T) {
	for _, name := range []string{
		"photo.jpg", "photo.JPEG", "photo.jfif", "photo.png", "photo.webp",
		"photo.gif", "photo.bmp", "photo.tif", "photo.tiff", "photo.avif",
	} {
		if !isEmbeddingImageName(name) {
			t.Errorf("%s is not passed to the embedding service", name)
		}
	}
	for _, name := range []string{"drawing.svg", "photo.heic", "notes.txt", "image"} {
		if isEmbeddingImageName(name) {
			t.Errorf("%s is passed to the embedding service", name)
		}
	}
}

// Indexing lists only the formats the embedding service reads, leaving the
// viewer's SVGs out.
func TestIndexImagesSkipsFormatsEmbeddingCannotRead(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "alpha.png"), indexColor(6))
	if err := os.WriteFile(filepath.Join(dir, "logo.svg"),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := indexImages(dir, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "alpha.png" {
		t.Errorf("the index lists %v, want alpha.png alone", paths)
	}
}

// Indexing a folder lists its own images, and only reaches the images of the
// folders under it when the index is recursive.
func TestIndexImagesReachesNestedFoldersOnlyWhenRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Trips")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "alpha.png"), indexColor(1))
	writePNG(t, filepath.Join(sub, "beach.png"), indexColor(2))

	names := func(recursive bool) []string {
		paths, err := indexImages(dir, true, recursive)
		if err != nil {
			t.Fatal(err)
		}
		listed := make([]string, len(paths))
		for i, path := range paths {
			listed[i] = filepath.Base(path)
		}
		slices.Sort(listed)
		return listed
	}
	if got := names(false); !slices.Equal(got, []string{"alpha.png"}) {
		t.Errorf("the non-recursive index lists %v, want alpha.png alone", got)
	}
	if got := names(true); !slices.Equal(got, []string{"alpha.png", "beach.png"}) {
		t.Errorf("the recursive index lists %v, want the folder's two images", got)
	}
}
