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

// searchWay is how the search view finds images: through gmbd's HTTP
// service, or through the SQLite vector database the app owns.
type searchWay string

const (
	searchWayGmbd   searchWay = "gmbd"
	searchWaySQLite searchWay = "sqlite"
)

// parseSearchWay reads a search.way value. An empty value is gmbd, so a
// config written before the search settings existed keeps working.
func parseSearchWay(s string) (searchWay, error) {
	switch searchWay(strings.ToLower(strings.TrimSpace(s))) {
	case searchWayGmbd, "":
		return searchWayGmbd, nil
	case searchWaySQLite:
		return searchWaySQLite, nil
	}
	return "", fmt.Errorf(`search.way must be "gmbd" or "sqlite"`)
}

// settings is what the config file holds, once read: the size of a grid
// cell and how the search view finds images.
type settings struct {
	cell       cellSize
	searchWay  searchWay
	embedURL   string
	embedModel string
}

func defaultSettings() settings {
	return settings{
		cell:       defaultCellSize(),
		searchWay:  searchWayGmbd,
		embedURL:   defaultEmbedURL,
		embedModel: defaultEmbedModel,
	}
}

// cellSize is the size of a grid cell, in DIPs.
type cellSize struct {
	width, height float32
}

func defaultCellSize() cellSize {
	return cellSize{width: defaultCellWidth, height: defaultCellHeight}
}

// configFile mirrors ~/.glry/config.json. Sizes are written with a px suffix, as
// {"cell": {"height": "176px", "width": "176px"}}, and embed sits beside
// cell and search.
type configFile struct {
	Cell   cellConfig   `json:"cell"`
	Search searchConfig `json:"search"`
	Embed  embedConfig  `json:"embed"`
}

type cellConfig struct {
	Height string `json:"height"`
	Width  string `json:"width"`
}

// searchConfig chooses how the search view finds images. The embed config
// beside it names the service that turns a query into the vector the
// sqlite way matches.
type searchConfig struct {
	Way string `json:"way"`
}

type embedConfig struct {
	URL   string `json:"url"`
	Model string `json:"model"`
}

func defaultConfigFile() configFile {
	return configFile{
		Cell: cellConfig{
			Height: fmt.Sprintf("%dpx", defaultCellHeight),
			Width:  fmt.Sprintf("%dpx", defaultCellWidth),
		},
		Search: searchConfig{Way: string(searchWayGmbd)},
		Embed:  embedConfig{URL: defaultEmbedURL, Model: defaultEmbedModel},
	}
}

// formatPixels writes a size as the config file does, as 176px.
func formatPixels(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', -1, 32) + "px"
}

// configJSON formats the settings as the config file's JSON, for the
// config view.
func configJSON(s settings) string {
	cfg := configFile{
		Cell: cellConfig{
			Width:  formatPixels(s.cell.width),
			Height: formatPixels(s.cell.height),
		},
		Search: searchConfig{Way: string(s.searchWay)},
		Embed:  embedConfig{URL: s.embedURL, Model: s.embedModel},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return string(data) + "\n"
}

// configText is the text the config view edits: path's contents when the
// file holds JSON, else the JSON of s.
func configText(path string, s settings) string {
	data, err := os.ReadFile(path)
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		return string(data)
	}
	return configJSON(s)
}

// glryDir is where the app keeps its files: ~/.glry.
func glryDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".glry"
	}
	return filepath.Join(home, ".glry")
}

// configPath is ~/.glry/config.json.
func configPath() string {
	return filepath.Join(glryDir(), "config.json")
}

// loadConfig reads the settings from path, making the file with defaults
// when it is absent. A file that cannot be read or understood leaves the
// defaults in place.
func loadConfig(path string) settings {
	s := defaultSettings()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := writeConfig(path, defaultConfigFile()); err != nil {
				log.Printf("writing %s: %v", path, err)
			}
		} else {
			log.Printf("reading %s: %v", path, err)
		}
		return s
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("reading %s: %v", path, err)
		return s
	}
	if cfg.Cell.Width != "" {
		if w, err := parsePixels(cfg.Cell.Width); err == nil {
			s.cell.width = w
		} else {
			log.Printf("%s: cell width: %v", path, err)
		}
	}
	if cfg.Cell.Height != "" {
		if h, err := parsePixels(cfg.Cell.Height); err == nil {
			s.cell.height = h
		} else {
			log.Printf("%s: cell height: %v", path, err)
		}
	}
	if cfg.Search.Way != "" {
		if way, err := parseSearchWay(cfg.Search.Way); err == nil {
			s.searchWay = way
		} else {
			log.Printf("%s: %v", path, err)
		}
	}
	if cfg.Embed.URL != "" {
		s.embedURL = strings.TrimSpace(cfg.Embed.URL)
	}
	if cfg.Embed.Model != "" {
		s.embedModel = strings.TrimSpace(cfg.Embed.Model)
	}
	return s
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

// loadSettings reads path's config, applies its settings, and puts the
// file's JSON in the config view's editor.
func (a *app) loadSettings(path string) {
	a.configPath = path
	s := loadConfig(path)
	a.setCellSize(s.cell)
	a.setSearcher(s.searchWay)
	a.embedURL = s.embedURL
	a.embedModel = s.embedModel
	a.configJSON = configText(path, s)
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
		ui.Text(c, "Edit the app's settings as JSON. Cell sizes are in pixels. search.way picks \"gmbd\" or \"sqlite\"; the sqlite way embeds the query through embed.").FontSize(12).TextColor(t.TextMuted)
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

// saveConfig reads the editor's JSON, applies its settings, and writes
// it to the config file when there is one. Invalid JSON or values stay in
// the editor with a message; a write that fails keeps the applied settings
// and says why.
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
	way, err := parseSearchWay(cfg.Search.Way)
	if err != nil {
		a.configErr = "search.way: " + err.Error()
		return
	}
	embedURL := strings.TrimSpace(cfg.Embed.URL)
	if embedURL == "" {
		embedURL = defaultEmbedURL
	}
	embedModel := strings.TrimSpace(cfg.Embed.Model)
	if embedModel == "" {
		embedModel = defaultEmbedModel
	}

	a.setCellSize(cellSize{width: width, height: height})
	a.setSearcher(way)
	a.embedURL = embedURL
	a.embedModel = embedModel
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
