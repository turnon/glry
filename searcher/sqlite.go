package searcher

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
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
	// VectorDims is the width of the images.vector column, the width the
	// embedding service returns.
	VectorDims = 768
	// sqliteMaxK is the largest k a vec0 KNN query accepts, and so the
	// furthest a page can reach.
	sqliteMaxK = 4096
	// sqliteScopeDirs is how many dirs a scope may hold for its filter to
	// be pushed into the KNN query, where each one costs a pass over the
	// dir column. Wider scopes narrow the nearest rows instead.
	sqliteScopeDirs = 16
	// sqliteBusyTimeout is how long a connection waits for another one's
	// lock before it gives up, in milliseconds.
	sqliteBusyTimeout = 5000
)

// sqliteSchema creates the images table on a new database. The id column
// auto-increments, so an indexer can insert name, dir, digest and vector
// alone.
var sqliteSchema = fmt.Sprintf(`
create virtual table if not exists images using vec0(
	id integer primary key,
	name text,
	dir text,
	digest text,
	vector float[%d]
)`, VectorDims)

// SQLite is the sqlite search way: it keeps image embeddings in Path and
// ranks them with sqlite-vec.
type SQLite struct {
	// Path is the database, EmbedQuery embeds a query for the vector table,
	// and EmbedImages embeds an image for indexing. OnError reports why an
	// index stopped, for a view to show.
	Path        string
	EmbedQuery  func(string) ([]float32, error)
	EmbedImages func(...string) ([][]float32, error)
	OnError     func(error)
}

// dsn is the DSN of s's database. WAL lets a search read the database while
// an index writes it, and the busy timeout makes a connection wait for
// another one's lock rather than fail a search or stop an index with
// SQLITE_BUSY.
func (s SQLite) dsn() string {
	u := url.URL{Scheme: "file", Path: s.Path}
	q := u.Query()
	q.Set("_busy_timeout", strconv.Itoa(sqliteBusyTimeout))
	q.Set("_journal_mode", "wal")
	u.RawQuery = q.Encode()
	return u.String()
}

// Search embeds query, asks the SQLite vector table for the nearest images,
// and returns the page wanted. A query naming a local image is embedded as
// an image, so the images that look like it come first. It runs on a
// goroutine, so the results it makes are ready for the thumbnails the view
// asks for.
func (s SQLite) Search(query, scope string, page int) ([]Result, int, error) {
	if err := s.EnsureDB(); err != nil {
		return nil, 0, err
	}
	vector, err := s.queryVector(query)
	if err != nil {
		return nil, 0, err
	}
	return s.searchVector(vector, scope, page)
}

// queryVector embeds query: the local image it names, else its text with the
// prefix the model expects for a search.
func (s SQLite) queryVector(query string) ([]float32, error) {
	if !isLocalImageQuery(query) {
		return s.EmbedQuery(query)
	}
	if s.EmbedImages == nil {
		return nil, fmt.Errorf("searching by image needs an embedding service")
	}
	data, err := os.ReadFile(query)
	if err != nil {
		return nil, fmt.Errorf("reading the query image %s: %w", query, err)
	}
	vectors, err := s.EmbedImages(base64.StdEncoding.EncodeToString(data))
	if err != nil {
		return nil, fmt.Errorf("embedding the query image %s: %w", query, err)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("the embedding service returned no embedding for %s", query)
	}
	return vectors[0], nil
}

// searchVector ranks the images nearest vector within scope and returns the
// page wanted, with how many images the scope holds.
func (s SQLite) searchVector(vector []float32, scope string, page int) ([]Result, int, error) {
	dbPath := s.Path
	db, err := sql.Open("sqlite", s.dsn())
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
		return []Result{}, 0, nil
	}

	// The vector table wants a vector as JSON text, and its k limit is the
	// number of nearest rows it takes, 4096 at most. A page holds the rows
	// from its offset up to the end of it, so k must reach the end while
	// still leaving the scope's nearest need rows in hand.
	page = max(page, 1)
	offset := (page - 1) * PageSize
	need := min(offset+PageSize, sqliteMaxK)
	vectorText := s.vectorJSON(vector)

	var results []Result
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
	if len(results) > PageSize {
		results = results[:PageSize]
	}
	return results, total, nil
}

// scopeMatch compiles a scope into a matcher for a dir, using the scope as
// the regular expression gmbd's scope was, or returns nil for no scope.
func (s SQLite) scopeMatch(scope string) (func(string) bool, error) {
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
func (s SQLite) scope(db *sql.DB, match func(string) bool) ([]string, int, error) {
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
func (s SQLite) nearestInScope(db *sql.DB, vectorText string, match func(string) bool, need int) ([]Result, error) {
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
func (s SQLite) nearest(db *sql.DB, vectorText string, match func(string) bool, k int) ([]Result, error) {
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
func (s SQLite) nearestDirs(db *sql.DB, vectorText string, dirs []string, k int) ([]Result, error) {
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
func (s SQLite) results(rows *sql.Rows, capacity int, match func(string) bool) ([]Result, error) {
	defer rows.Close()
	results := make([]Result, 0, capacity)
	for rows.Next() {
		var dir, path string
		var score float64
		if err := rows.Scan(&dir, &path, &score); err != nil {
			return nil, err
		}
		if match != nil && !match(dir) {
			continue
		}
		result := Result{Path: path, Score: score}
		if info, err := os.Stat(path); err == nil {
			result.Size = info.Size()
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// EnsureDB creates the database's path and its images table when they do not
// exist yet, and leaves an existing database as it is.
func (s SQLite) EnsureDB() error {
	path := s.Path
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

	db, err := sql.Open("sqlite", s.dsn())
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
func (s SQLite) vectorJSON(v []float32) string {
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
// so how many images one insert carries.
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

// Index adds the images at path to the database, path being an image or a
// folder whose images it finds, under it too when recursive. Images whose
// digest is already in the database are copied or skipped rather than
// embedded again, and the new ones are embedded a batch at a time. The
// channel reports how many of the images it has handled and how many there
// are, and closes when the index is done; the embedding and insertion run on
// a goroutine.
func (s SQLite) Index(path string, recursive bool) (chan Progress, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.IsDir() && !isEmbeddingImageName(path) {
		return nil, fmt.Errorf("%s is not an image the embedding service reads", path)
	}
	if s.EmbedImages == nil {
		return nil, fmt.Errorf("indexing needs an embedding service")
	}
	if err := s.EnsureDB(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.dsn())
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", s.Path, err)
	}
	progress := make(chan Progress)
	go func() {
		defer close(progress)
		defer db.Close()
		paths, err := indexImages(path, info.IsDir(), recursive)
		if err != nil {
			s.indexFailed(path, err)
			return
		}
		progress <- Progress{Total: len(paths)}
		if err := s.indexFiles(db, paths, progress); err != nil {
			s.indexFailed(path, err)
		}
	}()
	return progress, nil
}

// Remove deletes path from the database: its image row, and, when path is a
// folder, the rows of its images, those in the folders under it too. A path
// that is not in the database is left alone.
func (s SQLite) Remove(path string) error {
	if err := s.EnsureDB(); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.dsn())
	if err != nil {
		return fmt.Errorf("opening %s: %w", s.Path, err)
	}
	defer db.Close()
	prefix := strings.TrimSuffix(path, string(filepath.Separator)) + string(filepath.Separator)
	_, err = db.Exec(`delete from images
		where (dir = ? and name = ?) or dir = ? or instr(dir, ?) = 1`,
		filepath.Dir(path), filepath.Base(path), path, prefix)
	if err != nil {
		return fmt.Errorf("removing %s from %s: %w", path, s.Path, err)
	}
	return nil
}

// indexFailed logs why indexing path stopped and reports it, when the app
// asked for that.
func (s SQLite) indexFailed(path string, err error) {
	log.Printf("indexing %s: %v", path, err)
	if s.OnError != nil {
		s.OnError(err)
	}
}

// indexFiles adds paths to the database, embedding the images the database
// does not know in batches. It reports each image it handles, with how many
// images there are.
func (s SQLite) indexFiles(db *sql.DB, paths []string, progress chan<- Progress) error {
	// A batch keeps the images to embed and a record of each insert, so
	// that a repeated digest in the same batch is embedded once and its
	// copies take its vector.
	var (
		unique  []sqliteIndexImage
		records []sqliteIndexRecord
		vectors map[string]int
	)
	done, skipped := 0, 0
	// advance reports n images handled, with how many images there are.
	advance := func(n int) {
		done += n
		progress <- Progress{Done: done, Total: len(paths), Skipped: skipped}
	}
	report := func() {
		advance(1)
	}
	// skip reports n images left out of the index.
	skip := func(n int) {
		skipped += n
		advance(n)
	}
	reset := func() {
		unique = unique[:0]
		records = records[:0]
		vectors = nil
	}
	// embedAndInsert embeds the images and inserts them with their vectors.
	// A batch the embedding service rejects is halved and tried again, so
	// that one unreadable image does not stop the index. An image that still
	// fails alone is logged and skipped.
	var embedAndInsert func(images []sqliteIndexImage, records [][]sqliteIndexRecord) error
	embedAndInsert = func(images []sqliteIndexImage, records [][]sqliteIndexRecord) error {
		input := make([]string, len(images))
		for i, image := range images {
			input[i] = base64.StdEncoding.EncodeToString(image.data)
		}
		embedded, err := s.EmbedImages(input...)
		if err != nil {
			if len(images) == 1 {
				log.Printf("skipping %s: %v", images[0].path, err)
				skip(len(records[0]))
				return nil
			}
			mid := len(images) / 2
			if err := embedAndInsert(images[:mid], records[:mid]); err != nil {
				return err
			}
			return embedAndInsert(images[mid:], records[mid:])
		}
		if len(embedded) != len(images) {
			return fmt.Errorf("the embedding service returned %d embeddings for %d images",
				len(embedded), len(images))
		}
		// Insert the batch's images in one statement rather than one per
		// image, and report them once written.
		values := make([]string, 0, len(records))
		args := make([]any, 0, len(records)*4)
		rows := 0
		for i := range images {
			vector := s.vectorJSON(embedded[i])
			for _, record := range records[i] {
				image := record.image
				values = append(values, "(?, ?, ?, ?)")
				args = append(args, image.name, image.dir, image.digest, vector)
				rows++
			}
		}
		if _, err := db.Exec(`insert into images(name, dir, digest, vector) values `+
			strings.Join(values, ", "), args...); err != nil {
			return fmt.Errorf("adding the images: %w", err)
		}
		advance(rows)
		return nil
	}
	flush := func() error {
		if len(records) == 0 {
			reset()
			return nil
		}
		groups := make([][]sqliteIndexRecord, len(unique))
		for _, record := range records {
			groups[record.vector] = append(groups[record.vector], record)
		}
		err := embedAndInsert(unique, groups)
		reset()
		return err
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("indexing %s: %v", path, err)
			skip(1)
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
			report()
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
			report()
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
