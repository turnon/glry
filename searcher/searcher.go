// Package searcher finds images by a query, and keeps the index a search
// asks: the gmbd service's own index, or the SQLite vector database the app
// owns.
package searcher

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
