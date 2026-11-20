package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigCreatesDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings := loadConfig(path)
	if settings.cell != defaultCellSize() {
		t.Errorf("loaded %+v, want defaults %+v", settings.cell, defaultCellSize())
	}
	if settings.searchWay != searchWayGmbd || settings.embedURL != defaultEmbedURL || settings.embedModel != defaultEmbedModel {
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
	if cfg.Search.Way != "gmbd" || cfg.Embed.URL != defaultEmbedURL || cfg.Embed.Model != defaultEmbedModel {
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
	if settings.embedURL != "http://example.test/embed" || settings.embedModel != "m" {
		t.Errorf("embed settings are %q/%q", settings.embedURL, settings.embedModel)
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
		{"other", "", false},
	} {
		got, err := parseSearchWay(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("parseSearchWay(%q) = %q, %v, want %q, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestSaveConfigAppliesSearchWay(t *testing.T) {
	a := syncApp(t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	a.configPath = path
	a.configJSON = `{
		"cell": {"height": "20px", "width": "15px"},
		"search": {"way": "sqlite"},
		"embed": {"url": "http://example.test/embed", "model": "m"}
	}`
	a.saveConfig()
	if a.configErr != "" {
		t.Fatalf("saving the config failed: %s", a.configErr)
	}
	if a.searchWay != searchWaySQLite || a.embedURL != "http://example.test/embed" || a.embedModel != "m" {
		t.Errorf("the config applied %+v", a.searchWay)
	}
	if a.cellWidth != 15 || a.cellHeight != 20 {
		t.Errorf("the config applied cell %v×%v", a.cellWidth, a.cellHeight)
	}
	if a.configStatus != "Saved." {
		t.Errorf("the config status is %q", a.configStatus)
	}
}
