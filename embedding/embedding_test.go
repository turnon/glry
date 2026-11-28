package embedding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// embedAnswer answers an embed request with one dimensions-wide vector per
// input, as the service does.
func embedAnswer(t *testing.T, w http.ResponseWriter, n int) {
	t.Helper()
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = make([]float32, dimensions)
		vectors[i][0] = float32(i + 1)
	}
	body, err := json.Marshal(map[string][][]float32{"embeddings": vectors})
	if err != nil {
		t.Errorf("writing the embed answer: %v", err)
		return
	}
	w.Write(body)
}

// Query asks the configured model for the vector of the text with the prefix
// it expects, at the dimensions the app stores.
func TestQueryEmbedsPrefixedText(t *testing.T) {
	var got embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("reading the embed request: %v", err)
		}
		embedAnswer(t, w, 1)
	}))
	defer srv.Close()

	e := Default()
	e.SetURL(srv.URL)
	e.SetModel("test-embed")
	vector, err := e.Query("red car")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "test-embed" || got.Dimensions != dimensions || got.Input != queryPrefix+"red car" {
		t.Errorf("the query was embedded as %+v", got)
	}
	if len(vector) != dimensions || vector[0] != 1 {
		t.Errorf("Query returned a vector of %d, want the service's", len(vector))
	}
}

// Images posts each image in the batch as its base64 field, and returns the
// service's vectors in the order the images were given.
func TestImagesEmbedsBase64Images(t *testing.T) {
	var got embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("reading the embed request: %v", err)
		}
		embedAnswer(t, w, 2)
	}))
	defer srv.Close()

	e := Default()
	e.SetURL(srv.URL)
	vectors, err := e.Images("AAA", "BBB")
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.Input.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("the images were embedded as %#v, want a batch of 2", got.Input)
	}
	for i, want := range []string{"AAA", "BBB"} {
		item, ok := items[i].(map[string]any)
		if !ok || item["image"] != want {
			t.Errorf("image %d was embedded as %#v, want its base64 %q", i, items[i], want)
		}
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][0] != 2 {
		t.Errorf("Images returned %d vectors, want the service's two", len(vectors))
	}
}
