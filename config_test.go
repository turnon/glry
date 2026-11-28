package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/turnon/glry/embedding"
	"github.com/turnon/glry/searcher"
)

func TestLoadConfigCreatesDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings := loadConfig(path)
	if settings.cell != defaultCellSize() {
		t.Errorf("loaded %+v, want defaults %+v", settings.cell, defaultCellSize())
	}
	if settings.searchWay != searchWayGmbd || settings.embed != embedding.DefaultEmbedConfig() {
		t.Errorf("loaded search settings %+v, want gmbd defaults", settings)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the default config was not written: %v", err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("the default config is not JSON: %v\n%s", err, data)
	}
	if cfg.Cell.Height != "176px" || cfg.Cell.Width != "176px" {
		t.Errorf("default config is %+v", cfg)
	}
	if cfg.Search.Way != "gmbd" || cfg.Embed != embedding.DefaultEmbedConfig() {
		t.Errorf("default config search is %+v / %+v", cfg.Search, cfg.Embed)
	}
}

func TestLoadConfigReadsCellSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"cell":{"height":"20px","width":"15px"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := loadConfig(path)
	if settings.cell.width != 15 || settings.cell.height != 20 {
		t.Errorf("loaded %+v, want 15×20", settings.cell)
	}
	if settings.searchWay != searchWayGmbd {
		t.Errorf("an old config's search way is %q, want gmbd", settings.searchWay)
	}
}

func TestParsePixels(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float32
		ok   bool
	}{
		{"20px", 20, true},
		{" 15 PX ", 15, true},
		{"12", 12, true},
		{"0px", 0, false},
		{"-4px", 0, false},
		{"0.5px", 0, false},
		{"5000px", 0, false},
		{"wide", 0, false},
		{"", 0, false},
	} {
		got, err := parsePixels(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("parsePixels(%q) error = %v, want ok %v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("parsePixels(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLoadConfigReadsSearchWay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"search":{"way":"sqlite"},"embed":{"url":"http://example.test/embed","model":"m"}}`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := loadConfig(path)
	if settings.searchWay != searchWaySQLite {
		t.Errorf("search way is %q, want sqlite", settings.searchWay)
	}
	if settings.embed.URL != "http://example.test/embed" || settings.embed.Model != "m" {
		t.Errorf("embed settings are %q/%q", settings.embed.URL, settings.embed.Model)
	}
}

func TestParseSearchWay(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want searchWay
		ok   bool
	}{
		{"", searchWayGmbd, true},
		{"gmbd", searchWayGmbd, true},
		{" SQLITE ", searchWaySQLite, true},
		{"PostgreSQL", searchWayPostgresql, true},
		{"other", "", false},
	} {
		got, err := parseSearchWay(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("parseSearchWay(%q) = %q, %v, want %q, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestLoadConfigReadsPostgresqlBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"search":{"way":"postgresql","backends":[{"way":"postgresql","dsn":"postgres://example.test/glry"}]}}`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := loadConfig(path)
	if settings.searchWay != searchWayPostgresql {
		t.Errorf("search way is %q, want postgresql", settings.searchWay)
	}
	dsn, err := postgresDSN(settings.backends)
	if err != nil || dsn != "postgres://example.test/glry" {
		t.Errorf("the backend DSN is %q, %v, want postgres://example.test/glry", dsn, err)
	}
}

// A postgresql search way without a filled backend falls back to gmbd
// rather than searching a database it does not name.
func TestLoadConfigPostgresqlWithoutDSNUsesGmbd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"search":{"way":"postgresql","backends":[{"way":"postgresql","dsn":""}]}}`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if settings := loadConfig(path); settings.searchWay != searchWayGmbd {
		t.Errorf("search way is %q, want gmbd", settings.searchWay)
	}
}

func TestSaveConfigAppliesPostgresqlBackend(t *testing.T) {
	a := syncApp(t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	a.configPath = path
	a.config = configFile{
		Cell: cellConfig{Height: "20px", Width: "15px"},
		Search: searchConfig{
			Way:      "postgresql",
			Backends: []searchBackend{{Way: "postgresql", DSN: "postgres://example.test/glry"}},
		},
	}
	a.saveConfig()
	if a.configErr != "" {
		t.Fatalf("saving the config failed: %s", a.configErr)
	}
	pg, ok := a.searcher.(searcher.Postgres)
	if !ok || pg.DSN != "postgres://example.test/glry" {
		t.Errorf("the config applied %+v, want the postgresql backend's DSN", a.searcher)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the config was not written: %v", err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("the saved config is not JSON: %v\n%s", err, data)
	}
	want := searchBackend{Way: "postgresql", DSN: "postgres://example.test/glry"}
	if cfg.Search.Way != "postgresql" || len(cfg.Search.Backends) != 1 || cfg.Search.Backends[0] != want {
		t.Errorf("the saved config is %+v, want the postgresql backend", cfg.Search)
	}
}

// The postgresql way needs its backend's DSN filled, and a save without one
// says so rather than writing a config that cannot search.
func TestSaveConfigRejectsPostgresqlWithoutDSN(t *testing.T) {
	a := syncApp(t.TempDir())
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	a.config = configFile{
		Cell:   cellConfig{Height: "20px", Width: "15px"},
		Search: searchConfig{Way: "postgresql", Backends: []searchBackend{{Way: "postgresql"}}},
	}
	a.saveConfig()
	if a.configErr != `search.backends: the postgresql backend in search.backends needs a "dsn"` {
		t.Errorf("the config error is %q", a.configErr)
	}
	if a.configStatus != "" {
		t.Errorf("a rejected config reports %q", a.configStatus)
	}
}

func TestSaveConfigAppliesSearchWay(t *testing.T) {
	a := syncApp(t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	a.configPath = path
	a.config = configFile{
		Cell:   cellConfig{Height: "20px", Width: "15px"},
		Search: searchConfig{Way: "sqlite"},
		Embed:  embedding.EmbedConfig{URL: "http://example.test/embed", Model: "m"},
	}
	a.saveConfig()
	if a.configErr != "" {
		t.Fatalf("saving the config failed: %s", a.configErr)
	}
	if _, ok := a.searcher.(searcher.SQLite); !ok || a.embed.URL() != "http://example.test/embed" || a.embed.Model() != "m" {
		t.Errorf("the config applied %+v", a.searcher)
	}
	if a.cellWidth != 15 || a.cellHeight != 20 {
		t.Errorf("the config applied cell %v×%v", a.cellWidth, a.cellHeight)
	}
	if a.configStatus != "Saved." {
		t.Errorf("the config status is %q", a.configStatus)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the config was not written: %v", err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("the saved config is not JSON: %v\n%s", err, data)
	}
	if cfg.Cell.Width != "15px" || cfg.Cell.Height != "20px" || cfg.Search.Way != "sqlite" || cfg.Embed.URL != "http://example.test/embed" || cfg.Embed.Model != "m" {
		t.Errorf("the saved config is %+v", cfg)
	}
}

func TestSaveConfigRejectsBadCellSize(t *testing.T) {
	a := syncApp(t.TempDir())
	a.config = defaultConfigFile()
	a.config.Cell.Width = "wide"
	a.saveConfig()
	if a.configErr != `cell.width: "wide" is not a size in pixels` {
		t.Errorf("the config error is %q", a.configErr)
	}
	if a.configStatus != "" {
		t.Errorf("a rejected config reports %q", a.configStatus)
	}
}

func TestConfigViewEditsTheForm(t *testing.T) {
	a := syncApp(t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	a.loadSettings(path)
	a.showConfig()
	tt := ui.NewTester(a.view, 900, 600)
	for _, text := range []string{"Cell", "Search", "Embedding", "Width", "Height", "Way", "URL", "Model", "Save"} {
		if !tt.HasText(text) {
			t.Errorf("the config form does not show %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Width"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("20px")
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.configErr != "" || a.configStatus != "Saved." {
		t.Fatalf("saving the form reports %q / %q", a.configErr, a.configStatus)
	}
	if a.cellWidth != 20 {
		t.Errorf("the form saved cell width %v, want 20", a.cellWidth)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the config was not written: %v", err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Cell.Width != "20px" {
		t.Errorf("the saved config is %+v, %v\n%s", cfg, err, data)
	}
}

// The DSN field belongs to the postgresql way alone: the other ways have
// no database to connect to.
func TestConfigShowsDSNForPostgresqlOnly(t *testing.T) {
	a := syncApp(t.TempDir())
	a.loadSettings(filepath.Join(t.TempDir(), "config.json"))
	a.showConfig()
	tt := ui.NewTester(a.view, 900, 600)

	if _, ok := tt.Find("DSN"); ok {
		t.Fatalf("the gmbd way shows the DSN field: %q", tt.Texts())
	}
	a.config.Search.Way = "sqlite"
	tt.Frame()
	if _, ok := tt.Find("DSN"); ok {
		t.Errorf("the sqlite way shows the DSN field: %q", tt.Texts())
	}

	a.config.Search.Way = "postgresql"
	tt.Frame()
	if _, ok := tt.Find("DSN"); !ok {
		t.Errorf("the postgresql way hides the DSN field: %q", tt.Texts())
	}
}

func TestConfigInputWidths(t *testing.T) {
	a := syncApp(t.TempDir())
	a.loadSettings(filepath.Join(t.TempDir(), "config.json"))
	a.showConfig()
	tt := ui.NewTester(a.view, 900, 600)
	for _, tc := range []struct {
		name  string
		role  ui.Role
		width float32
	}{
		{"Width", ui.RoleTextField, 200},
		{"Height", ui.RoleTextField, 200},
		{"Way", ui.RolePopUpButton, 200},
		{"URL", ui.RoleTextField, 300},
		{"Model", ui.RoleTextField, 400},
	} {
		info, ok := tt.AccessByRole(tc.role, tc.name)
		if !ok {
			t.Errorf("the config form has no %s control", tc.name)
			continue
		}
		if info.Bounds.W != tc.width {
			t.Errorf("%s is %v wide, want %v", tc.name, info.Bounds.W, tc.width)
		}
	}
}

func TestConfigSectionsHighlightOnHover(t *testing.T) {
	a := syncApp(t.TempDir())
	a.loadSettings(filepath.Join(t.TempDir(), "config.json"))
	a.showConfig()
	tt := ui.NewTester(a.view, 900, 600)
	section, ok := tt.Find("Cell")
	if !ok {
		t.Fatal("the config form does not show the Cell section")
	}
	x, y := int(section.X+3), int(section.Y+3)
	plain := tt.Image().RGBAAt(x, y)
	tt.Move(section.X+section.W/2, section.Y+section.H/2)
	hovered := tt.Image().RGBAAt(x, y)
	if hovered == plain {
		t.Errorf("the section stays %v under the pointer", plain)
	}
}
