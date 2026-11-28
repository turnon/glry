// Package searcher finds images by a query, and keeps the index a search
// asks: the gmbd service's own index, or the SQLite or PostgreSQL vector
// database the app owns.
package searcher

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"regexp"
)

// PageSize is how many images a search page holds.
const PageSize = 20

// Result is one image a search found, with its score and the size of the
// file the view reads.
type Result struct {
	Path  string
	Size  int64
	Score float64
}

// Progress says how far an index got: how many images it has handled, how
// many it has to handle, and how many of them it left out of the index.
type Progress struct {
	Done, Total, Skipped int
}

// Searcher is a search way: it finds the images matching a query within a
// scope, one page at a time, indexes the images at a path, those under a
// folder too when recursive, and removes a path from its index.
type Searcher interface {
	Search(query, scope string, page int) ([]Result, int, error)
	Index(path string, recursive bool) (chan Progress, error)
	Remove(path string) error
}

// queryVector embeds query: the local image it names, else its text with
// the prefix the model expects for a search. The sqlite and postgresql
// ways embed a query the same way.
func queryVector(query string, embedQuery func(string) ([]float32, error), embedImages func(...string) ([][]float32, error)) ([]float32, error) {
	if !isLocalImageQuery(query) {
		return embedQuery(query)
	}
	if embedImages == nil {
		return nil, fmt.Errorf("searching by image needs an embedding service")
	}
	data, err := os.ReadFile(query)
	if err != nil {
		return nil, fmt.Errorf("reading the query image %s: %w", query, err)
	}
	vectors, err := embedImages(base64.StdEncoding.EncodeToString(data))
	if err != nil {
		return nil, fmt.Errorf("embedding the query image %s: %w", query, err)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("the embedding service returned no embedding for %s", query)
	}
	return vectors[0], nil
}

// scopeMatch compiles a scope into a matcher for a dir, using the scope as
// the regular expression gmbd's scope was, or returns nil for no scope.
func scopeMatch(scope string) (func(string) bool, error) {
	if scope == "" {
		return nil, nil
	}
	expression, err := regexp.Compile(scope)
	if err != nil {
		return nil, fmt.Errorf("the scope %q is not a regular expression: %w", scope, err)
	}
	return expression.MatchString, nil
}

// indexFailed logs why indexing path stopped and reports it to onError,
// when the app asked for that.
func indexFailed(path string, err error, onError func(error)) {
	log.Printf("indexing %s: %v", path, err)
	if onError != nil {
		onError(err)
	}
}
