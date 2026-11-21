package main

import "github.com/egoist/mygo/ui"

// gridDrag is a press in a grid, and the rectangle it drags to choose the
// items inside it.
type gridDrag struct {
	// press says the pointer went down in the grid; on says it moved far
	// enough for a drag to choose items rather than a click.
	press, on bool
	// index is the item pressed, or -1 for empty space.
	index int
	// from and at are the window's points the drag goes between.
	from, at ui.Point
	// add keeps the items chosen before the drag, as Cmd-drag does, in
	// base, and adds the rectangle's.
	add  bool
	base []string
	// mods and clicks are those of the press, for a click that follows.
	mods   ui.Modifiers
	clicks int
}

// gridPick is a grid's choice of several items: the paths chosen, the
// item a Shift-click extends from, the drag choosing a rectangle, and the
// places of the cells it built last, which the rectangle tests.
type gridPick struct {
	selection ui.Selection[string]
	pivot     int
	drag      gridDrag
	handles   []ui.Handle
	built     []int
}

// gridModel is a view's grid as choosing items needs it: its pick, the
// item it chooses, how its items are counted and told by path, and what a
// double click opens.
type gridModel struct {
	pick     *gridPick
	selected *int
	count    func() int
	key      func(i int) string
	open     func(c *ui.Context, i int)
}

// gridDragSlop is how far the pointer moves before a press in the grid
// becomes a drag choosing items.
const gridDragSlop = 4

// gridCellInput takes the cell of item i's pointer input, so that a drag
// chooses the items of a rectangle, and a click chooses what a click of
// its modifiers does.
func (a *app) gridCellInput(c *ui.Context, m gridModel, i int, h *ui.Handle, ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		a.beginGridDrag(m, i, h.Bounds(c), ev)
		return true
	case ui.InputPointerMove:
		if !m.pick.drag.press {
			return false
		}
		a.moveGridDrag(c, m, h.Bounds(c), ev)
		return true
	case ui.InputPointerUp:
		if !m.pick.drag.press {
			return false
		}
		a.endGridDrag(c, m, h.Bounds(c), ev)
		return true
	}
	return false
}

// gridInput takes the pointer input of the grid's empty space, where a
// drag chooses items too and a click chooses none.
func (a *app) gridInput(c *ui.Context, m gridModel, h *ui.Handle, ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		a.beginGridDrag(m, -1, h.Bounds(c), ev)
		return true
	case ui.InputPointerMove:
		if !m.pick.drag.press {
			return false
		}
		a.moveGridDrag(c, m, h.Bounds(c), ev)
		return true
	case ui.InputPointerUp:
		if !m.pick.drag.press {
			return false
		}
		a.endGridDrag(c, m, h.Bounds(c), ev)
		return true
	}
	return false
}

// beginGridDrag starts a press at item i, or on empty space with i -1,
// whose pointer is at the point of the press in the box r.
func (a *app) beginGridDrag(m gridModel, i int, r ui.Rect, ev ui.InputEvent) {
	d := gridDrag{
		press:  true,
		index:  i,
		from:   ui.Point{X: r.X + ev.X, Y: r.Y + ev.Y},
		mods:   ev.Mods,
		clicks: ev.Clicks,
		add:    ev.Mods&ui.Cmd != 0,
	}
	d.at = d.from
	if d.add {
		for path := range m.pick.selection.All() {
			d.base = append(d.base, path)
		}
	}
	m.pick.drag = d
}

// moveGridDrag follows the pointer at the point of a move in the box r,
// choosing the items of the rectangle once it has gone far enough.
func (a *app) moveGridDrag(c *ui.Context, m gridModel, r ui.Rect, ev ui.InputEvent) {
	d := &m.pick.drag
	d.at = ui.Point{X: r.X + ev.X, Y: r.Y + ev.Y}
	if !d.on {
		dx, dy := d.at.X-d.from.X, d.at.Y-d.from.Y
		if dx*dx+dy*dy < gridDragSlop*gridDragSlop {
			return
		}
		d.on = true
		switch {
		case d.index >= 0 && d.index < m.count():
			*m.selected = d.index
		case !d.add:
			*m.selected = -1
		}
	}
	a.applyGridDrag(c, m)
}

// endGridDrag ends the press at the point of the release in the box r: a
// drag leaves the items of its rectangle chosen, and a press that did not
// move is a click.
func (a *app) endGridDrag(c *ui.Context, m gridModel, r ui.Rect, ev ui.InputEvent) {
	d := m.pick.drag
	m.pick.drag = gridDrag{}
	if !d.press {
		return
	}
	if d.on {
		if !ev.Canceled {
			d.at = ui.Point{X: r.X + ev.X, Y: r.Y + ev.Y}
			m.pick.drag = d
			a.applyGridDrag(c, m)
		}
		m.pick.drag = gridDrag{}
		return
	}
	if ev.Canceled {
		return
	}
	if d.index < 0 {
		// A click on empty space clears the choice, unless a modifier adds
		// to it.
		if d.mods&(ui.Cmd|ui.Shift) == 0 {
			m.pick.selection.Clear()
			*m.selected, m.pick.pivot = -1, -1
		}
		return
	}
	a.clickGridItem(c, m, d.index, d.mods, d.clicks)
}

// applyGridDrag chooses the items of the drag's rectangle, on top of those
// chosen before it when the press held Cmd.
func (a *app) applyGridDrag(c *ui.Context, m gridModel) {
	p := m.pick
	d := &p.drag
	p.selection.Clear()
	if d.add {
		for _, path := range d.base {
			p.selection.Add(path)
		}
	}
	r := dragRect(d.from, d.at)
	for _, i := range p.built {
		if i < 0 || i >= m.count() || i >= len(p.handles) {
			continue
		}
		if b := p.handles[i].Bounds(c); b.W > 0 && b.H > 0 && rectsOverlap(r, b) {
			p.selection.Add(m.key(i))
		}
	}
}

// clickGridItem chooses the item i of a grid as a click with mods does,
// and opens it when the click was the second of a double click.
func (a *app) clickGridItem(c *ui.Context, m gridModel, i int, mods ui.Modifiers, clicks int) {
	if i < 0 || i >= m.count() {
		return
	}
	p := m.pick
	switch {
	case mods&ui.Shift != 0 && p.pivot >= 0 && p.pivot < m.count():
		p.selection.Clear()
		for j := min(p.pivot, i); j <= max(p.pivot, i); j++ {
			p.selection.Add(m.key(j))
		}
		*m.selected = i
	case mods&ui.Cmd != 0:
		key := m.key(i)
		if p.selection.Has(key) {
			p.selection.Remove(key)
		} else {
			p.selection.Add(key)
		}
		*m.selected, p.pivot = i, i
	default:
		p.selection.Clear()
		p.selection.Add(m.key(i))
		*m.selected, p.pivot = i, i
	}
	if clicks >= 2 && m.open != nil {
		m.open(c, i)
	}
}

// paintGridDrag paints the rectangle of pick's drag choosing items,
// clipped to the grid's box.
func (a *app) paintGridDrag(p *ui.Painter, r ui.Rect, pick *gridPick) {
	if !pick.drag.on {
		return
	}
	rect := intersectRect(dragRect(pick.drag.from, pick.drag.at), r)
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	t := p.Theme()
	p.Fill(rect, t.Accent.Alpha(0.15), 0)
	p.Stroke(rect, t.Accent, 0, 1)
}

// dragRect returns the rectangle between two points, whichever way the
// drag goes.
func dragRect(a, b ui.Point) ui.Rect {
	return ui.Rect{
		X: min(a.X, b.X),
		Y: min(a.Y, b.Y),
		W: max(a.X, b.X) - min(a.X, b.X),
		H: max(a.Y, b.Y) - min(a.Y, b.Y),
	}
}

// rectsOverlap reports whether two rectangles share any area.
func rectsOverlap(a, b ui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// intersectRect returns the area two rectangles share, empty where they
// share none.
func intersectRect(a, b ui.Rect) ui.Rect {
	x0, y0 := max(a.X, b.X), max(a.Y, b.Y)
	x1, y1 := min(a.X+a.W, b.X+b.W), min(a.Y+a.H, b.Y+b.H)
	if x1 <= x0 || y1 <= y0 {
		return ui.Rect{}
	}
	return ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}
