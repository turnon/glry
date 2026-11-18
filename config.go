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

	"github.com/egoist/mygo/ui"
)

const (
	defaultCellWidth  = 176
	defaultCellHeight = 176
)

// cellSize is the size of a grid cell, in DIPs.
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

// formatPixels writes a size as the config file does, as 176px.
func formatPixels(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', -1, 32) + "px"
}

// configJSON formats size as the config file's JSON, for the config view.
func configJSON(size cellSize) string {
	cfg := configFile{Cell: cellConfig{
		Width:  formatPixels(size.width),
		Height: formatPixels(size.height),
	}}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return string(data) + "\n"
}

// configText is the text the config view edits: path's contents when the
// file holds JSON, else the JSON of size.
func configText(path string, size cellSize) string {
	data, err := os.ReadFile(path)
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		return string(data)
	}
	return configJSON(size)
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
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeConfigText(path, string(data))
}

// writeConfigText writes text to path, making the directory as needed.
func writeConfigText(path, text string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
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

// loadSettings reads path's config, applies its cell size, and puts the
// file's JSON in the config view's editor.
func (a *app) loadSettings(path string) {
	a.configPath = path
	a.setCellSize(loadConfig(path))
	a.configJSON = configText(path, cellSize{width: a.cellWidth, height: a.cellHeight})
	a.configErr = ""
	if !json.Valid([]byte(a.configJSON)) {
		a.configErr = "The config is not valid JSON."
	}
	a.configStatus = ""
	a.configStatusErr = false
}

// showConfig opens the config view.
func (a *app) showConfig() {
	if a.mode == viewConfig {
		return
	}
	a.mode = viewConfig
	a.clearSingle()
}

// configView edits the config file as JSON.
func (a *app) configView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Padding(24).Gap(12).Children(func() {
		ui.Text(c, "Config").FontSize(22).Bold()
		ui.Text(c, "Edit the app's settings as JSON. Cell sizes are in pixels.").FontSize(12).TextColor(t.TextMuted)
		editor := ui.TextArea(c, &a.configJSON).Label("Config JSON").Grow(1).FillWidth()
		editor.Error(a.configErr)
		editor.OnChange(func() {
			a.configErr = ""
			a.configStatus = ""
		})
		if a.configErr != "" {
			ui.Text(c, a.configErr).FontSize(12).TextColor(t.Danger)
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.PrimaryButton(c, "Save").OnClick(a.saveConfig)
			if a.configStatus != "" {
				color := t.TextMuted
				if a.configStatusErr {
					color = t.Danger
				}
				ui.Text(c, a.configStatus).FontSize(12).TextColor(color)
			}
			if a.configPath != "" {
				ui.Text(c, a.configPath).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(a.configPath)
			}
		})
	})
}

// saveConfig reads the editor's JSON, applies its cell size, and writes
// it to the config file when there is one. Invalid JSON or sizes stay in
// the editor with a message; a write that fails keeps the applied size and
// says why.
func (a *app) saveConfig() {
	a.configErr = ""
	a.configStatus = ""
	a.configStatusErr = false

	var cfg configFile
	if err := json.Unmarshal([]byte(a.configJSON), &cfg); err != nil {
		a.configErr = "Invalid JSON: " + err.Error()
		return
	}
	width, err := parsePixels(cfg.Cell.Width)
	if err != nil {
		a.configErr = "cell.width: " + err.Error()
		return
	}
	height, err := parsePixels(cfg.Cell.Height)
	if err != nil {
		a.configErr = "cell.height: " + err.Error()
		return
	}

	a.setCellSize(cellSize{width: width, height: height})
	if a.configPath == "" {
		a.configStatus = "Applied."
		return
	}
	if err := writeConfigText(a.configPath, a.configJSON); err != nil {
		a.configStatus = err.Error()
		a.configStatusErr = true
		return
	}
	a.configStatus = "Saved."
}
