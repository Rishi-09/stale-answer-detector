package freshness

import (
	"regexp"
	"strings"
	"sync"
)

// Tier 1 compiled regex patterns
var (
	oncePatterns sync.Once

	// Pricing patterns: currency symbols with units or pricing words
	rePricing *regexp.Regexp

	// API version patterns: semantic versions, deprecation, release numbers
	reAPIVersion *regexp.Regexp

	// Regulation patterns: named laws, directives, compliance standards
	reRegulation *regexp.Regexp

	// Company patterns: leadership, mergers, founding, headquarters
	reCompany *regexp.Regexp

	// Temporal recency markers that elevate staleness likelihood
	reRecencyCues *regexp.Regexp
)

func initPatterns() {
	oncePatterns.Do(func() {
		// Pricing: e.g. "$5.00 per million", "$0.002 / 1k", "costs $20/month", "€15 per user"
		rePricing = regexp.MustCompile(`(?i)(?:[\$€£¥]\s*\d+(?:\.\d+)?|\b\d+(?:\.\d+)?\s*(?:cents|usd|eur|gbp))\s*(?:per|\/)\s*(?:million|1m|m|token|1k|thousand|user|month|mo|year|yr|request|call)\b|(?:costs?|priced\s+at|price\s+of|fee\s+of)\s*[\$€£¥]\s*\d+(?:\.\d+)?`)

		// API Version: e.g. "version 3.12", "v2.4.0", "Python 3.11", "API v1", "deprecated in v4"
		reAPIVersion = regexp.MustCompile(`(?i)\b(?:v\d+(?:\.\d+)+(?:-[a-z0-9.]+)?|version\s+\d+(?:\.\d+)+|release\s+\d+(?:\.\d+)+|(?:api|sdk|library|cli)\s+v\d+(?:\.\d+)*|(?:python|node|go|java|ruby|rust)\s+\d+(?:\.\d+)+|(?:deprecated|removed|introduced)\s+(?:in|since)\s+v?\d+(?:\.\d+)*)\b`)

		// Regulation: e.g. "EU AI Act", "GDPR", "CCPA", "HIPAA", "Digital Markets Act", "Executive Order 14110"
		reRegulation = regexp.MustCompile(`(?i)\b(?:gdpr|ccpa|cpra|hipaa|dma|dsa|eu\s+ai\s+act|ai\s+act|digital\s+markets\s+act|digital\s+services\s+act|executive\s+order\s+\d+|california\s+consumer\s+privacy|section\s+\d+[a-z]?|article\s+\d+)\b`)

		// Company: e.g. "CEO of", "acquired by", "founded in 2015", "headquartered in San Francisco", "valuation of $80B"
		reCompany = regexp.MustCompile(`(?i)\b(?:(?:ceo|cto|cfo|coo|chief\s+executive)\s+of\s+[A-Z][a-zA-Z0-9_\s&]+|acquired\s+by\s+[A-Z][a-zA-Z0-9_\s&]+|founded\s+in\s+(?:19|20)\d{2}|headquartered\s+in\s+[A-Z][a-zA-Z\s,]+|valuation\s+of\s+[\$€£¥]\s*\d+(?:\.\d+)?\s*(?:billion|b|million|m)?|merged\s+with\s+[A-Z][a-zA-Z0-9_\s&]+)\b`)

		// Recency cues: explicit temporality indicating claims vulnerable to staleness
		reRecencyCues = regexp.MustCompile(`(?i)\b(?:as\s+of\s+(?:today|\d{4}|[a-z]+\s+\d{4})|currently|latest\s+version|current\s+release|latest\s+model|as\s+of\s+now|at\s+present|recent\s+update)\b`)
	})
}

// DetectCandidates performs high-speed Tier 1 candidate matching.
// It returns nil with zero allocations if no candidate patterns match.
func DetectCandidates(text string, maxChars int, allowedCategories map[string]bool) []CandidateSpan {
	if len(text) == 0 {
		return nil
	}

	initPatterns()

	// Bound scanned text
	scanText := text
	if maxChars > 0 && len(scanText) > maxChars {
		scanText = scanText[:maxChars]
	}

	var candidates []CandidateSpan

	// Check Pricing
	if allowedCategories == nil || allowedCategories[CategoryPricing] {
		locs := rePricing.FindAllStringIndex(scanText, 8)
		for _, loc := range locs {
			candidates = append(candidates, CandidateSpan{
				Category: CategoryPricing,
				Start:    loc[0],
				End:      loc[1],
				Snippet:  scanText[loc[0]:loc[1]],
				Hint:     "pricing",
			})
		}
	}

	// Check API Version
	if allowedCategories == nil || allowedCategories[CategoryAPIVersion] {
		locs := reAPIVersion.FindAllStringIndex(scanText, 8)
		for _, loc := range locs {
			candidates = append(candidates, CandidateSpan{
				Category: CategoryAPIVersion,
				Start:    loc[0],
				End:      loc[1],
				Snippet:  scanText[loc[0]:loc[1]],
				Hint:     "api_version",
			})
		}
	}

	// Check Regulation
	if allowedCategories == nil || allowedCategories[CategoryRegulation] {
		locs := reRegulation.FindAllStringIndex(scanText, 8)
		for _, loc := range locs {
			candidates = append(candidates, CandidateSpan{
				Category: CategoryRegulation,
				Start:    loc[0],
				End:      loc[1],
				Snippet:  scanText[loc[0]:loc[1]],
				Hint:     "regulation",
			})
		}
	}

	// Check Company
	if allowedCategories == nil || allowedCategories[CategoryCompany] {
		locs := reCompany.FindAllStringIndex(scanText, 8)
		for _, loc := range locs {
			candidates = append(candidates, CandidateSpan{
				Category: CategoryCompany,
				Start:    loc[0],
				End:      loc[1],
				Snippet:  scanText[loc[0]:loc[1]],
				Hint:     "company",
			})
		}
	}

	// If recency cues are present, expand context around matches
	if len(candidates) > 0 && reRecencyCues.MatchString(scanText) {
		for i := range candidates {
			candidates[i].Hint += ";temporal_anchor"
		}
	}

	return candidates
}

// HasCandidates is an ultra-fast boolean pre-check for the hot path.
// It performs early exit without allocating slices.
func HasCandidates(text string, maxChars int) bool {
	if len(text) == 0 {
		return false
	}

	initPatterns()

	scanText := text
	if maxChars > 0 && len(scanText) > maxChars {
		scanText = scanText[:maxChars]
	}

	return rePricing.MatchString(scanText) ||
		reAPIVersion.MatchString(scanText) ||
		reRegulation.MatchString(scanText) ||
		reCompany.MatchString(scanText) ||
		reRecencyCues.MatchString(scanText)
}

// CleanText performs basic text sanitization and normalization.
func CleanText(s string) string {
	return strings.TrimSpace(s)
}
