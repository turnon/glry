package searcher

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// pgTestDSN returns a DSN that keeps a postgresql test in a schema of its
// own, from GLRY_TEST_POSTGRES_DSN. Without it the tests skip, as they need
// a PostgreSQL database with pgvector.
func pgTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("GLRY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GLRY_TEST_POSTGRES_DSN to run the postgresql tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the test database: %v", err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	schema := fmt.Sprintf("glry_test_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "create schema "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("creating the test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(ctx, "drop schema if exists "+pgx.Identifier{schema}.Sanitize()+" cascade"); err != nil {
			t.Logf("dropping the test schema: %v", err)
		}
	})
	// The vector type lives in public, the images table in the test schema,
	// which comes first.
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + url.Values{"search_path": {schema + ",public"}}.Encode()
}

// pgTestSearcher prepares the database and returns the searcher and a
// connection to fill it with, as a test needs.
func pgTestSearcher(t *testing.T) (Postgres, *pgx.Conn) {
	t.Helper()
	p := Postgres{
		DSN: pgTestDSN(t),
		EmbedQuery: func(string) ([]float32, error) {
			vector := make([]float32, VectorDims)
			vector[0] = 1
			return vector, nil
		},
	}
	ctx := context.Background()
	if err := p.EnsureDB(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := p.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	return p, conn
}

// pgInsert adds an image with a vector whose first component is x, so that
// tests can tell how near the images rank.
func pgInsert(t *testing.T, conn *pgx.Conn, dir, name string, x float32) {
	t.Helper()
	vector := make([]float32, VectorDims)
	vector[0] = x
	if _, err := conn.Exec(context.Background(),
		`insert into images(name, dir, digest, vector) values ($1, $2, $3, $4)`,
		name, dir, "digest", pgvector.NewVector(vector)); err != nil {
		t.Fatal(err)
	}
}

// Search returns the page asked, nearest first, and counts every image it
// ranks.
func TestPostgresSearchPagesResults(t *testing.T) {
	p, conn := pgTestSearcher(t)
	imageDir := filepath.Join(t.TempDir(), "Images")
	for i := 0; i < 60; i++ {
		pgInsert(t, conn, imageDir, fmt.Sprintf("a%02d.png", i), 1-float32(i)/1000)
	}

	results, total, err := p.Search("red car", "", 1)
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

	results, _, err = p.Search("red car", "", 2)
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

// A scope narrows the search to the dirs its regular expression matches
// before the page is taken.
func TestPostgresSearchFiltersScope(t *testing.T) {
	p, conn := pgTestSearcher(t)
	root := t.TempDir()
	aDir, bDir := filepath.Join(root, "A"), filepath.Join(root, "B")
	for i := 0; i < 6; i++ {
		imageDir := aDir
		if i%2 == 1 {
			imageDir = bDir
		}
		pgInsert(t, conn, imageDir, fmt.Sprintf("a%d.png", i), 1-float32(i)/100)
	}

	results, total, err := p.Search("anything", bDir, 1)
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
	results, total, err = p.Search("anything", `B$`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || total != 3 {
		t.Fatalf("the %q scope found %d of %d images, want 3 of 3", `B$`, len(results), total)
	}

	// A scope no dir matches is no images.
	results, total, err = p.Search("anything", "no such folder", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 || total != 0 {
		t.Errorf("the empty scope found %d of %d images, want none", len(results), total)
	}

	// A scope that is not a regular expression explains why nothing ran.
	if _, _, err := p.Search("anything", "(", 1); err == nil ||
		!strings.Contains(err.Error(), "not a regular expression") {
		t.Errorf("the %q scope searched with %v", "(", err)
	}
}

// A query naming a local image is embedded as that image, so the images
// that look like it come first.
func TestPostgresSearchEmbedsQueryImage(t *testing.T) {
	p, conn := pgTestSearcher(t)
	root := t.TempDir()
	imageDir := filepath.Join(root, "Images")
	pgInsert(t, conn, imageDir, "found.png", 1)

	queryImage := filepath.Join(root, "query.png")
	writePNG(t, queryImage, indexColor(2))
	data, err := os.ReadFile(queryImage)
	if err != nil {
		t.Fatal(err)
	}

	var embedded []string
	p.EmbedQuery = func(string) ([]float32, error) {
		t.Error("the query image was embedded as text")
		vector := make([]float32, VectorDims)
		vector[0] = 1
		return vector, nil
	}
	p.EmbedImages = func(images ...string) ([][]float32, error) {
		embedded = images
		vector := make([]float32, VectorDims)
		vector[0] = 1
		return [][]float32{vector}, nil
	}

	results, _, err := p.Search(queryImage, "", 1)
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

// Index embeds the folder's images and inserts them, skipping an image the
// embedding service cannot read, as the sqlite way does.
func TestPostgresIndexAddsImagesAndSkipsFailures(t *testing.T) {
	p, _ := pgTestSearcher(t)
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "alpha.png"), indexColor(6))
	writePNG(t, filepath.Join(dir, "beta.png"), indexColor(7))

	var calls int
	p.EmbedImages = func(images ...string) ([][]float32, error) {
		calls++
		// The batch of two fails, and so does the first image it splits
		// into, so one image is skipped and the other indexed.
		if len(images) > 1 || calls == 2 {
			return nil, fmt.Errorf("the service cannot read the image")
		}
		vectors := make([][]float32, len(images))
		for i := range images {
			vectors[i] = make([]float32, VectorDims)
			vectors[i][0] = float32(i + 1)
		}
		return vectors, nil
	}

	progress, err := p.Index(dir, true)
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

	conn, err := p.connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var count int
	if err := conn.QueryRow(context.Background(), `select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("the database holds %d images, want the one that embedded", count)
	}
}

// A copy of an indexed image takes the indexed image's vector rather than
// being embedded again, and indexing a path already indexed skips it.
func TestPostgresIndexCopiesAndSkipsKnownImages(t *testing.T) {
	p, _ := pgTestSearcher(t)
	root := t.TempDir()
	aDir, bDir := filepath.Join(root, "A"), filepath.Join(root, "B")
	for _, dir := range []string{aDir, bDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(aDir, "one.png")
	writePNG(t, first, indexColor(3))
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bDir, "copy.png"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	var images int
	p.EmbedImages = func(input ...string) ([][]float32, error) {
		images += len(input)
		vectors := make([][]float32, len(input))
		for i := range input {
			vectors[i] = make([]float32, VectorDims)
			vectors[i][0] = 1
		}
		return vectors, nil
	}
	for _, dir := range []string{aDir, bDir, bDir} {
		progress, err := p.Index(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		for range progress {
		}
	}
	if images != 1 {
		t.Errorf("the embed service got %d images, want the one not copied or skipped", images)
	}

	ctx := context.Background()
	conn, err := p.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var count int
	if err := conn.QueryRow(ctx, `select count(*) from images`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("the database holds %d images, want 2", count)
	}
	var same bool
	if err := conn.QueryRow(ctx, `select (select count(distinct vector) from images) = 1`).Scan(&same); err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Error("the copies have different vectors, want the same vector")
	}
}

// Remove takes a folder's images and the images under it out of the
// database, and leaves the ones beside it.
func TestPostgresRemoveFolder(t *testing.T) {
	p, conn := pgTestSearcher(t)
	root := t.TempDir()
	kept, trips, italy := filepath.Join(root, "keep.png"), filepath.Join(root, "Trips"), filepath.Join(root, "Trips", "Italy")
	pgInsert(t, conn, root, filepath.Base(kept), 1)
	pgInsert(t, conn, trips, "beach.png", 0.9)
	pgInsert(t, conn, italy, "colosseum.png", 0.8)

	if err := p.Remove(trips); err != nil {
		t.Fatal(err)
	}
	var paths []string
	rows, err := conn.Query(context.Background(), `select dir || '/' || name from images order by name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != kept {
		t.Errorf("after removing %s the database holds %v, want %s alone", trips, paths, kept)
	}
}
