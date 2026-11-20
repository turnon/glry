package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

// The sqlite search way keeps image embeddings in ~/.glry/db.sqlite and ranks
// them with sqlite-vec. A query is embedded by the service configured in
// embed, then matched against the stored vectors.
const (
	// sqliteVectorDims is the width of the images.vector column.
	sqliteVectorDims = 768
	// sqliteMaxK is the largest k a vec0 KNN query accepts, and so the
	// furthest a page can reach.
	sqliteMaxK = 4096
	// sqliteScopeDirs is how many dirs a scope may hold for its filter to
	// be pushed into the KNN query, where each one costs a pass over the
	// dir column. Wider scopes narrow the nearest rows instead.
	sqliteScopeDirs = 16
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

// sqlite is the sqlite search way: it keeps image embeddings in path and
// ranks them with sqlite-vec.
type sqlite struct {
	// path is the database, and embed embeds a query for the vector table.
	path  string
	embed func(string) ([]float32, error)
}

// defaultSQLitePath is ~/.glry/db.sqlite.
func defaultSQLitePath() string {
	return filepath.Join(glryDir(), "db.sqlite")
}

// search embeds query, asks the SQLite vector table for the nearest images,
// and returns the page wanted. It runs on a goroutine, so the photos it
// makes are ready for the thumbnails the view asks for.
func (s sqlite) search(query, scope string, page int) ([]searchResult, int, error) {
	dbPath := s.path
	if err := s.ensureDB(); err != nil {
		return nil, 0, err
	}
	vector, err := s.embed(query)
	if err != nil {
		return nil, 0, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, 0, fmt.Errorf("opening %s: %w", dbPath, err)
	}
	defer db.Close()

	// total is how many images the scope holds, which the view's pages
	// count. A KNN query filters vec0 metadata with = or in only, so the
	// scope's expression is matched here, over the dirs of the whole table,
	// in the same pass that counts their images.
	match, err := s.scopeMatch(scope)
	if err != nil {
		return nil, 0, err
	}
	dirs, total, err := s.scope(db, match)
	if err != nil {
		return nil, 0, fmt.Errorf("counting %s: %w", dbPath, err)
	}
	if total == 0 {
		return []searchResult{}, 0, nil
	}

	// The vector table wants a vector as JSON text, and its k limit is the
	// number of nearest rows it takes, 4096 at most. A page holds the rows
	// from its offset up to the end of it, so k must reach the end while
	// still leaving the scope's nearest need rows in hand.
	page = max(page, 1)
	offset := (page - 1) * searchPageSize
	need := min(offset+searchPageSize, sqliteMaxK)
	vectorText := s.vectorJSON(vector)

	var results []searchResult
	switch {
	case match == nil:
		results, err = s.nearest(db, vectorText, nil, need)
	case len(dirs) <= sqliteScopeDirs:
		// The scope is narrow enough to filter inside the KNN query, so
		// its k nearest rows are the nearest rows in scope.
		results, err = s.nearestDirs(db, vectorText, dirs, need)
	default:
		// The scope is too wide to filter inside the KNN query, so the
		// nearest rows are taken first and narrowed afterwards.
		results, err = s.nearestInScope(db, vectorText, match, need)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("searching %s: %w", dbPath, err)
	}
	if offset < len(results) {
		results = results[offset:]
	} else {
		results = results[:0]
	}
	if len(results) > searchPageSize {
		results = results[:searchPageSize]
	}
	return results, total, nil
}

// scopeMatch compiles a scope into a matcher for a dir, using the scope as
// the regular expression gmbd's scope was, or returns nil for no scope.
func (s sqlite) scopeMatch(scope string) (func(string) bool, error) {
	if scope == "" {
		return nil, nil
	}
	expression, err := regexp.Compile(scope)
	if err != nil {
		return nil, fmt.Errorf("the scope %q is not a regular expression: %w", scope, err)
	}
	return expression.MatchString, nil
}

// scope counts the images in scope and lists the dirs it matches. A scope
// that matches no dir holds no images.
func (s sqlite) scope(db *sql.DB, match func(string) bool) ([]string, int, error) {
	if match == nil {
		var total int
		err := db.QueryRow(`select count(*) from images`).Scan(&total)
		return nil, total, err
	}
	rows, err := db.Query(`select dir, count(*) from images group by dir`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var (
		dirs  []string
		total int
	)
	for rows.Next() {
		var dir string
		var n int
		if err := rows.Scan(&dir, &n); err != nil {
			return nil, 0, err
		}
		if !match(dir) {
			continue
		}
		dirs = append(dirs, dir)
		total += n
	}
	return dirs, total, rows.Err()
}

// nearestInScope takes the nearest rows that the scope matches. Each pass
// takes k rows of the whole table and the scope narrows them after, so k
// doubles until the scope fills the pages before the wanted one or the table
// runs out.
func (s sqlite) nearestInScope(db *sql.DB, vectorText string, match func(string) bool, need int) ([]searchResult, error) {
	var rows int
	if err := db.QueryRow(`select count(*) from images`).Scan(&rows); err != nil {
		return nil, err
	}
	for k := min(need, rows); ; k = min(k*2, rows) {
		results, err := s.nearest(db, vectorText, match, k)
		if err != nil {
			return nil, err
		}
		if len(results) >= need || k >= rows || k >= sqliteMaxK {
			return results, nil
		}
	}
}

// nearest takes the k rows nearest vector from the images table, keeps those
// whose dir match accepts and returns them with their score, nearest first.
// A nil match keeps every row.
func (s sqlite) nearest(db *sql.DB, vectorText string, match func(string) bool, k int) ([]searchResult, error) {
	rows, err := db.Query(`
		select dir, dir || '/' || name as path, distance as score
		from images
		where vector match ? and k = ?
		order by distance`, vectorText, k)
	if err != nil {
		return nil, err
	}
	return s.results(rows, k, match)
}

// nearestDirs takes the k rows nearest vector among those in dirs. The dirs
// are filtered by the vec0 scan itself, so k counts rows in scope.
func (s sqlite) nearestDirs(db *sql.DB, vectorText string, dirs []string, k int) ([]searchResult, error) {
	args := make([]any, 0, len(dirs)+2)
	args = append(args, vectorText, k)
	for _, dir := range dirs {
		args = append(args, dir)
	}
	rows, err := db.Query(`
		select dir, dir || '/' || name as path, distance as score
		from images
		where vector match ? and k = ? and dir in (`+
		strings.TrimSuffix(strings.Repeat("?,", len(dirs)), ",")+`)
		order by distance`, args...)
	if err != nil {
		return nil, err
	}
	return s.results(rows, k, nil)
}

// results reads a nearest-images query's dir, path and score columns, keeping
// the rows whose dir match accepts. A nil match keeps every row.
func (s sqlite) results(rows *sql.Rows, capacity int, match func(string) bool) ([]searchResult, error) {
	defer rows.Close()
	results := make([]searchResult, 0, capacity)
	for rows.Next() {
		var dir, path string
		var score float64
		if err := rows.Scan(&dir, &path, &score); err != nil {
			return nil, err
		}
		if match != nil && !match(dir) {
			continue
		}
		p := &photo{path: path, name: filepath.Base(path)}
		if info, err := os.Stat(path); err == nil {
			p.size = info.Size()
		}
		results = append(results, searchResult{photo: p, score: score})
	}
	return results, rows.Err()
}

// ensureDB creates the database's path and its images table when they do not
// exist yet, and leaves an existing database as it is.
func (s sqlite) ensureDB() error {
	path := s.path
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

// vectorJSON writes a vector the way sqlite-vec reads one, as
// [0.1,0.2,...].
func (s sqlite) vectorJSON(v []float32) string {
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
