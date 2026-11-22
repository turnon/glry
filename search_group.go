package main

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/egoist/mygo/ui"
)

// searchGroupPad is the room the grouped results leave around their rows.
const searchGroupPad = 12

// The grouped rows leave room for their scroll bar below the images: its
// track, and a gap clear of the images.
const (
	searchGroupBarWidth = 8
	searchGroupBarGap   = 12
)

// searchDirRow is a directory's row in the grouped search: where it
// scrolls sideways, how many batches of images it shows, and the offset
// it last considered for loading the next batch.
type searchDirRow struct {
	scroll  ui.ScrollState
	batches int
	atX     float32
}

// searchGroup is one directory's images among the search results, most
// matched first. start is where its images begin in the flat order the
// grouped grid chooses them in.
type searchGroup struct {
	dir     string
	abs     string
	results []int
	score   float64
	start   int
	row     *searchDirRow
}

// searchGroupFit is how many images fit across a row, from the width the
// list had in the last frame. It is 0 before the list has a width.
func (a *app) searchGroupFit(c *ui.Context) int {
	w := a.searchGroupList.Bounds(c).W
	if w <= 0 || a.cellWidth <= 0 {
		return 0
	}
	w -= 2 * searchGroupPad
	gap := c.Theme().Space(2)
	return max(1, int((w+gap)/(a.cellWidth+gap)))
}

// searchGroupLabel names a directory in the grouped results: its path
// under the root, or its absolute path when it lies elsewhere.
func (a *app) searchGroupLabel(dir string) string {
	if underRoot(a.root, dir) {
		if rel, err := filepath.Rel(a.root, dir); err == nil {
			if rel == "." {
				return rootName(a.root)
			}
			return filepath.Join(rootName(a.root), rel)
		}
	}
	return dir
}

// resetSearchGroups forgets the grouped results, for a search that starts
// again.
func (a *app) resetSearchGroups() {
	a.searchGroups = nil
	a.searchGroupFlat = nil
	a.searchGroupChosen = -1
	a.searchGroupRows = map[string]*searchDirRow{}
	a.searchGroupList = ui.ListState{}
}

// toggleSearchGroup switches the results between the grid and the rows
// grouped by directory, keeping the images chosen.
func (a *app) toggleSearchGroup() {
	a.resetSearchGroups()
	a.searchPick.pivot = -1
	a.searchPick.handles = make([]ui.Handle, len(a.searchResults))
	a.searchPick.built = a.searchPick.built[:0]
	if a.searchGroup {
		a.rebuildSearchGroups()
	}
}

// rebuildSearchGroups groups the results loaded so far by their
// directory: each group's images most matched first, and the groups by
// the score of their first image, ties by absolute directory path. It
// keeps each directory's row, so its sideways place survives a page.
func (a *app) rebuildSearchGroups() {
	chosen := ""
	if i := a.searchGroupChosen; i >= 0 && i < len(a.searchGroupFlat) {
		if result, ok := a.searchGroupResult(i); ok && result.photo != nil {
			chosen = result.photo.path
		}
	}
	byDir := make(map[string]*searchGroup)
	groups := make([]*searchGroup, 0, len(a.searchResults))
	for i, result := range a.searchResults {
		if result.photo == nil {
			continue
		}
		dir := filepath.Dir(result.photo.path)
		g := byDir[dir]
		if g == nil {
			g = &searchGroup{dir: dir, abs: absSearchDir(dir)}
			byDir[dir] = g
			groups = append(groups, g)
		}
		g.results = append(g.results, i)
	}
	for _, g := range groups {
		sort.SliceStable(g.results, func(i, j int) bool {
			return a.searchResultBefore(a.searchResults[g.results[i]], a.searchResults[g.results[j]])
		})
		g.score = a.searchResults[g.results[0]].score
		row := a.searchGroupRows[g.dir]
		if row == nil {
			row = &searchDirRow{}
			a.searchGroupRows[g.dir] = row
		}
		g.row = row
	}
	sort.SliceStable(groups, func(i, j int) bool {
		x, y := groups[i], groups[j]
		if x.score != y.score {
			if a.searchScoreAsc {
				return x.score < y.score
			}
			return x.score > y.score
		}
		return x.abs < y.abs
	})
	// Forget the rows of directories the results no longer hold.
	for dir := range a.searchGroupRows {
		if byDir[dir] == nil {
			delete(a.searchGroupRows, dir)
		}
	}
	a.searchGroups = a.searchGroups[:0]
	a.searchGroupFlat = a.searchGroupFlat[:0]
	for _, g := range groups {
		g.start = len(a.searchGroupFlat)
		a.searchGroups = append(a.searchGroups, *g)
		for _, i := range g.results {
			a.searchGroupFlat = append(a.searchGroupFlat, i)
		}
	}
	a.searchGroupChosen = -1
	if chosen != "" {
		for i := range a.searchGroupFlat {
			result, ok := a.searchGroupResult(i)
			if ok && result.photo != nil && result.photo.path == chosen {
				a.searchGroupChosen = i
				break
			}
		}
	}
	if len(a.searchPick.handles) < len(a.searchGroupFlat) {
		a.searchPick.handles = make([]ui.Handle, len(a.searchResults))
	}
}

// searchResultBefore reports whether x ranks before y: the higher score
// first, or the lower one for a nearest-first search, ties by path.
func (a *app) searchResultBefore(x, y searchResult) bool {
	if x.score != y.score {
		if a.searchScoreAsc {
			return x.score < y.score
		}
		return x.score > y.score
	}
	if x.photo == nil || y.photo == nil {
		return y.photo == nil && x.photo != nil
	}
	return x.photo.path < y.photo.path
}

// absSearchDir is the absolute form of a directory, for ordering groups.
func absSearchDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// searchGroupResult returns the result at a place in the grouped order.
func (a *app) searchGroupResult(i int) (searchResult, bool) {
	if i < 0 || i >= len(a.searchGroupFlat) {
		return searchResult{}, false
	}
	j := a.searchGroupFlat[i]
	if j < 0 || j >= len(a.searchResults) {
		return searchResult{}, false
	}
	return a.searchResults[j], true
}

// searchGroupGridModel is the grouped results as choosing images needs
// them, in the order the rows and their images show.
func (a *app) searchGroupGridModel() gridModel {
	return gridModel{
		pick:     &a.searchPick,
		selected: &a.searchGroupChosen,
		count:    func() int { return len(a.searchGroupFlat) },
		key: func(i int) string {
			if result, ok := a.searchGroupResult(i); ok && result.photo != nil {
				return result.photo.path
			}
			return ""
		},
		open: func(c *ui.Context, i int) {
			if result, ok := a.searchGroupResult(i); ok && result.photo != nil {
				a.searchGroupChosen = i
				a.openSearchPhotos(c, result.photo.path)
			}
		},
	}
}

// searchGroupsView shows the results grouped by directory. A row holds
// one directory's images, most matched first, and scrolls sideways: it
// builds enough images to fill it and one more, and loads the next batch
// as the row scrolls to its end.
func (a *app) searchGroupsView(c *ui.Context) {
	if len(a.searchResults) == 0 {
		return
	}
	if a.searchGroups == nil {
		a.rebuildSearchGroups()
	}
	fit := a.searchGroupFit(c)
	fitKnown := fit > 0
	if !fitKnown {
		fit = 1
		c.Invalidate() // the list has no width to count a batch with yet
	}
	m := a.searchGroupGridModel()
	a.searchPick.built = a.searchPick.built[:0]
	more := false
	var built []*searchGroup
	a.searchGroupList.Key = func(i int) any {
		if i >= 0 && i < len(a.searchGroups) {
			return a.searchGroups[i].dir
		}
		return i
	}
	a.searchGroupList.Label = func(i int) string {
		if i >= 0 && i < len(a.searchGroups) {
			return rootName(a.searchGroups[i].dir)
		}
		return ""
	}
	a.searchGroupList.EndReached = func(int) { a.loadMoreSearch() }
	list := ui.List(c, &a.searchGroupList, len(a.searchGroups), func(i int) {
		if i < 0 || i >= len(a.searchGroups) {
			return
		}
		g := &a.searchGroups[i]
		built = append(built, g)
		if a.groupRowWantsMore(g, fit) {
			more = true
		}
		a.searchGroupRow(c, fit, m, g)
	}).Grow(1).Padding(searchGroupPad)
	list.DrawOver(func(p *ui.Painter, r ui.Rect) { a.paintGridDrag(p, r, &a.searchPick) })
	if more || (fitKnown && a.searchGroupsNeedMore(fit, built)) {
		a.loadMoreSearch()
	}
}

// groupRowWantsMore reveals a row's next batch once the user scrolls it
// to its end, and reports whether that batch must ask for more results.
func (a *app) groupRowWantsMore(g *searchGroup, fit int) bool {
	row := g.row
	if row == nil {
		return false
	}
	if row.batches < 1 {
		row.batches = 1
	}
	if row.scroll.MaxX <= 0 {
		row.atX = row.scroll.X
		return false
	}
	if row.scroll.X == row.atX {
		return false
	}
	row.atX = row.scroll.X
	if row.scroll.X < row.scroll.MaxX-a.cellWidth/2 {
		return false
	}
	shown := min(len(g.results), row.batches*fit+1)
	row.batches++
	return shown >= len(g.results)
}

// searchGroupsNeedMore asks for another page when a row in view has too
// few images to fill its batch, or when the rows in view show their ends
// and leave room below them, so that results in directories not loaded yet
// do not wait for a scroll they cannot make.
func (a *app) searchGroupsNeedMore(fit int, groups []*searchGroup) bool {
	if a.searchLoading || a.searchingMore || a.searchedNoMore ||
		len(a.searchResults) >= a.searchTotal {
		return false
	}
	for _, g := range groups {
		row := g.row
		if row != nil && row.batches*fit+1 > len(g.results) {
			return true // not enough results yet to fill this row's batch
		}
	}
	if !a.searchGroupList.AtEnd() {
		return false
	}
	for _, g := range groups {
		row := g.row
		if row == nil {
			continue
		}
		if row.batches*fit+1 < len(g.results) {
			return false // this row still has loaded images to show
		}
		if row.scroll.MaxX > 0 && row.scroll.X < row.scroll.MaxX-1 {
			return false // the user can scroll this row toward more
		}
	}
	return true
}

// showSearchGroupDir jumps from a grouped row's directory to the folders
// view showing it.
func (a *app) showSearchGroupDir(c *ui.Context, dir string) {
	a.showFolders()
	a.selectDir(dir)
	c.Invalidate()
}

// searchGroupRow shows a directory in the grouped results: its name, the
// images loaded for it in a row that scrolls sideways, and the batch the
// row shows so far.
func (a *app) searchGroupRow(c *ui.Context, fit int, m gridModel, g *searchGroup) {
	t := c.Theme()
	row := g.row
	if row == nil {
		return
	}
	gen := a.searchGen
	limit := min(len(g.results), row.batches*fit+1)
	gap := t.Space(2)
	rowEl := ui.Column(c).FillWidth().Gap(4).
		Padding(t.Space(2), 0, 0, 0).Margin(0, 0, t.Space(3), 0)
	hovered := rowEl.Hovered()
	if hovered {
		rowEl.Background(t.SurfaceHover).Radius(t.Radius)
	}
	rowEl.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).PaddingX(t.Space(2)).Children(func() {
			name := a.searchGroupLabel(g.dir)
			button := ui.ButtonBase(c).Label(name).Tooltip(g.dir).Cursor(ui.CursorPointer).
				OnClick(func() { a.showSearchGroupDir(c, g.dir) })
			button.Children(func() {
				label := ui.Text(c, name).FontSize(12).SingleLine()
				if button.Hovered() {
					label.TextColor(t.Accent)
				} else {
					label.TextColor(t.TextMuted)
				}
			})
			ui.Text(c, fmt.Sprintf("%d/%d", limit, len(g.results))).
				FontSize(10).TextColor(t.TextMuted).
				Tooltip("Images shown of those loaded for this directory")
		})
		ui.ScrollHorizontal(c).FillWidth().Height(a.cellHeight+searchGroupBarWidth+searchGroupBarGap).
			TrackScroll(&row.scroll).
			Scrollbars(hovered).KeepScrollbars(hovered).
			ScrollbarAxes(true, false).
			ScrollbarStyle(ui.ScrollbarStyle{
				TrackWidth: searchGroupBarWidth,
				ThumbWidth: 4,
				MinLength:  24,
			}).
			Children(func() {
				ui.Row(c).Gap(gap).AlignItems(ui.Stretch).Children(func() {
					for k := 0; k < limit; k++ {
						a.searchResultCell(c, gen, m, g.start+k, g.results[k], a.cellWidth, a.cellHeight)
					}
				})
			})
	})
}
