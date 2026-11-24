package main

import "testing"

func TestGmbdIndexReturnsError(t *testing.T) {
	if _, err := (gmbd{url: "http://example.test"}).index("/tmp/a.png", false); err == nil {
		t.Error("the gmbd way indexed an image")
	}
}
