package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// searchPageSize is how many images a search page holds.
const searchPageSize = 20

// searchResult is one image the search service found, with its score.
type searchResult struct {
	photo *photo
	score float64
}

// showSearch opens the search view, keeping what the last search found.
func (a *app) showSearch() {
	if a.mode == viewSearch {
		return
	}
	a.mode = viewSearch
	a.clearSingle()
}

// searchView searches the folder tree's images and shows the results as a
// grid.
func (a *app) searchView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.searchHeader(c)
		ui.Divider(c)
		a.searchResultsView(c)
	})
}

// searchHeader holds the search view's query, scope, the button that runs
// the search, and the arrows that step through its pages. Enter in either
// input submits it too.
func (a *app) searchHeader(c *ui.Context) {
	submit := func() {
		a.searchPage = 1
		a.searchPageSize = 0
		a.runSearch()
	}
	ui.Row(c).FillWidth().Padding(14, 24).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.SearchField(c, &a.searchQuery).Label("Query").
			Placeholder("Words to look for").Grow(1).MaxWidth(480).
			OnSubmit(submit)
		a.scopeField(c, submit)
		ui.PrimaryButton(c, "Submit").Tooltip("Run the search").OnClick(submit)
		ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
			a.pageButton(c, "Previous page", "Previous page", chevronLeftIcon, a.searchPage <= 1, func() {
				if a.searchPage > 1 {
					a.searchPage--
					a.runSearch()
				}
			})
			a.pageButton(c, "Next page", "Next page", chevronRightIcon, !a.searchHasMore(), func() {
				a.searchPage++
				a.runSearch()
			})
			ui.Text(c, fmt.Sprintf("%d/%d pages", a.searchPage, a.searchPages())).
				FontSize(12).TextColor(c.Theme().TextMuted).PaddingStart(4)
		})
	})
}

// scopeField is the scope input, with a clear icon inside it while it
// holds a folder. The icon is the one SearchField draws, a filled circle
// with a cross cut out of it, so that the two inputs' clear icons match.
func (a *app) scopeField(c *ui.Context, submit func()) {
	t := c.Theme()
	ui.Box(c).Width(260).Children(func() {
		input := ui.TextInput(c, &a.searchScope).FillWidth().Label("Scope").
			Placeholder("scope").OnSubmit(submit)
		if a.searchScope == "" {
			return
		}
		input.PaddingEnd(28)
		ui.Row(c).Absolute().Top(0).Bottom(0).Right(6).AlignItems(ui.Center).Children(func() {
			clear := ui.ButtonBase(c).Size(t.Space(4), t.Space(4)).Radius(t.Space(2)).
				Background(t.TextMuted.Alpha(0.5)).
				Label("Clear scope").Tooltip("Clear the scope").
				OnClick(func() { a.searchScope = "" })
			clear.Draw(func(p *ui.Painter, r ui.Rect) {
				k := r.W * 0.3
				var x ui.Path
				x.MoveTo(r.X+k, r.Y+k).LineTo(r.X+r.W-k, r.Y+r.H-k)
				x.MoveTo(r.X+r.W-k, r.Y+k).LineTo(r.X+k, r.Y+r.H-k)
				p.StrokePath(&x, 1.5, t.Background)
			})
		})
	})
}

// pageButton is one arrow of the search header's pagination, disabled when
// there is no page that way.
func (a *app) pageButton(c *ui.Context, label, tooltip string, icon *ui.SVG, disabled bool, onClick func()) {
	t := c.Theme()
	btn := ui.ButtonBase(c).Label(label).Tooltip(tooltip).Size(32, 32).
		Radius(t.Radius).Disabled(disabled).OnClick(onClick)
	btn.Children(func() {
		ui.Icon(c, icon).Size(16, 16)
	})
	if disabled {
		btn.TextColor(t.TextMuted)
		return
	}
	switch {
	case btn.Pressed():
		btn.Background(t.SurfacePressed)
	case btn.Hovered():
		btn.Background(t.SurfaceHover)
	}
}

// searchHasMore reports whether the total reaches past the page shown, the
// results it holds giving the page's size.
func (a *app) searchHasMore() bool {
	n := len(a.searchResults)
	return n > 0 && a.searchPage*n < a.searchTotal
}

// searchPages is how many pages the total takes, the largest page of
// results seen giving a page's size.
func (a *app) searchPages() int {
	size := a.searchPageSize
	if size <= 0 || a.searchTotal <= 0 {
		return max(a.searchPage, 1)
	}
	return max((a.searchTotal+size-1)/size, 1)
}

// searchResultsView shows what the search found: a message for a search on its
// way, one that failed or one that found nothing, and otherwise the images
// as the folders view does.
func (a *app) searchResultsView(c *ui.Context) {
	t := c.Theme()
	switch {
	case a.searchLoading:
		a.gridMessage(c, "Searching…", "", true)
		return
	case a.searchErr != "":
		a.gridMessage(c, "Can't search", a.searchErr, false)
		return
	case a.searchResults == nil:
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			ui.Icon(c, searchIcon).Size(42, 42).TextColor(t.TextMuted)
			ui.Text(c, "Search the images by name").TextColor(t.TextMuted)
		})
		return
	case len(a.searchResults) == 0:
		a.gridMessage(c, "No images found", "Try another query or scope.", false)
		return
	}
	gen := a.searchGen
	ui.Column(c).Fill().Children(func() {
		a.searchGrid.Key = func(i int) any {
			if i >= 0 && i < len(a.searchResults) {
				return a.searchResults[i].photo.path
			}
			return i
		}
		a.searchGrid.Selected = &a.searchChosen
		a.searchGrid.Label = func(i int) string {
			if i >= 0 && i < len(a.searchResults) {
				return a.searchResults[i].photo.name
			}
			return ""
		}
		grid := ui.GridView(c, &a.searchGrid, len(a.searchResults), a.cellWidth, a.cellHeight, func(i int) {
			a.searchCell(c, gen, i)
		}).Grow(1).Padding(12)
		if grid.Submitted() {
			a.openSearchResult(c)
		}
	})
}

// searchCell shows one image the search found, with its score.
func (a *app) searchCell(c *ui.Context, gen uint64, i int) {
	if i < 0 || i >= len(a.searchResults) {
		return
	}
	result := a.searchResults[i]
	p := result.photo
	if p == nil {
		return
	}
	t := c.Theme()
	pad, gap, iconSize, showName := a.cellMetrics()
	showMeta := a.cellHeight >= 64 && a.cellWidth >= 60
	ui.Column(c).Fill().Padding(pad).Gap(gap).Children(func() {
		if p.img != nil {
			ui.Image(c, p.img).Grow(1).Fit(ui.Contain).MinHeight(0)
		} else {
			a.requestThumb(func() bool { return gen == a.searchGen }, p)
			ui.Column(c).Grow(1).Center().Children(func() {
				if p.state == thumbFailed && showName {
					ui.Text(c, "Can't open").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Icon(c, pictureIcon).Size(iconSize, iconSize).TextColor(t.TextMuted)
				}
			})
		}
		if showMeta {
			ui.Text(c, fmt.Sprintf("%.3f", result.score)).
				FontSize(10).TextColor(t.TextMuted).SingleLine().TextAlign(ui.Center)
		}
		if showName {
			ui.Text(c, p.name).FontSize(12).SingleLine().
				TextAlign(ui.Center).FillWidth().Tooltip(p.path)
		}
	})
}

// openSearchResult shows the chosen result alone, stepping through the
// results with the arrows: the folders view keeps the folder it shows.
func (a *app) openSearchResult(c *ui.Context) {
	i := a.searchChosen
	if i < 0 || i >= len(a.searchResults) {
		return
	}
	p := a.searchResults[i].photo
	if p == nil {
		return
	}
	// The view steps through the results, not the folder's images, so
	// that opening one here leaves the folders view as it was.
	photos := make([]*photo, 0, len(a.searchResults))
	at := -1
	for _, result := range a.searchResults {
		if result.photo == nil {
			continue
		}
		if result.photo == p {
			at = len(photos)
		}
		photos = append(photos, result.photo)
	}
	if at < 0 {
		return
	}
	// A tag the view opened before shows the photo again, as choosing it
	// from the folders view does.
	a.clearSingle()
	tag := -1
	for j := len(a.history) - 1; j >= 0; j-- {
		if a.history[j] == p.path {
			tag = j
			break
		}
	}
	if tag >= 0 {
		a.tag = tag
	} else {
		a.addTag(p)
	}
	a.setSingleIn(photos, at)
	c.Invalidate()
}

// searcher is a search way: it finds the images matching a query within a
// scope, one page at a time.
type searcher interface {
	search(query, scope string, page int) ([]searchResult, int, error)
}

// setSearcher chooses the backend the search asks, from the configured way.
func (a *app) setSearcher(way searchWay) {
	if way == searchWaySQLite {
		a.searcher = sqlite{path: a.sqlitePath, embed: a.query}
		return
	}
	a.searcher = gmbd{url: a.gmbdURL}
}

// runSearch asks the chosen backend for the images matching the query
// within the scope, on a goroutine, replacing the results shown when it
// answers.
func (a *app) runSearch() {
	query := strings.TrimSpace(a.searchQuery)
	if query == "" {
		a.searchGen++ // an answer on its way no longer matters
		a.searchLoading = false
		a.searchErr = "Type what to look for first."
		a.searchResults = nil
		a.searchTotal = 0
		a.searchChosen = -1
		a.searchGrid = ui.GridState{}
		return
	}
	scope := strings.TrimSpace(a.searchScope)
	page := max(a.searchPage, 1)
	a.searchGen++
	gen := a.searchGen
	a.searchErr = ""
	a.searchLoading = true
	a.searchResults = nil
	a.searchTotal = 0
	a.searchChosen = -1
	a.searchGrid = ui.GridState{}
	a.async(func() {
		results, total, err := a.searcher.search(query, scope, page)
		a.post(func() {
			if gen != a.searchGen {
				return // another search ran meanwhile
			}
			a.searchLoading = false
			if err != nil {
				a.searchErr = err.Error()
				return
			}
			a.searchResults = results
			a.searchTotal = total
			if len(results) > a.searchPageSize {
				a.searchPageSize = len(results)
			}
		})
	})
}
