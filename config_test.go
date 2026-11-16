package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigCreatesDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".glry.json")
	size := loadConfig(path)
	if size != defaultCellSize() {
		t.Errorf("loaded %+v, want defaults %+v", size, defaultCellSize())
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
}

func TestLoadConfigReadsCellSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".glry.json")
	if err := os.WriteFile(path, []byte(`{"cell":{"height":"20px","width":"15px"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	size := loadConfig(path)
	if size.width != 15 || size.height != 20 {
		t.Errorf("loaded %+v, want 15×20", size)
	}
}

func TestLoadConfigKeepsDefaultsForInvalidSizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".glry.json")
	if err := os.WriteFile(path, []byte(`{"cell":{"height":"nope","width":"15px"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	size := loadConfig(path)
	if size.width != 15 {
		t.Errorf("width is %v, want 15", size.width)
	}
	if size.height != defaultCellHeight {
		t.Errorf("height is %v, want the default %v", size.height, float32(defaultCellHeight))
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
