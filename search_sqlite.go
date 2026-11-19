package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

// The sqlite search way keeps image embeddings in ~/.glry.sqlite and ranks
// them with sqlite-vec. A query is embedded by the service configured in
// embed, then matched against the stored vectors.
const (
	defaultEmbedURL   = "http://localhost:11434/api/embed"
	defaultEmbedModel = "qwen3-embedding:4b"
	// sqliteVectorDims is the width of the images.vector column.
	sqliteVectorDims = 768
	// embedTimeout lets the embedding service load a model on first use.
	embedTimeout = 2 * time.Minute
	// queryEmbeddingPrefix is what the embedding model expects before a
	// search query.
	queryEmbeddingPrefix = "task: search result | query: "
	sqliteMaxBody        = 8 << 20
)

// sqliteSchema creates the images table on a new database. The id column
// auto-increments, so an indexer can insert name, dir, digest and vector
// alone.
const sqliteSchema = `
create virtual table if not exists images using vec0(
	id integer primary key,
	name text,
	dir text,
	digest text,
	vector float[768]
)`

// defaultSQLitePath is ~/.glry.sqlite.
func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".glry.sqlite"
	}
	return filepath.Join(home, ".glry.sqlite")
}

// searchSQLite embeds query, asks the SQLite vector table for the nearest
// images, and returns the page wanted. It runs on a goroutine, so the
// photos it makes are ready for the thumbnails the view asks for.
func searchSQLite(dbPath, embedURL, embedModel, query, scope string, page int) ([]searchResult, int, error) {
	if err := ensureSQLiteDB(dbPath); err != nil {
		return nil, 0, err
	}
	vector, err := embedQuery(embedURL, embedModel, query)
	if err != nil {
		return nil, 0, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, 0, fmt.Errorf("opening %s: %w", dbPath, err)
	}
	defer db.Close()

	var total int
	if scope == "" {
		err = db.QueryRow(`select count(*) from images`).Scan(&total)
	} else {
		err = db.QueryRow(`select count(*) from images where dir like ?`, scopeLikePattern(scope)).Scan(&total)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("counting %s: %w", dbPath, err)
	}

	// The vector table wants a vector as JSON text. Its k limit is the
	// number of nearest rows to fetch, so page n asks for the first
	// searchPageSize*n and keeps the last page of them.
	page = max(page, 1)
	k := searchPageSize * page
	vectorText := vectorJSON(vector)
	var rows *sql.Rows
	if scope == "" {
		rows, err = db.Query(`
			select dir || '/' || name as path, distance as score
			from images
			where vector match ? and k = ?
			order by distance`, vectorText, k)
	} else {
		// sqlite-vec rejects LIKE directly on a vec0 column in a KNN
		// query, so the nearest rows are materialized first and the
		// scope is applied to them as SQL.
		rows, err = db.Query(`
			with knn as materialized (
				select dir || '/' || name as path, dir, distance as score
				from images
				where vector match ? and k = ?
			)
			select path, score from knn where dir like ? order by score`,
			vectorText, k, scopeLikePattern(scope))
	}
	if err != nil {
		return nil, 0, fmt.Errorf("searching %s: %w", dbPath, err)
	}
	defer rows.Close()

	results := make([]searchResult, 0, k)
	for rows.Next() {
		var path string
		var score float64
		if err := rows.Scan(&path, &score); err != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", dbPath, err)
		}
		p := &photo{path: path, name: filepath.Base(path)}
		if info, err := os.Stat(path); err == nil {
			p.size = info.Size()
		}
		results = append(results, searchResult{photo: p, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reading %s: %w", dbPath, err)
	}
	if len(results) > searchPageSize {
		results = results[len(results)-searchPageSize:]
	}
	return results, total, nil
}

// scopeLikePattern turns a scope into a SQL LIKE pattern. A scope that
// already holds a % wildcard is used as it is; otherwise it matches the
// scope anywhere in the directory, as gmbd's regexp scope did.
func scopeLikePattern(scope string) string {
	if strings.Contains(scope, "%") {
		return scope
	}
	return "%" + scope + "%"
}

// ensureSQLiteDB creates dbPath and its images table when they do not
// exist yet, and leaves an existing database as it is.
func ensureSQLiteDB(path string) error {
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("creating %s: %w", dir, err)
			}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("creating %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("creating %s: %w", path, err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(sqliteSchema); err != nil {
		return fmt.Errorf("creating the images table in %s: %w", path, err)
	}
	return nil
}

// embedRequest is Ollama's embed API body, which is also what an
// OpenAI-compatible /v1/embeddings endpoint accepts.
type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// embedResponse holds either Ollama's embeddings or an OpenAI-compatible
// data array.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Data       []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// embedQuery turns query into the vector the sqlite way matches, through
// the embedding service at endpoint.
func embedQuery(endpoint, model, query string) ([]float32, error) {
	if endpoint == "" {
		endpoint = defaultEmbedURL
	}
	if model == "" {
		model = defaultEmbedModel
	}
	body, err := json.Marshal(embedRequest{
		Model:      model,
		Input:      []string{queryEmbeddingPrefix + query},
		Dimensions: sqliteVectorDims,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("embedding %q: %w", query, err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: embedTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking %s to embed the query: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(detail))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("the embedding service answered %s: %s", resp.Status, msg)
	}
	var out embedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, sqliteMaxBody)).Decode(&out); err != nil {
		return nil, fmt.Errorf("reading the embedding service's answer: %w", err)
	}
	var vector []float32
	if len(out.Embeddings) > 0 {
		vector = out.Embeddings[0]
	} else if len(out.Data) > 0 {
		vector = out.Data[0].Embedding
	}
	if len(vector) == 0 {
		return nil, fmt.Errorf("the embedding service returned no embedding for %q", query)
	}
	if len(vector) != sqliteVectorDims {
		return nil, fmt.Errorf("the embedding has %d dimensions, want %d", len(vector), sqliteVectorDims)
	}
	return vector, nil
}

// vectorJSON writes a vector the way sqlite-vec reads one, as
// [0.1,0.2,...].
func vectorJSON(v []float32) string {
	var b strings.Builder
	b.Grow(len(v)*10 + 2)
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
