package searcher

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The connections wait for each other's locks and let reads run beside the
// index's writes, rather than fail with SQLITE_BUSY.
func TestSQLiteDSNUsesWALAndWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", (SQLite{Path: path}).dsn())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow(`pragma journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("the journal mode is %q, want wal", mode)
	}
	var timeout int
	if err := db.QueryRow(`pragma busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != sqliteBusyTimeout {
		t.Errorf("the busy timeout is %d, want %d", timeout, sqliteBusyTimeout)
	}
}

// writePNG writes a plain picture of the color to path.
func writePNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	stddraw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, stddraw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// indexColor gives each image a distinct color, so that tests can tell them
// apart.
func indexColor(i int) color.Color {
	return color.RGBA{R: uint8(40 * i), G: uint8(20 * i), B: uint8(10 * i), A: 255}
}

// A batch the embedding service rejects is halved until each image is sent
// alone and can be indexed.
func TestIndexSplitsFailedEmbeddingBatches(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		writePNG(t, filepath.Join(dir, fmt.Sprintf("a%d.png", i)), indexColor(i+1))
	}
	var calls []int
	s := SQLite{
		Path: filepath.Join(dir, "db.sqlite"),
		EmbedImages: func(encoded ...string) ([][]float32, error) {
			calls = append(calls, len(encoded))
			if len(encoded) > 1 {
				return nil, fmt.Errorf("the service refuses batches")
			}
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return [][]float32{vector}, nil
		},
	}
	progress, err := s.Index(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	for range progress {
	}
	if got := fmt.Sprint(calls); got != "[4 2 1 1 2 1 1]" {
		t.Errorf("the embedding service got batches %v, want [4 2 1 1 2 1 1]", calls)
	}

	db, err := sql.Open("sqlite", s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("the database holds %d images, want 4", count)
	}
}

// An image whose embedding still fails alone is logged, skipped, and does
// not stop the images after it.
func TestIndexSkipsImageWhoseEmbeddingFails(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "alpha.png"), indexColor(6))
	writePNG(t, filepath.Join(dir, "beta.png"), indexColor(7))

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	failNext := true
	s := SQLite{
		Path: filepath.Join(dir, "db.sqlite"),
		EmbedImages: func(encoded ...string) ([][]float32, error) {
			if len(encoded) > 1 {
				return nil, fmt.Errorf("the service refuses batches")
			}
			if failNext {
				failNext = false
				return nil, fmt.Errorf("the service cannot read the image")
			}
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return [][]float32{vector}, nil
		},
	}
	progress, err := s.Index(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	total, done, skipped := 0, 0, 0
	for p := range progress {
		total, done, skipped = p.Total, p.Done, p.Skipped
	}
	if total != 2 || done != 2 {
		t.Errorf("the index reported %d of %d images, want 2 of 2", done, total)
	}
	if skipped != 1 {
		t.Errorf("the index reported %d skipped images, want the one that failed to embed", skipped)
	}
	if !strings.Contains(logs.String(), "skipping") {
		t.Errorf("the skipped image was not logged: %q", logs.String())
	}

	db, err := sql.Open("sqlite", s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("the database holds %d images, want the one that embedded", count)
	}
}

// A scope narrows the search to the dirs its regular expression matches
// before the page is taken, in the vector table's query or after it.
func TestSQLiteSearchFiltersScopeInSQL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := (SQLite{Path: dbPath}).EnsureDB(); err != nil {
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
		vector := make([]float32, VectorDims)
		vector[0] = 1 - float32(i)/100
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%d.png", i), imageDir, "digest", SQLite{}.vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := SQLite{
		Path: dbPath,
		EmbedQuery: func(string) ([]float32, error) {
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return vector, nil
		},
	}

	results, total, err := s.Search("anything", bDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || total != 3 {
		t.Fatalf("the scoped search found %d of %d images, want 3 of 3", len(results), total)
	}
	for _, result := range results {
		if got := filepath.Dir(result.Path); got != bDir {
			t.Errorf("%s is outside the scope %s", result.Path, bDir)
		}
	}

	// The scope is a regular expression, so it can pick the dirs by their
	// names rather than by their whole path.
	results, total, err = s.Search("anything", `B$`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || total != 3 {
		t.Fatalf("the %q scope found %d of %d images, want 3 of 3", `B$`, len(results), total)
	}
	for _, result := range results {
		if got := filepath.Dir(result.Path); got != bDir {
			t.Errorf("%s is outside the %q scope", result.Path, `B$`)
		}
	}

	// A scope that is not a regular expression explains why nothing ran.
	if _, _, err := s.Search("anything", "(", 1); err == nil ||
		!strings.Contains(err.Error(), "not a regular expression") {
		t.Errorf("the %q scope searched with %v", "(", err)
	}
}

// The scope is applied after the vector table takes its k nearest rows, so
// a scope whose images all rank past the first page's k must still fill it.
func TestSQLiteSearchScopePastKNearest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := (SQLite{Path: dbPath}).EnsureDB(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	aDir := filepath.Join(dir, "A")
	bDir := filepath.Join(dir, "B")
	// The first 40 images are the nearest and lie in A; the scoped ones
	// in B all rank past k = PageSize.
	for i := 0; i < 60; i++ {
		imageDir := aDir
		if i >= 40 {
			imageDir = bDir
		}
		vector := make([]float32, VectorDims)
		vector[0] = 1 - float32(i)/100
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%02d.png", i), imageDir, "digest", SQLite{}.vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := SQLite{
		Path: dbPath,
		EmbedQuery: func(string) ([]float32, error) {
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return vector, nil
		},
	}

	results, total, err := s.Search("anything", bDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != PageSize {
		t.Fatalf("page 1 found %d images, want %d", len(results), PageSize)
	}
	if first, last := filepath.Base(results[0].Path), filepath.Base(results[len(results)-1].Path); first != "a40.png" || last != "a59.png" {
		t.Errorf("page 1 shows %s..%s, want a40..a59", first, last)
	}
	for _, result := range results {
		if got := filepath.Dir(result.Path); got != bDir {
			t.Errorf("%s is outside the scope %s", result.Path, bDir)
		}
	}
	if total != 20 {
		t.Errorf("the scoped search counted %d images, want 20", total)
	}

	// The scope holds one page, so the next one is empty rather than a
	// repeat of the rows past its end.
	results, _, err = s.Search("anything", bDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("page 2 of a 20-image scope found %d images, want 0", len(results))
	}
}

// A scope too wide to filter inside the KNN query is narrowed after it, so
// its images must still fill a page when they all rank past that k.
func TestSQLiteSearchWideScopePastKNearest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := (SQLite{Path: dbPath}).EnsureDB(); err != nil {
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
		vector := make([]float32, VectorDims)
		vector[0] = 1 - float32(i)/400
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%03d.png", i), imageDir, "digest", SQLite{}.vectorJSON(vector)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := SQLite{
		Path: dbPath,
		EmbedQuery: func(string) ([]float32, error) {
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return vector, nil
		},
	}

	results, total, err := s.Search("anything", "Album", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 20 {
		t.Fatalf("the wide scope found %d images, want 20", len(results))
	}
	if first, last := filepath.Base(results[0].Path), filepath.Base(results[len(results)-1].Path); first != "a100.png" || last != "a119.png" {
		t.Errorf("the wide scope shows %s..%s, want a100..a119", first, last)
	}
	if total != 20 {
		t.Errorf("the wide scope counted %d images, want 20", total)
	}
}

// A scope no dir matches is no images, not a query with an empty dir list.
func TestSQLiteSearchScopeMatchesNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := (SQLite{Path: dbPath}).EnsureDB(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, VectorDims)
	vector[0] = 1
	if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
		"a.png", filepath.Join(dir, "A"), "digest", SQLite{}.vectorJSON(vector)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := SQLite{
		Path: dbPath,
		EmbedQuery: func(string) ([]float32, error) {
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return vector, nil
		},
	}
	results, total, err := s.Search("anything", "no such folder", 1)
	if err != nil {
		t.Fatalf("the empty scope failed: %v", err)
	}
	if len(results) != 0 || total != 0 {
		t.Errorf("the empty scope found %d of %d images, want none", len(results), total)
	}
}

// Search returns the page asked, nearest first, and counts every image it
// ranks.
func TestSQLiteSearchPagesResults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := (SQLite{Path: dbPath}).EnsureDB(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(dir, "Images")
	for i := 0; i < 60; i++ {
		vector := make([]float32, VectorDims)
		vector[0] = 1 - float32(i)/1000
		_, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
			fmt.Sprintf("a%02d.png", i), imageDir, "digest", SQLite{}.vectorJSON(vector))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := SQLite{
		Path: dbPath,
		EmbedQuery: func(string) ([]float32, error) {
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return vector, nil
		},
	}

	results, total, err := s.Search("red car", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != PageSize {
		t.Fatalf("page 1 found %d images, want %d", len(results), PageSize)
	}
	if first, last := filepath.Base(results[0].Path), filepath.Base(results[len(results)-1].Path); first != "a00.png" || last != "a19.png" {
		t.Errorf("page 1 shows %s..%s, want a00..a19", first, last)
	}
	if total != 60 {
		t.Errorf("the search counted %d images, want 60", total)
	}

	results, _, err = s.Search("red car", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != PageSize {
		t.Fatalf("page 2 found %d images, want %d", len(results), PageSize)
	}
	if first, last := filepath.Base(results[0].Path), filepath.Base(results[len(results)-1].Path); first != "a20.png" || last != "a39.png" {
		t.Errorf("page 2 shows %s..%s, want a20..a39", first, last)
	}
}

// A query naming a local image is embedded as that image, so the images
// that look like it come first.
func TestSQLiteSearchEmbedsQueryImage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	if err := (SQLite{Path: dbPath}).EnsureDB(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(dir, "Images")
	vector := make([]float32, VectorDims)
	vector[0] = 1
	if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
		"found.png", imageDir, "digest", SQLite{}.vectorJSON(vector)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	queryImage := filepath.Join(dir, "query.png")
	writePNG(t, queryImage, indexColor(2))
	data, err := os.ReadFile(queryImage)
	if err != nil {
		t.Fatal(err)
	}

	var embedded []string
	s := SQLite{
		Path: dbPath,
		EmbedQuery: func(string) ([]float32, error) {
			t.Error("the query image was embedded as text")
			return vector, nil
		},
		EmbedImages: func(images ...string) ([][]float32, error) {
			embedded = images
			return [][]float32{vector}, nil
		},
	}

	results, _, err := s.Search(queryImage, "", 1)
	if err != nil {
		t.Fatalf("searching by %s failed: %v", queryImage, err)
	}
	if len(embedded) != 1 || embedded[0] != base64.StdEncoding.EncodeToString(data) {
		t.Errorf("the query image was embedded as %v, want its base64 bytes", embedded)
	}
	if len(results) != 1 || results[0].Path != filepath.Join(imageDir, "found.png") {
		t.Errorf("the search found %v, want found.png", results)
	}
}
