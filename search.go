package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
)

// searchPageSize is how many images a search page holds; the grid asks
// for the next page as it nears the end of the results it has.
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
// grid that loads more as it nears its end.
func (a *app) searchView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.searchHeader(c)
		ui.Divider(c)
		a.searchResultsView(c)
	})
	showResults := a.searchResults != nil && !a.searchLoading && a.searchErr == ""
	notice, tooltip, failed := a.removeNotice()
	if notice == "" {
		notice, tooltip, failed = a.indexNotice()
	}
	if showResults {
		showing := fmt.Sprintf("Showing %d/%d", len(a.searchResults), a.searchTotal)
		if notice == "" {
			notice, tooltip = showing, "Images shown and found"
		} else {
			notice += ". " + showing
		}
	}
	if notice != "" {
		color := t.TextMuted
		if failed {
			color = t.Danger
		}
		ui.Overlay(c, func() {
			ui.Badge(c, notice).Absolute().Bottom(0).Right(0).
				Padding(3, 8).Radius(8, 0, 0, 0).
				Background(t.Surface).TextColor(color).Tooltip(tooltip)
		})
	}
}

// searchHeader holds the search view's query, scope and the button that runs
// the search. Enter in either input submits it too.
func (a *app) searchHeader(c *ui.Context) {
	submit := func() {
		a.searchPage = 1
		a.runSearch()
	}
	ui.Row(c).FillWidth().Padding(14, 24).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.SearchField(c, &a.searchQuery).Label("Query").
			Placeholder("Words to look for").Grow(1).MaxWidth(480).
			OnSubmit(submit)
		a.scopeField(c, submit)
		a.groupSwitch(c)
		ui.PrimaryButton(c, "Submit").Tooltip("Run the search").OnClick(submit)
	})
}

// groupSwitch is the switch that groups the results by their directory,
// after the scope.
func (a *app) groupSwitch(c *ui.Context) {
	switchEl := ui.Switch(c, &a.searchGroup).Label("group by dir").
		Tooltip("Group the results by their directory")
	ui.Text(c, "group by dir").FontSize(12).TextColor(c.Theme().TextMuted).
		Cursor(ui.CursorPointer).AccessHidden(true).
		OnClick(func() {
			a.searchGroup = !a.searchGroup
			a.toggleSearchGroup()
		})
	if switchEl.Changed() {
		a.toggleSearchGroup()
	}
}

// scopeField is the scope input, with a clear icon inside it while it
// holds a folder. The icon is the one SearchField draws, a filled circle
// with a cross cut out of it, so that the two inputs' clear icons match.
// The scope starts empty, which searches every folder.
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

// searchResultsView shows what the search found: a message for a search on its
// way, one that failed or one that found nothing, and otherwise the images
// as the folders view does, asking for more as the grid nears its end, or
// as rows of one directory grouped when the switch is on.
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
	if a.searchGroup {
		a.searchGroupsView(c)
		return
	}
	gen := a.searchGen
	m := a.searchGridModel()
	a.searchPick.built = a.searchPick.built[:0]
	ui.Column(c).Fill().Children(func() {
		a.searchGrid.Key = func(i int) any {
			if i >= 0 && i < len(a.searchResults) {
				return a.searchResults[i].photo.path
			}
			return i
		}
		a.searchGrid.Selected = &a.searchChosen
		a.searchGrid.Selection = &a.searchPick.selection
		a.searchGrid.Label = func(i int) string {
			if i >= 0 && i < len(a.searchResults) {
				return a.searchResults[i].photo.name
			}
			return ""
		}
		grid := ui.GridView(c, &a.searchGrid, len(a.searchResults), a.cellWidth, a.cellHeight, func(i int) {
			a.searchCell(c, gen, m, i)
		}).Grow(1).Padding(12).TrackScroll(&a.searchScroll)
		grid.HandleInput(func(ev ui.InputEvent) bool { return a.gridInput(c, m, &a.searchGrid.Handle, ev) })
		grid.DrawOver(func(p *ui.Painter, r ui.Rect) { a.paintGridDrag(p, r, &a.searchPick) })
		if grid.Submitted() && a.searchChosen >= 0 && a.searchChosen < len(a.searchResults) {
			a.openSearchPhotos(c, a.searchResults[a.searchChosen].photo.path)
		}
	})
	a.loadMoreSearchNearEnd(c)
}

// loadMoreSearchNearEnd asks for the next page once the user scrolls the
// grid near its end, or once the page shown does not fill the grid at all,
// so that the results flow on as they go down. It waits for a page it adds
// to be laid out before considering the place again.
func (a *app) loadMoreSearchNearEnd(c *ui.Context) {
	n := len(a.searchResults)
	if a.searchLaid != n {
		a.searchLaid = n
		c.Invalidate()
		return
	}
	y := a.searchScroll.Y
	scrolled := y != a.searchAtY
	a.searchAtY = y
	if n >= a.searchTotal || a.searchedNoMore {
		return
	}
	if a.searchScroll.MaxY > 0 {
		// The grid scrolls: load only as the user nears its end.
		if !scrolled || y < a.searchScroll.MaxY-a.cellHeight {
			return
		}
	}
	a.loadMoreSearch()
}

// searchGridModel is the search results' grid as choosing items needs it.
func (a *app) searchGridModel() gridModel {
	return gridModel{
		pick:     &a.searchPick,
		selected: &a.searchChosen,
		count:    func() int { return len(a.searchResults) },
		key: func(i int) string {
			if i >= 0 && i < len(a.searchResults) && a.searchResults[i].photo != nil {
				return a.searchResults[i].photo.path
			}
			return ""
		},
		open: func(c *ui.Context, i int) {
			if i >= 0 && i < len(a.searchResults) && a.searchResults[i].photo != nil {
				a.openSearchPhotos(c, a.searchResults[i].photo.path)
			}
		},
	}
}

// searchCell shows one image the search found, with its score. Its place
// in the grid binds it, at the result it shows.
func (a *app) searchCell(c *ui.Context, gen uint64, m gridModel, i int) {
	a.searchResultCell(c, gen, m, i, i, 0, 0)
}

// searchResultCell shows result index as the cell at slot, sized to width
// and height when the grouped rows give them, else filling its grid cell.
func (a *app) searchResultCell(c *ui.Context, gen uint64, m gridModel, slot, index int, width, height float32) {
	if index < 0 || index >= len(a.searchResults) {
		return
	}
	result := a.searchResults[index]
	p := result.photo
	if p == nil {
		return
	}
	a.searchPick.built = append(a.searchPick.built, slot)
	t := c.Theme()
	pad, gap, iconSize, showName := a.cellMetrics()
	showMeta := a.cellHeight >= 64 && a.cellWidth >= 60
	cell := ui.Column(c).Fill().Padding(pad).Gap(gap)
	grouped := width > 0 && height > 0
	if grouped {
		cell = ui.Column(c).Size(width, height).Shrink(0).Padding(pad).Gap(gap).
			Radius(t.Radius).Border(2, ui.Color{})
		chosen, hovered := m.pick.selection.Has(p.path), cell.Hovered()
		switch {
		case chosen:
			cell.Background(t.Accent.Alpha(0.2)).Border(2, t.Accent)
			if hovered {
				cell.Background(t.Accent.Alpha(0.28))
			}
		case hovered:
			cell.Background(t.SurfacePressed)
		}
	}
	a.bindGridCell(c, m, cell, slot)
	cell.Children(func() {
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
	cell.ContextMenu(func(menu *ui.Menu) {
		if menu.Item("Open").Chosen() {
			a.openSearchPhotos(c, p.path)
		}
	})
}

// openSearchPhotos opens path, or the results chosen with it, in the
// single image view. Several chosen results each get a history tag, and
// the view steps through them alone; one opens and steps through all the
// results, as choosing it with Enter does.
func (a *app) openSearchPhotos(c *ui.Context, path string) {
	if a.searchPick.selection.Has(path) {
		photos := make([]*photo, 0, a.searchPick.selection.Len())
		at, clicked := -1, -1
		for _, result := range a.searchResults {
			p := result.photo
			if p == nil || !a.searchPick.selection.Has(p.path) {
				continue
			}
			if p.path == path {
				at = len(photos)
			}
			photos = append(photos, p)
		}
		if len(photos) > 1 && at >= 0 {
			for _, p := range photos {
				tag := a.ensureTag(p)
				if p.path == path {
					clicked = tag
				}
			}
			if clicked >= 0 {
				a.tag = clicked
			}
			a.clearSingle()
			a.setSingleIn(photos, at)
			c.Invalidate()
			return
		}
	}
	a.searchChosen = -1
	for i, result := range a.searchResults {
		if result.photo != nil && result.photo.path == path {
			a.searchChosen = i
			break
		}
	}
	a.openSearchResult(c)
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

// indexProgress says how far an index got: how many images it has handled,
// how many it has to handle, and how many of them it left out of the index.
type indexProgress struct {
	done, total, skipped int
}

// searcher is a search way: it finds the images matching a query within a
// scope, one page at a time, indexes the images at a path, those under a
// folder too when recursive, and removes a path from its index.
type searcher interface {
	search(query, scope string, page int) ([]searchResult, int, error)
	index(path string, recursive bool) (chan indexProgress, error)
	remove(path string) error
}

// setSearcher chooses the backend the search asks, from the configured way.
func (a *app) setSearcher(way searchWay) {
	a.searchScoreAsc = way == searchWaySQLite
	if way == searchWaySQLite {
		a.searcher = sqlite{path: a.sqlitePath, embed: a.query, images: a.images, onError: a.indexFailed}
		return
	}
	a.searcher = gmbd{url: a.gmbdURL}
}

// runSearch asks the chosen backend for the images matching the query
// within the scope, from the page a.searchPage gives, on a goroutine,
// replacing the results shown when it answers.
func (a *app) runSearch() {
	query := strings.TrimSpace(a.searchQuery)
	if query == "" {
		a.searchGen++ // an answer on its way no longer matters
		a.searchLoading = false
		a.searchingMore = false
		a.searchedNoMore = false
		a.searchErr = "Type what to look for first."
		a.searchResults = nil
		a.searchTotal = 0
		a.searchPage = 0
		a.searchChosen = -1
		a.searchScroll = ui.ScrollState{}
		a.searchAtY = 0
		a.searchLaid = -1
		a.searchGrid = ui.GridState{}
		a.searchPick = gridPick{pivot: -1}
		a.resetSearchGroups()
		return
	}
	scope := strings.TrimSpace(a.searchScope)
	page := max(a.searchPage, 1)
	a.searchGen++
	gen := a.searchGen
	a.searchErr = ""
	a.searchLoading = true
	a.searchingMore = false
	a.searchedNoMore = false
	a.searchResults = nil
	a.searchTotal = 0
	a.searchPage = page
	a.searchChosen = -1
	a.searchScroll = ui.ScrollState{}
	a.searchAtY = 0
	a.searchLaid = -1
	a.searchGrid = ui.GridState{}
	a.searchPick = gridPick{pivot: -1}
	a.resetSearchGroups()
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
			a.searchedNoMore = len(results) == 0 || len(results) >= total
			a.searchPick.handles = make([]ui.Handle, len(results))
			a.searchPick.built = a.searchPick.built[:0]
			if a.searchGroup {
				a.rebuildSearchGroups()
			}
		})
	})
}

// loadMoreSearch asks for the page after the results shown and adds it when
// it answers, so that the grid holds everything the search found.
func (a *app) loadMoreSearch() {
	query := strings.TrimSpace(a.searchQuery)
	if query == "" || a.searchLoading || a.searchingMore || a.searchedNoMore {
		return
	}
	if len(a.searchResults) >= a.searchTotal {
		a.searchedNoMore = true
		return
	}
	scope := strings.TrimSpace(a.searchScope)
	page := a.searchPage + 1
	gen := a.searchGen
	a.searchingMore = true
	a.async(func() {
		results, total, err := a.searcher.search(query, scope, page)
		log.Printf("loading page: %v", page)
		a.post(func() {
			if gen != a.searchGen {
				return // another search ran meanwhile
			}
			a.searchingMore = false
			if err != nil {
				log.Printf("loading more search results: %v", err)
				a.searchedNoMore = true
				return
			}
			a.searchPage = page
			if total > 0 {
				a.searchTotal = total
			}
			before := len(a.searchResults)
			a.searchResults = append(a.searchResults, results...)
			a.searchPick.handles = append(a.searchPick.handles, make([]ui.Handle, len(results))...)
			if a.searchGroup {
				a.rebuildSearchGroups()
			}
			if len(results) == 0 || len(a.searchResults) == before || len(a.searchResults) >= a.searchTotal {
				a.searchedNoMore = true
			}
		})
	})
}
