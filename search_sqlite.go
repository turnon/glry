package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log"
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
	// path is the database, embed embeds a query for the vector table, and
	// images embeds an image for indexing. onError reports why an index
	// stopped, for a view to show.
	path    string
	embed   func(string) ([]float32, error)
	images  func(...string) ([][]float32, error)
	onError func(error)
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

	// total is how many images the scope holds, which the view's count
	// shows. A KNN query filters vec0 metadata with = or in only, so the
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

// sqliteIndexBatch is how many new images one embedding request carries, and
// so how many images a batch may insert along with copies of them.
const sqliteIndexBatch = 8

// sqliteIndexImage is an image being indexed, with the bytes its digest was
// taken from and that an embedding needs.
type sqliteIndexImage struct {
	path, name, dir, digest string
	data                    []byte
}

// sqliteIndexRecord is an image a batch inserts, with the place of its vector
// among the batch's embeddings: its own, or the one of the image it copies.
type sqliteIndexRecord struct {
	image  sqliteIndexImage
	vector int
}

// index adds the images at path to the database, path being an image or a
// folder whose images it finds recursively. Images whose digest is already
// in the database are copied or skipped rather than embedded again, and the
// new ones are embedded a batch at a time. The channel reports each image
// handled and closes when the index is done; the embedding and insertion run
// on a goroutine.
func (s sqlite) index(path string) (chan struct{}, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.IsDir() && !isImageName(path) {
		return nil, fmt.Errorf("%s is not an image", path)
	}
	if s.images == nil {
		return nil, fmt.Errorf("indexing needs an embedding service")
	}
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", s.path, err)
	}
	progress := make(chan struct{})
	go func() {
		defer close(progress)
		defer db.Close()
		paths, err := indexImages(path, info.IsDir())
		if err != nil {
			s.indexFailed(path, err)
			return
		}
		if err := s.indexFiles(db, paths, progress); err != nil {
			s.indexFailed(path, err)
		}
	}()
	return progress, nil
}

// indexFailed logs why indexing path stopped and reports it, when the app
// asked for that.
func (s sqlite) indexFailed(path string, err error) {
	log.Printf("indexing %s: %v", path, err)
	if s.onError != nil {
		s.onError(err)
	}
}

// indexImages lists the images at path: path itself when it is a file, else
// the images under it, skipping hidden files and folders.
func indexImages(path string, dir bool) ([]string, error) {
	if !dir {
		return []string{path}, nil
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
		if d.IsDir() || !isImageName(d.Name()) {
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

// indexFiles adds paths to the database, embedding the images the database
// does not know in batches. It reports each image it inserts or skips.
func (s sqlite) indexFiles(db *sql.DB, paths []string, progress chan<- struct{}) error {
	// A batch keeps the images to embed and a record of each insert, so
	// that a repeated digest in the same batch is embedded once and its
	// copies take its vector.
	var (
		unique  []sqliteIndexImage
		records []sqliteIndexRecord
		vectors map[string]int
	)
	reset := func() {
		unique = unique[:0]
		records = records[:0]
		vectors = nil
	}
	flush := func() error {
		if len(records) == 0 {
			reset()
			return nil
		}
		input := make([]string, len(unique))
		for i, image := range unique {
			input[i] = base64.StdEncoding.EncodeToString(image.data)
		}
		embedded, err := s.images(input...)
		if err != nil {
			return err
		}
		if len(embedded) != len(unique) {
			return fmt.Errorf("the embedding service returned %d embeddings for %d images",
				len(embedded), len(unique))
		}
		for _, record := range records {
			image := record.image
			if _, err := db.Exec(`insert into images(name, dir, digest, vector) values (?, ?, ?, ?)`,
				image.name, image.dir, image.digest, s.vectorJSON(embedded[record.vector])); err != nil {
				return fmt.Errorf("adding %s: %w", image.path, err)
			}
			progress <- struct{}{}
		}
		reset()
		return nil
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("indexing %s: %v", path, err)
			continue
		}
		name, dir := filepath.Base(path), filepath.Dir(path)
		digest := fmt.Sprintf("%x", md5.Sum(data))

		// A row with the image's digest and its dir and name is this
		// image already indexed.
		var known bool
		if err := db.QueryRow(`select exists(select 1 from images where digest = ? and dir = ? and name = ?)`,
			digest, dir, name).Scan(&known); err != nil {
			return fmt.Errorf("looking up %s: %w", path, err)
		}
		if known {
			progress <- struct{}{}
			continue
		}

		image := sqliteIndexImage{path: path, name: name, dir: dir, digest: digest, data: data}
		// A row with the digest alone is another copy of the image: insert
		// this path with that row's vector rather than embedding the same
		// picture again.
		copied, err := db.Exec(`insert into images(name, dir, digest, vector)
			select ?, ?, ?, vector from images where digest = ? limit 1`,
			name, dir, digest, digest)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}
		n, err := copied.RowsAffected()
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}
		if n > 0 {
			progress <- struct{}{}
			continue
		}

		// The image is new: embed it with the batch, or, where the batch
		// already holds its digest, take that image's place in its vectors.
		source, seen := vectors[digest]
		if !seen {
			source = len(unique)
			if vectors == nil {
				vectors = map[string]int{}
			}
			vectors[digest] = source
			unique = append(unique, image)
		}
		records = append(records, sqliteIndexRecord{image: image, vector: source})
		if len(records) >= sqliteIndexBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}
