package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	a.startIndex(dir)

	if a.indexErr != "" {
		t.Fatalf("indexing failed: %s", a.indexErr)
	}
	if a.indexRunning || a.indexCount != 2 {
		t.Errorf("the index is running %v after %d images, want done with 2", a.indexRunning, a.indexCount)
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
	a.startIndex(aDir)
	a.startIndex(bDir)
	if requests != 1 || images != 1 {
		t.Fatalf("the embed service got %d requests for %d images, want one image", requests, images)
	}
	// Indexing the same path again skips the row it already holds.
	a.startIndex(bDir)
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
	if len(vectors) != 2 || vectors[0] != vectors[1] {
		t.Errorf("the copies have vectors %v, want the same vector", vectors)
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
