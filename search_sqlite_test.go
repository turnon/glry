package main

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ensureSQLiteDB creates an empty images table at path, as the sqlite tests
// fill it.
func ensureSQLiteDB(path string) error {
	return sqlite{path: path}.ensureDB()
}

// sqliteAt makes the app search the database at path.
func sqliteAt(a *app, path string) {
	a.sqlitePath = path
	a.setSearcher(searchWaySQLite)
}

// vectorJSON writes a vector the way the sqlite tests store one.
func vectorJSON(v []float32) string {
	return sqlite{}.vectorJSON(v)
}

// newIndexTestServer answers embed requests with one vector per input and
// counts the requests and images it was asked to embed.
func newIndexTestServer(t *testing.T, requests, images *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("reading the embed request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		input, _ := body.Input.([]any)
		if input == nil {
			// A query is one string.
			input = []any{body.Input}
		}
		*requests++
		*images += len(input)
		vectors := make([]string, len(input))
		for i := range input {
			if item, ok := input[i].(map[string]any); ok {
				if _, err := base64.StdEncoding.DecodeString(item["image"].(string)); err != nil {
					t.Errorf("the image %d is not base64: %v", i, err)
				}
			}
			vector := make([]float32, sqliteVectorDims)
			vector[0] = float32(i + 1)
			vectors[i] = vectorJSON(vector)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, strings.Join(vectors, ","))
	}))
}

func TestIndexFromAppEmbedsFolderRecursively(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Trips")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "alpha.png")
	second := filepath.Join(sub, "beach.png")
	writePNG(t, first, indexColor(1))
	writePNG(t, second, indexColor(2))

	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(dir, "db.sqlite")
	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	a.startIndex(dir, true)

	if a.indexErr != "" {
		t.Fatalf("indexing failed: %s", a.indexErr)
	}
	if a.indexRunning || a.indexCount != 2 || a.indexTotal != 2 {
		t.Errorf("the index is running %v after %d of %d images, want done with 2 of 2",
			a.indexRunning, a.indexCount, a.indexTotal)
	}
	if requests != 1 || images != 2 {
		t.Errorf("the embed service got %d requests for %d images, want 1 for 2", requests, images)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`select name, dir, digest from images order by dir, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, dir, digest string
		if err := rows.Scan(&name, &dir, &digest); err != nil {
			t.Fatal(err)
		}
		got[filepath.Join(dir, name)] = digest
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%x", md5.Sum(data))
		if got[path] != want {
			t.Errorf("%s has digest %q, want its md5 %q", path, got[path], want)
		}
	}
	if len(got) != 2 {
		t.Errorf("the database holds %d images, want 2: %v", len(got), got)
	}
}

func TestIndexFromAppCopiesAndSkipsKnownImages(t *testing.T) {
	dir := t.TempDir()
	aDir := filepath.Join(dir, "A")
	bDir := filepath.Join(dir, "B")
	for _, d := range []string{aDir, bDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(aDir, "one.png")
	writePNG(t, first, indexColor(3))
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(bDir, "copy.png")
	if err := os.WriteFile(second, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(dir, "db.sqlite")
	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	a.startIndex(aDir, true)
	a.startIndex(bDir, true)
	if requests != 1 || images != 1 {
		t.Fatalf("the embed service got %d requests for %d images, want one image", requests, images)
	}
	// Indexing the same path again skips the row it already holds.
	a.startIndex(bDir, true)
	if requests != 1 || images != 1 {
		t.Errorf("reindexing asked for %d requests for %d images, want none", requests, images)
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
		t.Errorf("the database holds %d images, want 2", count)
	}
	var vectors []string
	rows, err := db.Query(`select vec_to_json(vector) from images order by dir`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var vector string
		if err := rows.Scan(&vector); err != nil {
			t.Fatal(err)
		}
		vectors = append(vectors, vector)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0] != vectors[1] {
		t.Errorf("the copies have vectors %v, want the same vector", vectors)
	}
}

func TestSQLiteSearchEmbedsQueryImage(t *testing.T) {
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
	vector := make([]float32, sqliteVectorDims)
	vector[0] = 1
	if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
		"found.png", imageDir, "digest", vectorJSON(vector)); err != nil {
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

	var input any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("reading the embed request: %v", err)
		}
		input = body.Input
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"embeddings":[%s]}`, vectorJSON(vector))
	}))
	defer srv.Close()

	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	a.searchQuery = queryImage
	a.searchPage = 1
	a.runSearch()

	if a.searchErr != "" {
		t.Fatalf("searching by %s failed: %s", queryImage, a.searchErr)
	}
	items, ok := input.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("the query was embedded as %#v, want one image", input)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("the query image was embedded as %#v, want an image", items[0])
	}
	if got, want := item["image"], base64.StdEncoding.EncodeToString(data); got != want {
		t.Errorf("the query image was embedded as %v, want its base64 bytes", got)
	}
	if len(a.searchResults) != 1 || a.searchResults[0].photo.path != filepath.Join(imageDir, "found.png") {
		t.Errorf("the search found %v, want found.png", a.searchResults)
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
		input      any
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
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	a.embedModel = "test-embed"
	a.searchQuery = "red car"
	a.searchScope = ""
	a.searchPage = 1
	a.runSearch()

	if got.model != "test-embed" || got.dimensions != sqliteVectorDims ||
		got.input != queryEmbeddingPrefix+"red car" {
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
	sqliteAt(a, dbPath)
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
	sqliteAt(a, dbPath)
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
	sqliteAt(a, dbPath)
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
	sqliteAt(a, dbPath)
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

// The connections wait for each other's locks and let reads run beside the
// index's writes, rather than fail with SQLITE_BUSY.
func TestSQLiteDSNUsesWALAndWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", (sqlite{path: path}).dsn())
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

// A search must keep working while an index writes the same database, as
// Submit and Index do when the user runs them together.
func TestSQLiteIndexAndSearchAtOnce(t *testing.T) {
	dir := t.TempDir()
	imageDir := filepath.Join(dir, "Images")
	if err := os.Mkdir(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const imageCount = 64
	for i := 0; i < imageCount; i++ {
		writePNG(t, filepath.Join(imageDir, fmt.Sprintf("a%02d.png", i)), indexColor(i))
	}

	indexErr := make(chan error, 1)
	s := sqlite{
		path: filepath.Join(dir, "db.sqlite"),
		embed: func(string) ([]float32, error) {
			vector := make([]float32, sqliteVectorDims)
			vector[0] = 1
			return vector, nil
		},
		images: func(encoded ...string) ([][]float32, error) {
			// Keep the index going long enough for searches to overlap it.
			time.Sleep(20 * time.Millisecond)
			vectors := make([][]float32, len(encoded))
			for i := range vectors {
				vectors[i] = make([]float32, sqliteVectorDims)
				vectors[i][0] = float32(i + 1)
			}
			return vectors, nil
		},
		onError: func(err error) {
			select {
			case indexErr <- err:
			default:
			}
		},
	}

	progress, err := s.index(imageDir, true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	total := 0
	go func() {
		for p := range progress {
			total = p.total
		}
		close(done)
	}()

	searches := 0
	var searchErr error
loop:
	for {
		select {
		case <-done:
			break loop
		default:
		}
		if _, _, err := s.search("anything", "", 1); err != nil {
			searchErr = err
			break
		}
		searches++
	}
	<-done
	select {
	case err := <-indexErr:
		t.Fatalf("indexing stopped while searching: %v", err)
	default:
	}
	if searchErr != nil {
		t.Fatalf("searching stopped while indexing: %v", searchErr)
	}
	if searches == 0 {
		t.Fatal("no search ran while indexing")
	}
	if total != imageCount {
		t.Errorf("the index reported %d images, want %d", total, imageCount)
	}
	var count int
	db, err := sql.Open("sqlite", s.dsn())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != imageCount {
		t.Errorf("the database holds %d images, want %d", count, imageCount)
	}
}

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

func TestIndexSkipsFormatsEmbeddingCannotRead(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "alpha.png"), indexColor(6))
	svg := filepath.Join(dir, "logo.svg")
	if err := os.WriteFile(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(dir, "db.sqlite")
	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	a.startIndex(dir, true)

	if a.indexErr != "" {
		t.Fatalf("indexing failed: %s", a.indexErr)
	}
	if a.indexCount != 1 || a.indexTotal != 1 {
		t.Errorf("the index handled %d of %d images, want the PNG alone", a.indexCount, a.indexTotal)
	}
	if requests != 1 || images != 1 {
		t.Errorf("the embed service got %d requests for %d images, want the PNG alone", requests, images)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`select name from images`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "alpha.png" {
		t.Errorf("the database holds %v, want alpha.png alone", names)
	}
}

// A batch the embedding service rejects is halved until each image is sent
// alone and can be indexed.
func TestIndexSplitsFailedEmbeddingBatches(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		writePNG(t, filepath.Join(dir, fmt.Sprintf("a%d.png", i)), indexColor(i+1))
	}
	var calls []int
	s := sqlite{
		path: filepath.Join(dir, "db.sqlite"),
		images: func(encoded ...string) ([][]float32, error) {
			calls = append(calls, len(encoded))
			if len(encoded) > 1 {
				return nil, fmt.Errorf("the service refuses batches")
			}
			vector := make([]float32, sqliteVectorDims)
			vector[0] = 1
			return [][]float32{vector}, nil
		},
	}
	progress, err := s.index(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	for range progress {
	}
	if got := fmt.Sprint(calls); got != "[4 2 1 1 2 1 1]" {
		t.Errorf("the embedding service got batches %v, want [4 2 1 1 2 1 1]", calls)
	}

	db, err := sql.Open("sqlite", s.dsn())
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
	s := sqlite{
		path: filepath.Join(dir, "db.sqlite"),
		images: func(encoded ...string) ([][]float32, error) {
			if len(encoded) > 1 {
				return nil, fmt.Errorf("the service refuses batches")
			}
			if failNext {
				failNext = false
				return nil, fmt.Errorf("the service cannot read the image")
			}
			vector := make([]float32, sqliteVectorDims)
			vector[0] = 1
			return [][]float32{vector}, nil
		},
	}
	progress, err := s.index(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	total, done, skipped := 0, 0, 0
	for p := range progress {
		total, done, skipped = p.total, p.done, p.skipped
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

	db, err := sql.Open("sqlite", s.dsn())
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

// Indexing a folder adds its own images, and only reaches the images of the
// folders under it when the index is recursive.
func TestIndexFolderReachesNestedFoldersOnlyWhenRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Trips")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "alpha.png"), indexColor(1))
	writePNG(t, filepath.Join(sub, "beach.png"), indexColor(2))

	var requests, images int
	srv := newIndexTestServer(t, &requests, &images)
	defer srv.Close()

	dbPath := filepath.Join(dir, "db.sqlite")
	a := syncApp(dir)
	sqliteAt(a, dbPath)
	a.embedURL = srv.URL
	a.startIndex(dir, false)
	if a.indexErr != "" {
		t.Fatalf("indexing failed: %s", a.indexErr)
	}
	if a.indexCount != 1 || a.indexTotal != 1 {
		t.Errorf("the index handled %d of %d images, want the folder's own 1", a.indexCount, a.indexTotal)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	names := func() []string {
		t.Helper()
		rows, err := db.Query(`select name from images order by name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return names
	}
	if got := strings.Join(names(), ","); got != "alpha.png" {
		t.Errorf("the database holds %q, want alpha.png alone", got)
	}

	a.startIndex(dir, true)
	if a.indexErr != "" {
		t.Fatalf("indexing failed: %s", a.indexErr)
	}
	if a.indexCount != 2 || a.indexTotal != 2 {
		t.Errorf("the index handled %d of %d images, want the folder's 2", a.indexCount, a.indexTotal)
	}
	if got := strings.Join(names(), ","); got != "alpha.png,beach.png" {
		t.Errorf("the database holds %q, want the folder's two images", got)
	}
}
