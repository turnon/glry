// Package embedding talks to the Ollama-compatible embedding service the
// app's sqlite search way stores its vectors from: a text or a
// base64-encoded image goes in, and one vector per input comes out.
package embedding

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// defaultURL is the embedding service the app asks unless configured
	// otherwise.
	defaultURL = "http://localhost:11434/api/embed"
	// defaultModel is the model the app asks for unless configured
	// otherwise.
	defaultModel = "embeddinggemma-2:440m-bf16-text-vision"
	// dimensions is the width of the vectors the app asks for, and so of
	// the images.vector column it stores them in.
	dimensions = 768
	// queryPrefix is what the embedding model expects before a search
	// query.
	queryPrefix = "task: search result | query: "
	// timeout lets the embedding service load a model on first use.
	timeout = 2 * time.Minute
	// maxBody bounds the response the app reads, so that a runaway service
	// cannot exhaust its memory.
	maxBody = 8 << 20
)

// EmbedConfig is the address and model of an embedding service, as the
// config file holds them.
type EmbedConfig struct {
	URL   string `json:"url"`
	Model string `json:"model"`
}

// DefaultEmbedConfig returns the service the app starts with.
func DefaultEmbedConfig() EmbedConfig {
	return EmbedConfig{URL: defaultURL, Model: defaultModel}
}

// Embedding is a configured embedding service.
type Embedding struct {
	embedURL   string
	embedModel string
}

// Default returns the embedding service the app starts with.
func Default() Embedding {
	c := DefaultEmbedConfig()
	return Embedding{embedURL: c.URL, embedModel: c.Model}
}

// URL returns the service's address, the default if it is unset.
func (e Embedding) URL() string {
	if e.embedURL == "" {
		return defaultURL
	}
	return e.embedURL
}

// Model returns the model the service is asked for, the default if it is
// unset.
func (e Embedding) Model() string {
	if e.embedModel == "" {
		return defaultModel
	}
	return e.embedModel
}

// Config returns the service's address and model, with the defaults filled
// in for unset values.
func (e Embedding) Config() EmbedConfig {
	return EmbedConfig{URL: e.URL(), Model: e.Model()}
}

// SetConfig sets the service's address and model. Blank values leave them
// unset, and so the defaults.
func (e *Embedding) SetConfig(c EmbedConfig) {
	e.SetURL(c.URL)
	e.SetModel(c.Model)
}

// SetURL sets the service's address. A blank address leaves it unset, and
// so the default.
func (e *Embedding) SetURL(url string) {
	e.embedURL = strings.TrimSpace(url)
}

// SetModel sets the model the service is asked for. A blank model leaves it
// unset, and so the default.
func (e *Embedding) SetModel(model string) {
	e.embedModel = strings.TrimSpace(model)
}

// Query embeds a search query, prefixed as the model expects, and returns
// its vector.
func (e *Embedding) Query(str string) ([]float32, error) {
	vectors, err := e.embed(queryPrefix + str)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("the embedding service returned no embedding for %q", str)
	}
	return vectors[0], nil
}

// Images embeds each base64-encoded image and returns their vectors, in the
// order the images were given.
func (e *Embedding) Images(base64images ...string) ([][]float32, error) {
	input := make([]map[string]string, len(base64images))
	for i, image := range base64images {
		input[i] = map[string]string{"image": image}
	}
	vectors, err := e.embed(input)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(input) {
		return nil, fmt.Errorf("the embedding service returned %d embeddings for %d images",
			len(vectors), len(input))
	}
	return vectors, nil
}

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

// embed posts input to the service and returns the vectors it answers, one
// per input.
func (e *Embedding) embed(input any) ([][]float32, error) {
	endpoint := e.URL()
	body, err := json.Marshal(embedRequest{
		Model:      e.Model(),
		Input:      input,
		Dimensions: dimensions,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout}
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
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
		if len(vector) != dimensions {
			return nil, fmt.Errorf("the embedding has %d dimensions, want %d",
				len(vector), dimensions)
		}
	}
	return vectors, nil
}
