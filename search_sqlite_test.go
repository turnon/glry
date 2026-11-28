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
	"strconv"
	"strings"
	"testing"

	"github.com/turnon/glry/searcher"
)

// embedRequest is the body the app posts to the embedding service, for
// these tests to read.
type embedRequest struct {
	Model      string `json:"model"`
	Input      any    `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

// ensureSQLiteDB creates an empty images table at path, as the sqlite tests
// fill it.
func ensureSQLiteDB(path string) error {
	return (searcher.SQLite{Path: path}).EnsureDB()
}

// sqliteAt makes the app search the database at path.
func sqliteAt(a *app, path string) {
	a.sqlitePath = path
	a.setSearcher(searchWaySQLite)
}

// vectorJSON writes a vector the way the sqlite tests store one.
func vectorJSON(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
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
			vector := make([]float32, searcher.VectorDims)
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
	a.embed.SetURL(srv.URL)
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
	a.embed.SetURL(srv.URL)
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
