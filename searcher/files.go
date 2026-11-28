package searcher

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// imageExtensions are the extensions of the images the embedding service
// reads, for indexing and for a query image. The viewer also shows SVG,
// which the service cannot read, so indexing leaves it out.
var imageExtensions = []string{
	".jpg", ".jpeg", ".jfif", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff", ".avif",
}

// EmbeddingImageExtensions returns the extensions of the images the
// embedding service reads, for a file dialog to filter by.
func EmbeddingImageExtensions() []string {
	return slices.Clone(imageExtensions)
}

// isEmbeddingImageName reports whether a file's extension is a format the
// embedding service reads.
func isEmbeddingImageName(name string) bool {
	return slices.Contains(imageExtensions, strings.ToLower(filepath.Ext(name)))
}

// isLocalImageQuery reports whether query names an image file the embedding
// service reads, which the search embeds instead of searching the text.
func isLocalImageQuery(query string) bool {
	if query == "" {
		return false
	}
	info, err := os.Stat(query)
	return err == nil && !info.IsDir() && isEmbeddingImageName(query)
}

// indexImages lists the images at path: path itself when it is a file, else
// the images in it, or under it too when recursive. Hidden files and folders
// are left out of the index.
func indexImages(path string, dir, recursive bool) ([]string, error) {
	if !dir {
		return []string{path}, nil
	}
	if !recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !isEmbeddingImageName(entry.Name()) {
				continue
			}
			paths = append(paths, filepath.Join(path, entry.Name()))
		}
		return paths, nil
	}
	var paths []string
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == path {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isEmbeddingImageName(d.Name()) {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}
