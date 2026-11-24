package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// scoredSearcher serves pages of results as given, and records the pages
// asked.
type scoredSearcher struct {
	pages [][]searchResult
	total int
	asked []int
}

func (s *scoredSearcher) search(query, scope string, page int) ([]searchResult, int, error) {
	s.asked = append(s.asked, page)
	if page < 1 || page > len(s.pages) {
		return nil, s.total, nil
	}
	return s.pages[page-1], s.total, nil
}

func (s *scoredSearcher) index(path string, recursive bool) (chan indexProgress, error) {
	return nil, nil
}

// result is an image of dir with a score, as a search result.
func result(dir, name string, score float64) searchResult {
	return searchResult{
		photo: &photo{path: filepath.Join(dir, name), name: name},
		score: score,
	}
}

func TestSearchViewGroupsByDir(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	gamma := filepath.Join(root, "gamma")
	for _, dir := range []string{alpha, beta, gamma} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(beta, "b1.png"), filepath.Join(beta, "b2.png"),
		filepath.Join(alpha, "a1.png"), filepath.Join(alpha, "a2.png"),
		filepath.Join(gamma, "g1.png"),
	} {
		writePNG(t, path, color.RGBA{A: 255})
	}
	searcher := &scoredSearcher{
		pages: [][]searchResult{{
			result(beta, "b1.png", 0.9),
			result(gamma, "g1.png", 0.8),
			result(alpha, "a1.png", 0.8),
			result(beta, "b2.png", 0.7),
			result(alpha, "a2.png", 0.8),
		}},
		total: 5,
	}
	a := syncApp(root)
	a.searcher = searcher
	a.searchQuery = "pictures"
	a.mode = viewSearch
	a.runSearch()
	tt := ui.NewTester(a.view, 1000, 640)
	tt.Frame()

	if a.searchGroups != nil {
		t.Fatalf("the results grouped before the switch: %+v", a.searchGroups)
	}
	if _, ok := tt.AccessByRole(ui.RoleSwitch, "group by dir"); !ok {
		t.Fatalf("the switch is not named group by dir: %v", tt.AccessAll())
	}
	if err := tt.Click("group by dir"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		tt.Frame()
	}
	if !a.searchGroup {
		t.Fatal("clicking the switch did not group the results")
	}

	if !tt.HasText("group by dir") {
		t.Fatalf("the header does not show the switch's label: %q", tt.Texts())
	}
	if !tt.HasText("2/2") {
		t.Errorf("the rows do not show how many of their images are loaded: %q", tt.Texts())
	}
	want := []string{beta, alpha, gamma}
	if len(a.searchGroups) != len(want) {
		t.Fatalf("the results grouped into %d rows, want %d: %+v", len(a.searchGroups), len(want), a.searchGroups)
	}
	for i, dir := range want {
		if got := a.searchGroups[i].dir; got != dir {
			t.Errorf("row %d holds %q, want %q", i, got, dir)
		}
	}
	wantAlpha := []string{filepath.Join(alpha, "a1.png"), filepath.Join(alpha, "a2.png")}
	var gotAlpha []string
	for _, i := range a.searchGroups[1].results {
		gotAlpha = append(gotAlpha, a.searchResults[i].photo.path)
	}
	if !slices.Equal(gotAlpha, wantAlpha) {
		t.Errorf("alpha's images are %v, want %v, most matched first", gotAlpha, wantAlpha)
	}

	// The rows show top to bottom in their order, and one row's images
	// left to right.
	betaLabel := a.searchGroupLabel(beta)
	alphaLabel := a.searchGroupLabel(alpha)
	gammaLabel := a.searchGroupLabel(gamma)
	betaAt, ok := tt.Find(betaLabel)
	if !ok {
		t.Fatalf("the first row does not show %q: %q", betaLabel, tt.Texts())
	}
	alphaAt, ok := tt.Find(alphaLabel)
	if !ok {
		t.Fatalf("the second row does not show %q: %q", alphaLabel, tt.Texts())
	}
	gammaAt, ok := tt.Find(gammaLabel)
	if !ok {
		t.Fatalf("the third row does not show %q: %q", gammaLabel, tt.Texts())
	}
	if !(betaAt.Y < alphaAt.Y && alphaAt.Y < gammaAt.Y) {
		t.Errorf("the rows show at y %v, %v and %v, want beta, alpha, gamma",
			betaAt.Y, alphaAt.Y, gammaAt.Y)
	}
	a1, ok := tt.Find("a1.png")
	if !ok {
		t.Fatalf("the alpha row does not show a1.png: %q", tt.Texts())
	}
	a2, ok := tt.Find("a2.png")
	if !ok {
		t.Fatalf("the alpha row does not show a2.png: %q", tt.Texts())
	}
	if a1.X >= a2.X {
		t.Errorf("a1.png shows at x %v and a2.png at %v, want a1.png to the left", a1.X, a2.X)
	}
}

func TestSearchViewGroupsLoadBatchesSideways(t *testing.T) {
	root := t.TempDir()
	shots := filepath.Join(root, "shots")
	if err := os.Mkdir(shots, 0o755); err != nil {
		t.Fatal(err)
	}
	const total = 2 * searchPageSize
	var pages [][]searchResult
	for i := 0; i < total; i++ {
		path := filepath.Join(shots, fmt.Sprintf("img%02d.png", i))
		writePNG(t, path, color.RGBA{A: 255})
		if i%searchPageSize == 0 {
			pages = append(pages, nil)
		}
		pages[len(pages)-1] = append(pages[len(pages)-1],
			result(shots, fmt.Sprintf("img%02d.png", i), 1-float64(i)/total))
	}
	searcher := &scoredSearcher{pages: pages, total: total}
	a := syncApp(root)
	a.setCellSize(cellSize{width: 100, height: 100})
	a.searcher = searcher
	a.searchQuery = "pictures"
	a.mode = viewSearch
	a.searchGroup = true
	a.runSearch()
	tt := ui.NewTester(a.view, 1000, 640)
	for range 3 {
		tt.Frame()
	}
	if len(a.searchGroups) != 1 || a.searchGroups[0].dir != shots {
		t.Fatalf("the results grouped into %+v, want one row for %q", a.searchGroups, shots)
	}
	row := a.searchGroups[0].row
	if row.batches != 1 {
		t.Fatalf("the row shows %d batches at first, want one", row.batches)
	}
	if n := len(a.searchPick.built); n >= searchPageSize {
		t.Fatalf("the row built %d cells, want only the batch that fits", n)
	}
	if !slices.Equal(searcher.asked, []int{1}) {
		t.Fatalf("the search asked for pages %v, want only the first", searcher.asked)
	}

	// Scrolling the row to its right end shows its next batch, and once
	// the loaded images run out, asks for the page holding more.
	for range 6 {
		if row.scroll.MaxX <= 0 {
			tt.Frame()
			continue
		}
		row.scroll.X = row.scroll.MaxX
		tt.Frame()
	}
	if row.batches < 2 {
		t.Errorf("the row still shows %d batches after scrolling right, want more", row.batches)
	}
	if !slices.Contains(searcher.asked, 2) {
		t.Errorf("the row did not load the next page at its end: asked %v", searcher.asked)
	}
	if len(a.searchResults) != total {
		t.Errorf("the search loaded %d results, want %d", len(a.searchResults), total)
	}
}

func TestSearchViewGroupsFillARow(t *testing.T) {
	root := t.TempDir()
	shots := filepath.Join(root, "shots")
	if err := os.Mkdir(shots, 0o755); err != nil {
		t.Fatal(err)
	}
	const total = 25
	var pages [][]searchResult
	for i := 0; i < total; i++ {
		if i%5 == 0 {
			pages = append(pages, nil)
		}
		path := filepath.Join(shots, fmt.Sprintf("img%02d.png", i))
		writePNG(t, path, color.RGBA{A: 255})
		pages[len(pages)-1] = append(pages[len(pages)-1],
			result(shots, fmt.Sprintf("img%02d.png", i), 1-float64(i)/total))
	}
	searcher := &scoredSearcher{pages: pages, total: total}
	a := syncApp(root)
	a.setCellSize(cellSize{width: 100, height: 100})
	a.searcher = searcher
	a.searchQuery = "pictures"
	a.mode = viewSearch
	a.searchGroup = true
	a.runSearch()
	tt := ui.NewTester(a.view, 1000, 640)
	for range 5 {
		tt.Frame()
		if slices.Contains(searcher.asked, 2) {
			break
		}
	}
	if !slices.Contains(searcher.asked, 2) {
		t.Fatalf("a row with room for more images asked only for pages %v", searcher.asked)
	}
	if len(a.searchResults) < 10 {
		t.Errorf("the row loaded %d results, want enough to fill it", len(a.searchResults))
	}
}

func TestSearchGroupsRankNearestFirstForDistance(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	a := syncApp(root)
	a.searchScoreAsc = true
	a.searchGroup = true
	a.searchResults = []searchResult{
		result(alpha, "far.png", 0.2),
		result(beta, "near.png", 0.1),
	}
	a.rebuildSearchGroups()
	if len(a.searchGroups) != 2 || a.searchGroups[0].dir != beta || a.searchGroups[1].dir != alpha {
		t.Fatalf("the rows are %+v, want the nearer %q before %q", a.searchGroups, beta, alpha)
	}
}

// groupedSearchItems makes a grouped search view showing the images named,
// in two directories.
func groupedSearchItems(t *testing.T, names ...string) (*app, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	results := make([]searchResult, len(names))
	for i, name := range names {
		dir := filepath.Join(root, fmt.Sprintf("dir%d", i%2))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		writePNG(t, path, indexColor(i+1))
		results[i] = searchResult{
			photo: &photo{path: path, name: name},
			score: float64(len(names)-i) / 10,
		}
	}
	a := syncApp(root)
	a.mode = viewSearch
	a.searchResults = results
	a.searchTotal = len(results)
	a.searchPick = gridPick{pivot: -1, handles: make([]ui.Handle, len(results))}
	a.searchGroup = true
	a.rebuildSearchGroups()
	tt := ui.NewTester(a.view, 1000, 640)
	for range 3 {
		tt.Frame()
	}
	return a, tt
}

func TestGroupedSearchCmdClickChoosesSeveral(t *testing.T) {
	a, tt := groupedSearchItems(t, "a1.png", "b1.png", "a2.png", "b2.png")
	for _, name := range []string{"a1.png", "b2.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if a.searchPick.selection.Len() != 2 {
		t.Fatalf("chose %d grouped images, want 2", a.searchPick.selection.Len())
	}
	for _, name := range []string{"a1.png", "b2.png"} {
		path := filepath.Join(filepath.Dir(a.searchResults[0].photo.path), name)
		found := false
		for _, result := range a.searchResults {
			if filepath.Base(result.photo.path) == name {
				path = result.photo.path
				found = true
				break
			}
		}
		if !found || !a.searchPick.selection.Has(path) {
			t.Errorf("%q is not chosen: %+v", name, a.searchPick.selection)
		}
	}
	chosen, ok := a.searchGroupResult(a.searchGroupChosen)
	if !ok || chosen.photo == nil || chosen.photo.path != a.searchResults[3].photo.path {
		t.Errorf("the grouped image last chosen is %d, want b2.png", a.searchGroupChosen)
	}
}

func TestGroupedSearchShiftClickChoosesTheRange(t *testing.T) {
	a, tt := groupedSearchItems(t, "a1.png", "b1.png", "a2.png", "b2.png")
	if err := tt.Click("a1.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Shift, "b1.png"); err != nil {
		t.Fatal(err)
	}
	if a.searchPick.selection.Len() != 3 {
		t.Fatalf("chose %d grouped images, want the three from a1.png through b1.png", a.searchPick.selection.Len())
	}
	for _, name := range []string{"a1.png", "a2.png", "b1.png"} {
		found := false
		for _, result := range a.searchResults {
			if filepath.Base(result.photo.path) == name {
				if !a.searchPick.selection.Has(result.photo.path) {
					t.Errorf("%q is not chosen", name)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q is not a result", name)
		}
	}
	if a.searchPick.selection.Has(a.searchResults[3].photo.path) {
		t.Error("b2.png is chosen outside the range")
	}
}

func TestGroupedSearchDragChoosesTheImagesInItsRectangle(t *testing.T) {
	a, tt := groupedSearchItems(t, "a1.png", "b1.png", "a2.png", "b2.png")
	x, y := labelCenter(t, tt, "a1.png")
	toX, toY := labelCenter(t, tt, "b2.png")
	tt.Press(x, y)
	tt.Move(toX, toY)
	tt.Release(toX, toY)
	if a.searchPick.selection.Len() != 4 {
		t.Fatalf("the drag chose %d grouped images, want 4: %+v", a.searchPick.selection.Len(), a.searchPick.selection)
	}
}

func TestGroupedSearchOpenOpensTheChosenImages(t *testing.T) {
	a, tt := groupedSearchItems(t, "a1.png", "b1.png", "a2.png", "b2.png")
	for _, name := range []string{"a1.png", "b2.png"} {
		if err := tt.ClickWith(ui.Cmd, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.RightClick("a1.png"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open"); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewSingle || len(a.singlePhotos) != 2 {
		t.Fatalf("Open shows %d grouped images in view %d, want the two chosen", len(a.singlePhotos), a.mode)
	}
	if p := a.singlePhoto(); p == nil || p.name != "a1.png" {
		t.Errorf("Open starts at %v, want a1.png", p)
	}
}

func TestGroupedSearchDirJumpsToFoldersView(t *testing.T) {
	a, tt := groupedSearchItems(t, "a1.png", "b1.png", "a2.png", "b2.png")
	dir := a.searchGroups[0].dir
	if err := tt.Click(a.searchGroupLabel(dir)); err != nil {
		t.Fatal(err)
	}
	if a.mode != viewFolders {
		t.Fatalf("clicking the directory left view %d showing, want the folders view", a.mode)
	}
	if a.selected != dir {
		t.Errorf("the folders view shows %q, want %q", a.selected, dir)
	}
	if len(a.photos) != 2 {
		t.Errorf("the folders view shows %d images, want the directory's 2", len(a.photos))
	}
}
