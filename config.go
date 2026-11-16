package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultCellWidth  = 176
	defaultCellHeight = 176
)

// cellSize is the size of a gallery cell, in DIPs.
type cellSize struct {
	width, height float32
}

func defaultCellSize() cellSize {
	return cellSize{width: defaultCellWidth, height: defaultCellHeight}
}

// configFile mirrors ~/.glry.json. Sizes are written with a px suffix, as
// {"cell": {"height": "176px", "width": "176px"}}.
type configFile struct {
	Cell cellConfig `json:"cell"`
}

type cellConfig struct {
	Height string `json:"height"`
	Width  string `json:"width"`
}

func defaultConfigFile() configFile {
	return configFile{Cell: cellConfig{
		Height: fmt.Sprintf("%dpx", defaultCellHeight),
		Width:  fmt.Sprintf("%dpx", defaultCellWidth),
	}}
}

// configPath is ~/.glry.json.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".glry.json"
	}
	return filepath.Join(home, ".glry.json")
}

// loadConfig reads the cell size from path, making the file with defaults
// when it is absent. A file that cannot be read or understood leaves the
// defaults in place.
func loadConfig(path string) cellSize {
	size := defaultCellSize()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := writeConfig(path, defaultConfigFile()); err != nil {
				log.Printf("writing %s: %v", path, err)
			}
		} else {
			log.Printf("reading %s: %v", path, err)
		}
		return size
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("reading %s: %v", path, err)
		return size
	}
	if cfg.Cell.Width != "" {
		if w, err := parsePixels(cfg.Cell.Width); err == nil {
			size.width = w
		} else {
			log.Printf("%s: cell width: %v", path, err)
		}
	}
	if cfg.Cell.Height != "" {
		if h, err := parsePixels(cfg.Cell.Height); err == nil {
			size.height = h
		} else {
			log.Printf("%s: cell height: %v", path, err)
		}
	}
	return size
}

// writeConfig writes cfg to path as JSON, making the directory as needed.
func writeConfig(path string, cfg configFile) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// parsePixels reads a length as 176px, or 176. It accepts surrounding
// space and either case for px. A size must be at least 1 pixel and at
// most 4096, a cell of either extreme being meaningless.
func parsePixels(s string) (float32, error) {
	v := strings.TrimSpace(strings.ToLower(s))
	v = strings.TrimSuffix(v, "px")
	v = strings.TrimSpace(v)
	n, err := strconv.ParseFloat(v, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size in pixels", strings.TrimSpace(s))
	}
	if n < 1 || n > 4096 {
		return 0, fmt.Errorf("%q must be between 1px and 4096px", strings.TrimSpace(s))
	}
	return float32(n), nil
}
