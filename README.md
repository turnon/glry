# glry

A read-only image browser built with [MyGo](https://github.com/egoist/mygo) native `ui`: a folder tree, a thumbnail gallery, and a single-image view with history tags. Supports PNG, JPEG, GIF, WebP, BMP and SVG (hidden files skipped, JPEG EXIF orientation applied).

In the single-image view the wheel and a pinch zoom the photo about the pointer, and a touchpad's scroll, the scroll bars or a pointer drag move it. Zoomed past the window, the view shows scroll bars for the parts out of sight. Each history tag keeps where its photo showed: going back to a tag returns to its zoom and place, and stepping with the arrows or double clicking fits only the photo you are on. Each tag's close icon, or Escape on the current tag, drops it: the view shows the tag opened before it, and the gallery when that was the last.

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
- The single image view fits the photo at `1` and zooms up to `8`; `tagViews` keeps a `tagView` of `zoom` and `ui.ScrollState` per history tag, in step with `history` (`addTag` appends both, dropping the oldest of each). At a zoom the view lays the photo out in a `ScrollBoth` tracked by that tag's state, so its scroll bars show and scroll it. `singleRef` gives the view's committed room to the input handler (`singleInput`), which anchors zoom at the pointer and turns drags into scroll offsets; `singleFitSize`/`singleImageSize`/`singleContentSize` compute the same geometry the view lays out, of the tag that `currentTagView` returns. Only the arrows (`stepSingle`) and a double click fit again, and then just that tag (`resetSingleZoom`/`fitTagView`).
- Thumbnails are requested only by cells the grid builds, and only once per photo (`thumbState`).
- `history` tags outlive folder changes (`pendingTag` waits for a load); `showInTag` renames the current tag for arrow navigation while `showPhotoAt` adds or finds one, and `closeTag` drops one with its `tagView`, showing the tag opened before the current one, or the gallery when none is left.
- Store app data and handles, never `ui.Element`/`ui.Context`. Follow `.agents/skills/mygo-maintenance` for lifetime and threading rules.
- Scan/decode/config failures are logged with `log.Printf` and shown as UI error or empty states; the app does not crash on bad files or folders.
