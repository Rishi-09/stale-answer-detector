package freshness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TavilySearchEndpoint is the standard Tavily search API endpoint.
const TavilySearchEndpoint = "https://api.tavily.com/search"

// TavilyRequest represents the payload sent to Tavily Search API.
type TavilyRequest struct {
	APIKey         string   `json:"api_key"`
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth"` // "basic" (1 credit) or "advanced" (2 credits)
	IncludeDomains []string `json:"include_domains,omitempty"`
	MaxResults     int      `json:"max_results"`
}

// TavilySearchResultItem represents a single search result from Tavily.
type TavilySearchResultItem struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Content       string  `json:"content"`
	Score         float64 `json:"score"`
	PublishedDate string  `json:"published_date,omitempty"`
}

// TavilyResponse represents the response body returned by Tavily.
type TavilyResponse struct {
	Query   string                   `json:"query"`
	Results []TavilySearchResultItem `json:"results"`
}

// TavilyClient provides live search and recorded replay search capabilities.
type TavilyClient struct {
	apiKey          string
	httpClient      *http.Client
	replayMode      bool
	replayMu        sync.RWMutex
	replayFixtures  map[string]TavilyResponse
	domainAllowlist map[string][]string
}

// NewTavilyClient constructs a Tavily client.
func NewTavilyClient(apiKey string, domainAllowlist map[string][]string, timeout time.Duration) *TavilyClient {
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	return &TavilyClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		replayMode:      false,
		replayFixtures:  make(map[string]TavilyResponse),
		domainAllowlist: domainAllowlist,
	}
}

// SetReplayMode toggles replay mode for offline deterministic tests.
func (c *TavilyClient) SetReplayMode(enabled bool) {
	c.replayMu.Lock()
	defer c.replayMu.Unlock()
	c.replayMode = enabled
}

// RegisterFixture stores a canned fixture keyed by query.
func (c *TavilyClient) RegisterFixture(queryKey string, resp TavilyResponse) {
	c.replayMu.Lock()
	defer c.replayMu.Unlock()
	c.replayFixtures[queryKey] = resp
}

// ConstructQuery safely generates the Tavily search query strictly from entity + attribute.
// NEVER includes prompt or raw answer text!
func ConstructQuery(claim Claim) string {
	entity := strings.TrimSpace(claim.Entity)
	attr := strings.TrimSpace(claim.Attribute)

	switch claim.Category {
	case CategoryPricing:
		return fmt.Sprintf("%s %s pricing cost official", entity, attr)
	case CategoryAPIVersion:
		return fmt.Sprintf("%s %s latest version release notes", entity, attr)
	case CategoryRegulation:
		return fmt.Sprintf("%s %s effective date current status", entity, attr)
	case CategoryCompany:
		return fmt.Sprintf("%s %s current", entity, attr)
	default:
		return fmt.Sprintf("%s %s current", entity, attr)
	}
}

// Search performs a web search via Tavily or serves from registered replay fixtures.
func (c *TavilyClient) Search(ctx context.Context, claim Claim, depth string) ([]Evidence, int, error) {
	query := ConstructQuery(claim)
	creditsSpent := 1
	if depth == "advanced" {
		creditsSpent = 2
	}

	domains := c.domainAllowlist[claim.Category]

	// Check replay mode
	c.replayMu.RLock()
	isReplay := c.replayMode
	if isReplay {
		queryKey := computeQueryHash(query, domains, depth)
		fixture, exists := c.replayFixtures[queryKey]
		if !exists {
			// Fallback: match by prefix
			for k, f := range c.replayFixtures {
				if strings.Contains(k, strings.ToLower(claim.Entity)) {
					fixture = f
					exists = true
					break
				}
			}
		}
		c.replayMu.RUnlock()

		if exists {
			return convertTavilyResults(fixture.Results, domains), creditsSpent, nil
		}
		// Return empty evidence if no fixture exists in replay mode
		return nil, creditsSpent, nil
	}
	c.replayMu.RUnlock()

	// Live mode check
	if c.apiKey == "" || c.apiKey == "env.TAVILY_API_KEY" {
		return nil, 0, errors.New("tavily API key is not configured")
	}

	tavilyReq := TavilyRequest{
		APIKey:         c.apiKey,
		Query:          query,
		SearchDepth:    depth,
		IncludeDomains: domains,
		MaxResults:     5,
	}

	bodyBytes, err := json.Marshal(tavilyReq)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal tavily request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TavilySearchEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create tavily HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("tavily search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("tavily API error HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp TavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, 0, fmt.Errorf("failed to decode tavily response: %w", err)
	}

	evidence := convertTavilyResults(tavilyResp.Results, domains)
	return evidence, creditsSpent, nil
}

func convertTavilyResults(results []TavilySearchResultItem, allowlist []string) []Evidence {
	evidence := make([]Evidence, 0, len(results))
	now := time.Now()

	for _, r := range results {
		authority := ComputeSourceAuthority(r.URL, r.PublishedDate, now, allowlist)
		evidence = append(evidence, Evidence{
			URL:         r.URL,
			Title:       r.Title,
			Snippet:     r.Content,
			Published:   r.PublishedDate,
			Authority:   authority,
			RetrievedAt: now,
		})
	}

	return evidence
}

func computeQueryHash(query string, domains []string, depth string) string {
	h := sha256.New()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(query))))
	h.Write([]byte(strings.Join(domains, ",")))
	h.Write([]byte(depth))
	return hex.EncodeToString(h.Sum(nil))
}

