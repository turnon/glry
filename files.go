package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	stddraw "image/draw"
	_ "image/gif"  // image.Decode
	_ "image/jpeg" // image.Decode
	_ "image/png"  // image.Decode
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "golang.org/x/image/bmp" // image.Decode
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // image.Decode
)

// scanDirs lists a directory's visible subdirectories, sorted by name.
// It returns nil for a directory without any.
func scanDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !e.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(dir, e.Name()))
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(rootName(dirs[i])) < strings.ToLower(rootName(dirs[j]))
	})
	if len(dirs) == 0 {
		return nil, nil
	}
	return dirs, nil
}

// scanGallery lists a directory's visible subdirectories and images,
// each sorted by name, for the tree and the gallery in one read.
func scanGallery(dir string) ([]string, []*photo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	dirs := make([]string, 0, len(entries))
	photos := make([]*photo, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
			continue
		}
		if !isImageName(e.Name()) {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		photos = append(photos, &photo{
			path: filepath.Join(dir, e.Name()),
			name: e.Name(),
			size: size,
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(rootName(dirs[i])) < strings.ToLower(rootName(dirs[j]))
	})
	sort.Slice(photos, func(i, j int) bool {
		return strings.ToLower(photos[i].name) < strings.ToLower(photos[j].name)
	})
	if len(dirs) == 0 {
		dirs = nil
	}
	return dirs, photos, nil
}

// scanImages lists the images in a directory, sorted by name.
func scanImages(dir string) ([]*photo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	photos := make([]*photo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !isImageName(e.Name()) {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		photos = append(photos, &photo{
			path: filepath.Join(dir, e.Name()),
			name: e.Name(),
			size: size,
		})
	}
	sort.Slice(photos, func(i, j int) bool {
		return strings.ToLower(photos[i].name) < strings.ToLower(photos[j].name)
	})
	return photos, nil
}

// isImageName reports whether a file's extension is one of the formats
// DecodeImage and decodeThumb read.
func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".jfif", ".gif", ".webp", ".bmp", ".svg":
		return true
	}
	return false
}

func isSVG(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".svg")
}

// decodeThumb decodes an image and scales it down to fit maxPixels on its
// longest side, applying a JPEG's EXIF orientation. It returns the
// thumbnail, and the original's width and height as they show. The
// thumbnail is an RGBA image of its own, made on a goroutine, which
// ui.NewBitmap can take without a copy on the main thread.
func decodeThumb(data []byte, maxPixels int) (image.Image, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("empty image")
	}
	tw, th := w, h
	if maxPixels > 0 && (w > maxPixels || h > maxPixels) {
		scale := float64(maxPixels) / float64(max(w, h))
		tw = max(1, int(math.Round(float64(w)*scale)))
		th = max(1, int(math.Round(float64(h)*scale)))
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	if tw == w && th == h {
		stddraw.Draw(dst, dst.Bounds(), img, b.Min, stddraw.Src)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	}
	dw, dh := w, h
	if o := exifOrientation(data); o > 1 {
		dst = orientRGBA(dst, o)
		if o >= 5 {
			dw, dh = h, w
		}
	}
	return dst, dw, dh, nil
}

// exifOrientation returns the orientation of a JPEG's EXIF data, 1 to 8,
// or 0 without one.
func exifOrientation(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xff || jpg[1] != 0xd8 {
		return 0
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xff {
			return 0
		}
		marker, size := jpg[i+1], int(binary.BigEndian.Uint16(jpg[i+2:]))
		if marker == 0xda || size < 2 || i+2+size > len(jpg) {
			return 0 // the image data starts, or the segment is malformed
		}
		seg := jpg[i+4 : i+2+size]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 0
}

// tiffOrientation reads the Orientation tag of the first IFD of TIFF data.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[ifd:]))
	for e := ifd + 2; e+12 <= len(t) && n > 0; e, n = e+12, n-1 {
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 { // Orientation, a SHORT
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orientRGBA turns and flips img as EXIF orientation o says it is to show.
func orientRGBA(src *image.RGBA, o int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			default:
				sx, sy = x, y
			}
			di := dst.PixOffset(x, y)
			si := src.PixOffset(sx, sy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
