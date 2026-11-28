package searcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// The gmbd search way asks the local gmbd service which images match a
// query, within a folder scope, and shows what it finds.
const (
	// DefaultGmbdURL is the address of the gmbd service the app starts
	// with.
	DefaultGmbdURL = "http://localhost:8500"
	gmbdTimeout    = 20 * time.Second
	// gmbdMaxBody bounds the response the view reads, so that a runaway
	// service cannot exhaust the app's memory.
	gmbdMaxBody = 8 << 20
)

// Gmbd is the gmbd search way: it asks the gmbd service at URL which images
// match a query.
type Gmbd struct {
	// URL is the service's address.
	URL string
}

// gmbdSearchResponse mirrors the gmbd service's JSON: the images it found,
// highest score first, and how many it holds in all.
type gmbdSearchResponse struct {
	List []struct {
		Path  string  `json:"path"`
		Score float64 `json:"score"`
	} `json:"list"`
	Total int `json:"total"`
}

// Search asks the gmbd service at g.URL which images match query within
// scope on page, and returns what it found and how many it counted in all.
// A query naming a local image reports why the gmbd way cannot search by
// it, as the service embeds text alone.
func (g Gmbd) Search(query, scope string, page int) ([]Result, int, error) {
	if isLocalImageQuery(query) {
		return nil, 0, fmt.Errorf("the gmbd search way can't search by an image; use the sqlite way")
	}
	target, err := url.Parse(strings.TrimSuffix(g.URL, "/") + "/search")
	if err != nil {
		return nil, 0, err
	}
	params := target.Query()
	params.Set("query", query)
	params.Set("scope", scope)
	params.Set("page", strconv.Itoa(page))
	target.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: gmbdTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(detail))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, 0, fmt.Errorf("the gmbd service answered %s: %s", resp.Status, msg)
	}
	var out gmbdSearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, gmbdMaxBody)).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("reading the gmbd service's answer: %w", err)
	}
	results := make([]Result, 0, len(out.List))
	for _, item := range out.List {
		if item.Path == "" {
			continue
		}
		result := Result{Path: item.Path, Score: item.Score}
		if info, err := os.Stat(item.Path); err == nil {
			result.Size = info.Size()
		}
		results = append(results, result)
	}
	total := out.Total
	if total < len(results) {
		total = len(results)
	}
	return results, total, nil
}

// Index is not available from the gmbd search way: the service keeps its own
// index.
func (g Gmbd) Index(path string, recursive bool) (chan Progress, error) {
	return nil, fmt.Errorf("the gmbd search way cannot index images")
}

// Remove is not available from the gmbd search way: the service keeps its
// own index.
func (g Gmbd) Remove(path string) error {
	return fmt.Errorf("the gmbd search way cannot remove images from its index")
}
