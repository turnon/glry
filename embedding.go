package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The embedding service configured in embed speaks Ollama's /api/embed API:
// a text or a base64-encoded image goes in, and one vector per input comes
// out.
const (
	defaultEmbedURL   = "http://localhost:11434/api/embed"
	defaultEmbedModel = "embeddinggemma-2:440m-bf16-text-vision"
	// embedTimeout lets the embedding service load a model on first use.
	embedTimeout = 2 * time.Minute
	// queryEmbeddingPrefix is what the embedding model expects before a
	// search query.
	queryEmbeddingPrefix = "task: search result | query: "
	// embedMaxBody bounds the response the app reads, so that a runaway
	// service cannot exhaust its memory.
	embedMaxBody = 8 << 20
)

// embedRequest is Ollama's embed API body. Input is a string for one text,
// or a list mixing texts and maps of "image" for a batch.
type embedRequest struct {
	Model      string `json:"model"`
	Input      any    `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

// embedResponse holds either Ollama's embeddings or an OpenAI-compatible
// data array.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Data       []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// query embeds a search query, prefixed as the model expects, and returns
// its vector.
func (a *app) query(str string) ([]float32, error) {
	vectors, err := a.embed(queryEmbeddingPrefix + str)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("the embedding service returned no embedding for %q", str)
	}
	return vectors[0], nil
}

// images embeds each base64-encoded image and returns their vectors, in the
// order the images were given.
func (a *app) images(base64images ...string) ([][]float32, error) {
	input := make([]map[string]string, len(base64images))
	for i, image := range base64images {
		input[i] = map[string]string{"image": image}
	}
	vectors, err := a.embed(input)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(input) {
		return nil, fmt.Errorf("the embedding service returned %d embeddings for %d images",
			len(vectors), len(input))
	}
	return vectors, nil
}

// embed posts input to the app's embedding service and returns the vectors
// it answers, one per input.
func (a *app) embed(input any) ([][]float32, error) {
	endpoint := a.embedURL
	if endpoint == "" {
		endpoint = defaultEmbedURL
	}
	model := a.embedModel
	if model == "" {
		model = defaultEmbedModel
	}
	body, err := json.Marshal(embedRequest{
		Model:      model,
		Input:      input,
		Dimensions: sqliteVectorDims,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: embedTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking %s to embed: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(detail))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("the embedding service answered %s: %s", resp.Status, msg)
	}
	var out embedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, embedMaxBody)).Decode(&out); err != nil {
		return nil, fmt.Errorf("reading the embedding service's answer: %w", err)
	}
	vectors := out.Embeddings
	if len(vectors) == 0 && len(out.Data) > 0 {
		vectors = make([][]float32, len(out.Data))
		for i, item := range out.Data {
			vectors[i] = item.Embedding
		}
	}
	for _, vector := range vectors {
		if len(vector) != sqliteVectorDims {
			return nil, fmt.Errorf("the embedding has %d dimensions, want %d",
				len(vector), sqliteVectorDims)
		}
	}
	return vectors, nil
}
