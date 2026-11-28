package searcher

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// The postgresql search way keeps image embeddings in a PostgreSQL database
// with the pgvector extension, and ranks them by the inner product of their
// vectors and the query's, which pgvector's <#> operator orders.
//
// The table mirrors the sqlite way's images table: a name, a dir and a
// digest identify a file, and the vector is what a search ranks it by.
var pgSchema = []string{
	`create extension if not exists vector`,
	fmt.Sprintf(`create table if not exists images (
		id bigserial primary key,
		name text not null,
		dir text not null,
		digest text not null,
		vector vector(%d) not null
	)`, VectorDims),
	`create index if not exists images_digest_idx on images (digest)`,
	`create index if not exists images_dir_idx on images (dir)`,
}

// Postgres is the postgresql search way: it keeps image embeddings in the
// database DSN names and ranks them with pgvector.
type Postgres struct {
	// DSN is the database's connection string, EmbedQuery embeds a query
	// for the vector table, and EmbedImages embeds an image for indexing.
	// OnError reports why an index stopped, for a view to show.
	DSN         string
	EmbedQuery  func(string) ([]float32, error)
	EmbedImages func(...string) ([][]float32, error)
	OnError     func(error)
}

// connect opens a connection to p's database and registers pgvector's
// types with it, so that a Vector can be sent and a distance read.
func (p Postgres) connect(ctx context.Context) (*pgx.Conn, error) {
	if strings.TrimSpace(p.DSN) == "" {
		return nil, fmt.Errorf(`the postgresql search way needs a "dsn" in search.backends`)
	}
	conn, err := pgx.Connect(ctx, p.DSN)
	if err != nil {
		return nil, fmt.Errorf("opening the postgresql database: %w", err)
	}
	if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("registering the vector type: %w", err)
	}
	return conn, nil
}

// EnsureDB creates the vector extension and the images table when they do
// not exist yet, and leaves an existing database as it is.
func (p Postgres) EnsureDB(ctx context.Context) error {
	if strings.TrimSpace(p.DSN) == "" {
		return fmt.Errorf(`the postgresql search way needs a "dsn" in search.backends`)
	}
	conn, err := pgx.Connect(ctx, p.DSN)
	if err != nil {
		return fmt.Errorf("opening the postgresql database: %w", err)
	}
	defer conn.Close(ctx)
	for _, statement := range pgSchema {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("preparing the images table: %w", err)
		}
	}
	return nil
}

// Search embeds query, asks the PostgreSQL vector table for the nearest
// images, and returns the page wanted. A query naming a local image is
// embedded as an image, so the images that look like it come first.
func (p Postgres) Search(query, scope string, page int) ([]Result, int, error) {
	ctx := context.Background()
	if err := p.EnsureDB(ctx); err != nil {
		return nil, 0, err
	}
	vector, err := queryVector(query, p.EmbedQuery, p.EmbedImages)
	if err != nil {
		return nil, 0, err
	}
	conn, err := p.connect(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close(ctx)
	return p.searchVector(ctx, conn, vector, scope, page)
}

// searchVector ranks the images nearest vector within scope and returns the
// page wanted, with how many images the scope holds.
func (p Postgres) searchVector(ctx context.Context, conn *pgx.Conn, vector []float32, scope string, page int) ([]Result, int, error) {
	match, err := scopeMatch(scope)
	if err != nil {
		return nil, 0, err
	}
	// total is how many images the scope holds, which the view's count
	// shows. The scope matches dirs, so it is applied to their groups in
	// the same pass that counts their images.
	dirs, total, err := p.scope(ctx, conn, match)
	if err != nil {
		return nil, 0, fmt.Errorf("counting the images: %w", err)
	}
	if total == 0 {
		return []Result{}, 0, nil
	}

	// The page holds the rows from its offset up to the end of it, nearest
	// first, as the sqlite way orders them.
	page = max(page, 1)
	offset := (page - 1) * PageSize
	var rows pgx.Rows
	vec := pgvector.NewVector(vector)
	if match == nil {
		rows, err = conn.Query(ctx, `
			select dir, dir || '/' || name as path, vector <#> $1 as score
			from images
			order by vector <#> $1
			limit $2 offset $3`, vec, PageSize, offset)
	} else {
		rows, err = conn.Query(ctx, `
			select dir, dir || '/' || name as path, vector <#> $1 as score
			from images
			where dir = any($2)
			order by vector <#> $1
			limit $3 offset $4`, vec, dirs, PageSize, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("searching the images: %w", err)
	}
	results, err := p.results(rows, PageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("searching the images: %w", err)
	}
	return results, total, nil
}

// scope counts the images in scope and lists the dirs it matches. A scope
// that matches no dir holds no images.
func (p Postgres) scope(ctx context.Context, conn *pgx.Conn, match func(string) bool) ([]string, int, error) {
	if match == nil {
		var total int
		err := conn.QueryRow(ctx, `select count(*) from images`).Scan(&total)
		return nil, total, err
	}
	rows, err := conn.Query(ctx, `select dir, count(*) from images group by dir`)
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

// results reads a nearest-images query's dir, path and score columns.
func (p Postgres) results(rows pgx.Rows, capacity int) ([]Result, error) {
	defer rows.Close()
	results := make([]Result, 0, capacity)
	for rows.Next() {
		var dir, path string
		var score float64
		if err := rows.Scan(&dir, &path, &score); err != nil {
			return nil, err
		}
		result := Result{Path: path, Score: score}
		if info, err := os.Stat(path); err == nil {
			result.Size = info.Size()
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// pgIndexBatch is how many new images one embedding request carries, and so
// how many images one insert carries.
const pgIndexBatch = 8

// pgIndexImage is an image being indexed, with the bytes its digest was
// taken from and that an embedding needs.
type pgIndexImage struct {
	path, name, dir, digest string
	data                    []byte
}

// pgIndexRecord is an image a batch inserts, with the place of its vector
// among the batch's embeddings: its own, or the one of the image it copies.
type pgIndexRecord struct {
	image  pgIndexImage
	vector int
}

// Index adds the images at path to the database, path being an image or a
// folder whose images it finds, under it too when recursive. Images whose
// digest is already in the database are copied or skipped rather than
// embedded again, and the new ones are embedded a batch at a time. The
// channel reports how many of the images it has handled and how many there
// are, and closes when the index is done; the embedding and insertion run on
// a goroutine.
func (p Postgres) Index(path string, recursive bool) (chan Progress, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.IsDir() && !isEmbeddingImageName(path) {
		return nil, fmt.Errorf("%s is not an image the embedding service reads", path)
	}
	if p.EmbedImages == nil {
		return nil, fmt.Errorf("indexing needs an embedding service")
	}
	ctx := context.Background()
	if err := p.EnsureDB(ctx); err != nil {
		return nil, err
	}
	conn, err := p.connect(ctx)
	if err != nil {
		return nil, err
	}
	progress := make(chan Progress)
	go func() {
		defer close(progress)
		defer conn.Close(ctx)
		paths, err := indexImages(path, info.IsDir(), recursive)
		if err != nil {
			indexFailed(path, err, p.OnError)
			return
		}
		progress <- Progress{Total: len(paths)}
		if err := p.indexFiles(ctx, conn, paths, progress); err != nil {
			indexFailed(path, err, p.OnError)
		}
	}()
	return progress, nil
}

// Remove deletes path from the database: its image row, and, when path is a
// folder, the rows of its images, those in the folders under it too. A path
// that is not in the database is left alone.
func (p Postgres) Remove(path string) error {
	ctx := context.Background()
	if err := p.EnsureDB(ctx); err != nil {
		return err
	}
	conn, err := p.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	prefix := strings.TrimSuffix(path, string(filepath.Separator)) + string(filepath.Separator)
	_, err = conn.Exec(ctx, `delete from images
		where (dir = $1 and name = $2) or dir = $3 or left(dir, length($4)) = $4`,
		filepath.Dir(path), filepath.Base(path), path, prefix)
	if err != nil {
		return fmt.Errorf("removing %s from the postgresql database: %w", path, err)
	}
	return nil
}

// indexFiles adds paths to the database, embedding the images the database
// does not know in batches. It reports each image it handles, with how many
// images there are.
func (p Postgres) indexFiles(ctx context.Context, conn *pgx.Conn, paths []string, progress chan<- Progress) error {
	// A batch keeps the images to embed and a record of each insert, so
	// that a repeated digest in the same batch is embedded once and its
	// copies take its vector.
	var (
		unique  []pgIndexImage
		records []pgIndexRecord
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
	var embedAndInsert func(images []pgIndexImage, records [][]pgIndexRecord) error
	embedAndInsert = func(images []pgIndexImage, records [][]pgIndexRecord) error {
		input := make([]string, len(images))
		for i, image := range images {
			input[i] = base64.StdEncoding.EncodeToString(image.data)
		}
		embedded, err := p.EmbedImages(input...)
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
			vector := pgvector.NewVector(embedded[i])
			for _, record := range records[i] {
				image := record.image
				values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d)",
					rows*4+1, rows*4+2, rows*4+3, rows*4+4))
				args = append(args, image.name, image.dir, image.digest, vector)
				rows++
			}
		}
		if _, err := conn.Exec(ctx, `insert into images(name, dir, digest, vector) values `+
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
		groups := make([][]pgIndexRecord, len(unique))
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
		if err := conn.QueryRow(ctx, `select exists(select 1 from images where digest = $1 and dir = $2 and name = $3)`,
			digest, dir, name).Scan(&known); err != nil {
			return fmt.Errorf("looking up %s: %w", path, err)
		}
		if known {
			report()
			continue
		}

		image := pgIndexImage{path: path, name: name, dir: dir, digest: digest, data: data}
		// A row with the digest alone is another copy of the image: insert
		// this path with that row's vector rather than embedding the same
		// picture again.
		copied, err := conn.Exec(ctx, `insert into images(name, dir, digest, vector)
			select $1, $2, $3, vector from images where digest = $4 limit 1`,
			name, dir, digest, digest)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}
		if copied.RowsAffected() > 0 {
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
		records = append(records, pgIndexRecord{image: image, vector: source})
		if len(records) >= pgIndexBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}
