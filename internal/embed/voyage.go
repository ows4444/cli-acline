// Package embed calls Voyage AI's embeddings API to power acline's optional
// semantic search (see internal/store/embedding.go and `acline search
// --semantic`). acline is local-first by default: this package's client is
// only ever constructed when VOYAGE_API_KEY is set, so a plain `acline` with
// no key configured never makes a network call and never imports anything
// from this package in practice — FromEnv is the only entry point, and it
// simply returns ok=false when no key is present.
package embed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// defaultModel is Voyage's cheapest current tier, plenty for the short
// memory/decision/spec/note bodies acline indexes — not a large-document or
// code-search workload.
const defaultModel = "voyage-3.5-lite"

const apiURL = "https://api.voyageai.com/v1/embeddings"

// maxBatchSize caps how many texts go into one request. Voyage's documented
// per-request limit is 1000 texts (and a total-token ceiling acline's short
// memory/decision/spec/note bodies are in no danger of individually
// approaching) -- capped well under that so one oversized `reindex-embeddings`
// batch can't trip either limit and fail the whole run.
const maxBatchSize = 128

// maxAttempts/retryBaseDelay govern retrying a single chunk on a transient
// failure (a network error, or a 429/5xx response) -- a flaky connection or
// a momentary rate limit previously failed the whole reindex batch outright.
// A 4xx other than 429 is not retried: it means
// the request itself is wrong (bad key, bad model) and retrying it wastes
// time without any chance of succeeding.
const maxAttempts = 4

var retryBaseDelay = 250 * time.Millisecond

// Client calls Voyage AI's embeddings endpoint over plain net/http, no SDK
// dependency, matching the rest of acline's minimal-dependency footprint.
type Client struct {
	apiKey  string
	model   string
	http    *http.Client
	baseURL string              // defaults to apiURL; overridable so tests can point at an httptest server
	sleep   func(time.Duration) // defaults to time.Sleep; overridable so tests don't wait out real backoff delays
}

// FromEnv builds a Client from VOYAGE_API_KEY (required) and
// ACLINE_EMBED_MODEL (optional). It returns ok=false, not an error, when no
// key is set — every caller treats that as "semantic search disabled" and
// falls back to keyword-only search, not a failure.
func FromEnv() (*Client, bool) {
	key := os.Getenv("VOYAGE_API_KEY")
	if key == "" {
		return nil, false
	}
	model := os.Getenv("ACLINE_EMBED_MODEL")
	if model == "" {
		model = defaultModel
	}
	return &Client{apiKey: key, model: model, http: &http.Client{Timeout: 20 * time.Second}, baseURL: apiURL, sleep: time.Sleep}, true
}

// Model is the embedding model in use — stored alongside every vector so a
// later model change never mixes incomparable vectors in one similarity
// scan (see internal/store's RowsMissingEmbeddings).
func (c *Client) Model() string { return c.model }

type embedRequest struct {
	Input     []string `json:"input"`
	Model     string   `json:"model"`
	InputType string   `json:"input_type,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Detail string `json:"detail"`
}

// Embed returns one vector per text in texts, in the same order. inputType
// should be "document" when indexing stored content and "query" when
// embedding a search query — Voyage's embeddings are asymmetric, and
// tagging each side correctly measurably improves retrieval quality over
// leaving it blank.
//
// texts is chunked into batches of at most maxBatchSize before sending, and
// each chunk is retried independently (see maxAttempts) -- a failure on one
// chunk doesn't waste the vectors already fetched for earlier chunks, since
// callers (e.g. a large `reindex-embeddings` run) can persist per-row as
// they go rather than needing one all-or-nothing call to succeed.
func (c *Client) Embed(texts []string, inputType string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		vectors, err := c.embedBatchWithRetry(texts[start:end], inputType)
		if err != nil {
			return nil, fmt.Errorf("embedding texts[%d:%d]: %w", start, end, err)
		}
		out = append(out, vectors...)
	}
	return out, nil
}

// embedBatchWithRetry sends one batch (already within maxBatchSize),
// retrying up to maxAttempts times on a transient failure: a network-level
// error from c.http.Do, or a 429/5xx response. Any other response (a
// non-retryable 4xx, or a successful-but-malformed body) returns
// immediately on the first attempt.
func (c *Client) embedBatchWithRetry(texts []string, inputType string) ([][]float32, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		vectors, retryable, err := c.embedBatchOnce(texts, inputType)
		if err == nil {
			return vectors, nil
		}
		lastErr = err
		if !retryable || attempt == maxAttempts {
			break
		}
		sleep := c.sleep
		if sleep == nil {
			sleep = time.Sleep
		}
		sleep(retryBaseDelay * time.Duration(1<<uint(attempt-1))) // 250ms, 500ms, 1s, ...
	}
	return nil, lastErr
}

// embedBatchOnce makes a single HTTP attempt and reports whether the
// failure (if any) is worth retrying.
func (c *Client) embedBatchOnce(texts []string, inputType string) (vectors [][]float32, retryable bool, err error) {
	reqBody, err := json.Marshal(embedRequest{Input: texts, Model: c.model, InputType: inputType})
	if err != nil {
		return nil, false, err
	}
	base := c.baseURL
	if base == "" {
		base = apiURL
	}
	req, err := http.NewRequest(http.MethodPost, base, bytes.NewReader(reqBody))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		// A network-level failure (connection reset, timeout, DNS hiccup)
		// is exactly the transient case worth retrying.
		return nil, true, fmt.Errorf("voyage embeddings request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("reading voyage response: %w", err)
	}

	var parsed embedResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		return nil, false, fmt.Errorf("parsing voyage response (status %d): %w", resp.StatusCode, jsonErr)
	}
	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if parsed.Detail != "" {
			return nil, retryable, fmt.Errorf("voyage embeddings: %s (status %d)", parsed.Detail, resp.StatusCode)
		}
		return nil, retryable, fmt.Errorf("voyage embeddings: status %d", resp.StatusCode)
	}
	if len(parsed.Data) != len(texts) {
		return nil, false, fmt.Errorf("voyage embeddings: expected %d vectors, got %d", len(texts), len(parsed.Data))
	}
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	return out, false, nil
}
