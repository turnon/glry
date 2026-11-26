# glry

A read-only image browser built with [MyGo](https://github.com/egoist/mygo) native `ui`: a folder tree with a grid of thumbnails, a search view, and a single-image view with history tags.

## Files

- `main.go` — app state and startup: the rail and view switching, tree scanning, folder loading, thumbnails, and the window.
- `folders.go` — the folders view: the sidebar's outline rows and the selected folder's grid of folder and photo cells, with their selection, the image count tag, and an image's Search, Index and Remove menu items.
- `select.go` — choosing several items in the folders and search grids: Cmd-click, Shift-click, and dragging a rectangle over them.
- `single.go` — the single-image view: opening photos, zoom, pan, the history tags with their kept views, and the image's Search menu item.
- `search.go` — the search view: query/scope header with the picture icon that picks a local image as the query, the results grid that loads more as it nears its end, its images' Open and Search menu items, and opening its chosen images.
- `search_group.go` — the search view's grouped results: a row of one directory's images, ordered by score, that loads a batch as it scrolls sideways.
- `search_gmbd.go` — the `gmbd` search way: the HTTP request to the gmbd service at `http://localhost:8500`; it searches text only, so image queries report why.
- `search_sqlite.go` — the `sqlite` search way: the sqlite-vec database at `~/.glry/db.sqlite`, the nearest-image queries, embedding a query image when the query is a local image, and indexing folders or images into it or removing them from it.
- `embedding.go` — the embedding service: query text and base64 images in, vectors out.
- `index.go` — indexing progress and the `Index` and `Index Recursively` context menu items of folders and images.
- `remove.go` — the `Remove` context menu item: moving folders and images to the trash, taking them out of the index, and refreshing the folders view.
- `config.go` — `~/.glry/config.json`: cell-size, search, and embedding settings, plus the config view's form.
- `files.go` — reading folders, listing images, decoding thumbnails, and applying EXIF orientation.
- `main_test.go` — tests for the views, folder scanning, thumbnails, search, and the single-image view.
- `index_test.go` — tests for the Index menu items of folders and images.
- `remove_test.go` — tests for the Remove menu items of folders and images.
- `select_test.go` — tests for choosing several items in the folders and search grids, and opening or indexing them.
- `search_sqlite_test.go` — tests for SQLite search and indexing.
- `search_group_test.go` — tests for grouping the search results by directory, choosing several of them, and loading their rows sideways.
- `search_gmbd_test.go` — tests for the gmbd search way.
- `config_test.go` — tests for config parsing, defaults, and saving.

## Run

```sh
go tool mygo dev        # builds and launches glry Dev.app
go run . [folder]       # one-off run, starting at folder or the default one
go tool mygo build      # writes to ./build
```

## Test

```sh
CGO_ENABLED=0 go test ./...
go tool mygo vet .
```
