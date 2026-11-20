# glry

A read-only image browser built with [MyGo](https://github.com/egoist/mygo) native `ui`: a folder tree with a grid of thumbnails, a search view, and a single-image view with history tags.

## Files

- `main.go` — app state and startup: the rail and view switching, tree scanning, folder loading, thumbnails, and the window.
- `folders.go` — the folders view: the sidebar's outline rows and the selected folder's grid of folder and photo cells, with their selection and the image count tag.
- `single.go` — the single-image view: opening photos, zoom, pan, and the history tags with their kept views.
- `search.go` — the search view: query/scope/page header and the results grid.
- `search_gmbd.go` — the `gmbd` search way: the HTTP request to the gmbd service at `http://localhost:8500`.
- `search_sqlite.go` — the `sqlite` search way: the sqlite-vec database at `~/.glry/db.sqlite`, the nearest-image queries, and indexing folders or images into it.
- `embedding.go` — the embedding service: query text and base64 images in, vectors out.
- `index.go` — indexing progress and the `Index` context menu item of folders and images.
- `config.go` — `~/.glry/config.json`: cell-size and search settings, plus the config editor view.
- `files.go` — reading folders, listing images, decoding thumbnails, and applying EXIF orientation.
- `main_test.go` — tests for the views, folder scanning, thumbnails, search, and the single-image view.
- `index_test.go` — tests for the Index menu item of folders and images.
- `search_sqlite_test.go` — tests for SQLite search and indexing.
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
