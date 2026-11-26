package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGmbdIndexReturnsError(t *testing.T) {
	if _, err := (gmbd{url: "http://example.test"}).index("/tmp/a.png", false); err == nil {
		t.Error("the gmbd way indexed an image")
	}
}

func TestGmbdSearchRejectsImageQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.png")
	if err := os.WriteFile(path, []byte("not read"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (gmbd{url: "http://example.test"}).search(path, "", 1); err == nil ||
		!strings.Contains(err.Error(), "can't search by an image") {
		t.Errorf("the gmbd way searched by %s with %v", path, err)
	}
}
