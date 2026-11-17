# glry

A read-only image browser built with [MyGo](https://github.com/egoist/mygo) native `ui`: a folder tree, a thumbnail gallery, and a single-image view with history tags. Supports PNG, JPEG, GIF, WebP, BMP and SVG (hidden files skipped, JPEG EXIF orientation applied).

## Requirements

- Go 1.27.3+, no cgo.
- MyGo is pinned as a tool in `go.mod`, so use `go tool mygo`.

## Develop

```sh
go tool mygo dev        # builds and launches glry Dev.app, live reloads
go run . [folder]       # one-off run; the argument overrides the start folder
```

`defaultRoot()` in `main.go` picks the CLI folder, then `~/Pictures`, `~/Photos`, the home directory, or the working directory. The sidebar's **Open** button changes roots at runtime.

## Build

```sh
go tool mygo build      # writes to ./build, per mygo.json
```

Edit `mygo.json` for name, identifier, version and output; `resources/icon.png` is the app icon. See MyGo's docs for signing, notarization and packaging.

## Test

```sh
CGO_ENABLED=0 go test ./...
go test -race ./...     # when touching async work or caches
go vet ./...
go tool mygo vet .
```

UI tests are headless through `ui.NewTester`; `syncApp` in `main_test.go` makes async work inline so tests can wait for results.

## Configuration

`~/.glry.json` sets the gallery cell size (default `176px` square), also editable in the **Config** view:

```json
{
  "cell": {
    "width": "176px",
    "height": "176px"
  }
}
```

Sizes accept `176px` or `176`, from 1 to 4096. Invalid values fall back to defaults; **Save** validates and applies before writing. The file is read once at startup.

## Notes for maintainers

- All `app` state is main-thread only. Slow work runs through `a.async` and posts results back with `a.post`, which needs `a.win`.
- `galleryGen` and `singleGen` reject stale async results; bump them whenever the source changes. `thumbSlots` limits parallel decodes and `thumbCacheLimit` bounds the thumbnail cache.
- Thumbnails are requested only by cells the grid builds, and only once per photo (`thumbState`).
- `history` tags outlive folder changes (`pendingTag` waits for a load); `showInTag` renames the current tag for arrow navigation while `showPhotoAt` adds or finds one.
- Store app data and handles, never `ui.Element`/`ui.Context`. Follow `.agents/skills/mygo-maintenance` for lifetime and threading rules.
- Scan/decode/config failures are logged with `log.Printf` and shown as UI error or empty states; the app does not crash on bad files or folders.
