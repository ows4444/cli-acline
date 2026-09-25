package embed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestFromEnvRequiresAPIKey(t *testing.T) {
	t.Setenv("VOYAGE_API_KEY", "")
	os.Unsetenv("VOYAGE_API_KEY")
	if _, ok := FromEnv(); ok {
		t.Fatal("expected FromEnv to return ok=false with no VOYAGE_API_KEY set")
	}
}

func TestFromEnvDefaultsModel(t *testing.T) {
	t.Setenv("VOYAGE_API_KEY", "test-key")
	t.Setenv("ACLINE_EMBED_MODEL", "")
	os.Unsetenv("ACLINE_EMBED_MODEL")
	c, ok := FromEnv()
	if !ok {
		t.Fatal("expected FromEnv to return ok=true with VOYAGE_API_KEY set")
	}
	if c.Model() != defaultModel {
		t.Errorf("expected default model %q, got %q", defaultModel, c.Model())
	}
}

func TestFromEnvHonorsModelOverride(t *testing.T) {
	t.Setenv("VOYAGE_API_KEY", "test-key")
	t.Setenv("ACLINE_EMBED_MODEL", "voyage-3-large")
	c, ok := FromEnv()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if c.Model() != "voyage-3-large" {
		t.Errorf("expected overridden model, got %q", c.Model())
	}
}

func TestEmbedSendsExpectedRequestAndParsesResponse(t *testing.T) {
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		gotBody = req.InputType
		if req.Model != "voyage-3.5-lite" {
			t.Errorf("expected model in request body, got %q", req.Model)
		}
		if len(req.Input) != 2 {
			t.Fatalf("expected 2 input texts, got %d", len(req.Input))
		}
		resp := embedResponse{}
		for range req.Input {
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
			}{Embedding: []float32{0.1, 0.2, 0.3}})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := &Client{apiKey: "test-key", model: "voyage-3.5-lite", http: server.Client(), baseURL: server.URL}

	vectors, err := c.Embed([]string{"hello", "world"}, "document")
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vectors))
	}
	if len(vectors[0]) != 3 {
		t.Fatalf("expected 3-dim vector, got %d", len(vectors[0]))
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("expected Authorization header, got %q", gotAuth)
	}
	if gotBody != "document" {
		t.Errorf("expected input_type=document in request, got %q", gotBody)
	}
}

func TestEmbedReturnsErrorOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"detail": "invalid api key"})
	}))
	defer server.Close()

	c := &Client{apiKey: "bad-key", model: "voyage-3.5-lite", http: server.Client(), baseURL: server.URL}
	_, err := c.Embed([]string{"hello"}, "document")
	if err == nil {
		t.Fatal("expected an error on a 401 response")
	}
}

// TestEmbedChunksLargeBatches is a regression test: Embed previously sent every text in one request with no
// regard for Voyage's per-request batch limit. This confirms a request
// larger than maxBatchSize is split into multiple requests, each capped at
// maxBatchSize, and that the returned vectors are still in the original
// order across the chunk boundary.
func TestEmbedChunksLargeBatches(t *testing.T) {
	var requestSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		requestSizes = append(requestSizes, len(req.Input))
		resp := embedResponse{}
		for _, text := range req.Input {
			// Encode which input this vector came from into its first
			// dimension, so the test can confirm ordering survives chunking.
			var n float32
			fmt.Sscanf(text, "text-%f", &n)
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
			}{Embedding: []float32{n}})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	total := maxBatchSize + 10
	texts := make([]string, total)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	c := &Client{apiKey: "k", model: "voyage-3.5-lite", http: server.Client(), baseURL: server.URL, sleep: func(time.Duration) {}}
	vectors, err := c.Embed(texts, "document")
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != total {
		t.Fatalf("expected %d vectors, got %d", total, len(vectors))
	}
	if len(requestSizes) != 2 || requestSizes[0] != maxBatchSize || requestSizes[1] != 10 {
		t.Fatalf("expected 2 requests of sizes [%d, 10], got %v", maxBatchSize, requestSizes)
	}
	for i, v := range vectors {
		if len(v) != 1 || v[0] != float32(i) {
			t.Fatalf("vector %d out of order: got %v", i, v)
		}
	}
}

// TestEmbedRetriesTransientFailures is a regression test: a single HTTP attempt with no retry/backoff previously
// failed the whole batch on one flaky connection or rate limit. This
// confirms a 503 followed by a 200 succeeds without the caller seeing an
// error, and that a non-retryable 401 fails immediately (no wasted retries).
func TestEmbedRetriesTransientFailures(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"detail": "overloaded"})
			return
		}
		resp := embedResponse{Data: []struct {
			Embedding []float32 `json:"embedding"`
		}{{Embedding: []float32{1, 2, 3}}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := &Client{apiKey: "k", model: "voyage-3.5-lite", http: server.Client(), baseURL: server.URL, sleep: func(time.Duration) {}}
	vectors, err := c.Embed([]string{"hello"}, "document")
	if err != nil {
		t.Fatalf("expected retry to eventually succeed, got: %v", err)
	}
	if len(vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(vectors))
	}
	if attempts != 3 {
		t.Fatalf("expected exactly 3 attempts (2 failures + 1 success), got %d", attempts)
	}
}

func TestEmbedDoesNotRetryNonTransientFailures(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"detail": "invalid api key"})
	}))
	defer server.Close()

	c := &Client{apiKey: "bad-key", model: "voyage-3.5-lite", http: server.Client(), baseURL: server.URL, sleep: func(time.Duration) {}}
	_, err := c.Embed([]string{"hello"}, "document")
	if err == nil {
		t.Fatal("expected an error on a 401 response")
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt for a non-retryable error, got %d", attempts)
	}
}

func TestEmbedGivesUpAfterMaxAttempts(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"detail": "still overloaded"})
	}))
	defer server.Close()

	c := &Client{apiKey: "k", model: "voyage-3.5-lite", http: server.Client(), baseURL: server.URL, sleep: func(time.Duration) {}}
	_, err := c.Embed([]string{"hello"}, "document")
	if err == nil {
		t.Fatal("expected an error after exhausting retries against a persistently failing server")
	}
	if attempts != maxAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", maxAttempts, attempts)
	}
}

func TestEmbedEmptyInputReturnsNil(t *testing.T) {
	c := &Client{apiKey: "k", model: "voyage-3.5-lite", http: http.DefaultClient}
	vectors, err := c.Embed(nil, "document")
	if err != nil {
		t.Fatal(err)
	}
	if vectors != nil {
		t.Fatalf("expected nil vectors for empty input, got %v", vectors)
	}
}
