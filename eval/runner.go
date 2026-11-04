package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/plugins/staledetector/freshness"
)

type TestCase struct {
	ID                 string `json:"id"`
	Category           string `json:"category"`
	Prompt             string `json:"prompt"`
	ModelAnswer        string `json:"model_answer"`
	Claim              string `json:"claim"`
	GroundTruthVerdict string `json:"ground_truth_verdict"`
	AsOfDate           string `json:"as_of_date"`
	SourceURL          string `json:"source_url"`
}

type CategoryMetrics struct {
	TotalCases        int
	TruePositives     int // Predicted stale & Ground truth stale
	FalsePositives    int // Predicted stale & Ground truth not stale
	FalseNegatives    int // Predicted not stale & Ground truth stale
	TrueNegatives     int
	CreditsSpent      int
	ZeroSearchCount   int
	Latencies         []float64
}

func main() {
	datasetPath := flag.String("dataset", "eval/dataset.jsonl", "Path to evaluation dataset")
	mode := flag.String("mode", "replay", "Execution mode: replay or live")
	split := flag.String("split", "test", "Split to report: test (odd cases), calib (even cases), or all")
	flag.Parse()

	file, err := os.Open(*datasetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening dataset: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	var allCases []TestCase
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var tc TestCase
		if err := json.Unmarshal([]byte(line), &tc); err == nil {
			allCases = append(allCases, tc)
		}
	}

	// Filter cases based on split
	var evalCases []TestCase
	for i, tc := range allCases {
		switch *split {
		case "test":
			if i%2 == 1 {
				evalCases = append(evalCases, tc)
			}
		case "calib":
			if i%2 == 0 {
				evalCases = append(evalCases, tc)
			}
		default:
			evalCases = append(evalCases, tc)
		}
	}

	cfg := freshness.DefaultConfig()
	tavilyClient := freshness.NewTavilyClient(cfg.TavilyAPIKey, cfg.DomainAllowlist, 3*time.Second)
	tavilyClient.SetReplayMode(*mode == "replay")

	// Seed replay fixtures for replay evaluation
	seedReplayFixtures(tavilyClient)

	engine := freshness.NewEngine(cfg, freshness.NewLocalExtractor(), tavilyClient)

	catMetrics := map[string]*CategoryMetrics{
		freshness.CategoryPricing:    {},
		freshness.CategoryAPIVersion: {},
		freshness.CategoryRegulation: {},
		freshness.CategoryCompany:    {},
	}

	var allLatencies []float64
	totalCredits := 0
	totalZeroSearches := 0

	for _, tc := range evalCases {
		metrics, ok := catMetrics[tc.Category]
		if !ok {
			metrics = &CategoryMetrics{}
			catMetrics[tc.Category] = metrics
		}
		metrics.TotalCases++

		start := time.Now()
		rec, err := engine.Process(context.Background(), tc.ID, "openai/gpt-4o", tc.ModelAnswer)
		elapsed := time.Since(start).Seconds() * 1000.0 // ms

		metrics.Latencies = append(metrics.Latencies, elapsed)
		allLatencies = append(allLatencies, elapsed)

		predictedVerdict := freshness.VerdictUnverifiable
		if err == nil && rec != nil && len(rec.Claims) > 0 {
			predictedVerdict = rec.Claims[0].Verdict
		}

		if rec != nil {
			metrics.CreditsSpent += rec.Stats.CreditsSpent
			totalCredits += rec.Stats.CreditsSpent
			if rec.Stats.Searches == 0 {
				metrics.ZeroSearchCount++
				totalZeroSearches++
			}
		}

		// Confusion matrix for stale verdict
		isGTStale := tc.GroundTruthVerdict == freshness.VerdictStale
		isPredStale := predictedVerdict == freshness.VerdictStale

		if isGTStale && isPredStale {
			metrics.TruePositives++
		} else if !isGTStale && isPredStale {
			metrics.FalsePositives++
		} else if isGTStale && !isPredStale {
			metrics.FalseNegatives++
		} else {
			metrics.TrueNegatives++
		}
	}

	// Output evaluation results table
	fmt.Printf("# Evaluation Benchmark Results (Split: %s, Mode: %s)\n\n", *split, *mode)
	fmt.Println("| Category | Cases | Precision | Recall | F1 Score | Credits/Req | Zero-Search % | p50 Latency | p95 Latency |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |")

	var totalTP, totalFP, totalFN, totalTN int

	categories := []string{
		freshness.CategoryPricing,
		freshness.CategoryAPIVersion,
		freshness.CategoryRegulation,
		freshness.CategoryCompany,
	}

	for _, cat := range categories {
		m := catMetrics[cat]
		if m.TotalCases == 0 {
			continue
		}

		prec := 0.0
		if m.TruePositives+m.FalsePositives > 0 {
			prec = float64(m.TruePositives) / float64(m.TruePositives+m.FalsePositives)
		}
		rec := 0.0
		if m.TruePositives+m.FalseNegatives > 0 {
			rec = float64(m.TruePositives) / float64(m.TruePositives+m.FalseNegatives)
		}
		f1 := 0.0
		if prec+rec > 0 {
			f1 = (2 * prec * rec) / (prec + rec)
		}

		p50, p95 := calculatePercentiles(m.Latencies)
		creditsPerReq := float64(m.CreditsSpent) / float64(m.TotalCases)
		zeroSearchPct := (float64(m.ZeroSearchCount) / float64(m.TotalCases)) * 100.0

		fmt.Printf("| **%s** | %d | %.2f | %.2f | **%.2f** | %.2f | %.1f%% | %.1f ms | %.1f ms |\n",
			cat, m.TotalCases, prec, rec, f1, creditsPerReq, zeroSearchPct, p50, p95)

		totalTP += m.TruePositives
		totalFP += m.FalsePositives
		totalFN += m.FalseNegatives
		totalTN += m.TrueNegatives
	}

	// Overall Row
	overallPrec := 0.0
	if totalTP+totalFP > 0 {
		overallPrec = float64(totalTP) / float64(totalTP+totalFP)
	}
	overallRec := 0.0
	if totalTP+totalFN > 0 {
		overallRec = float64(totalTP) / float64(totalTP+totalFN)
	}
	overallF1 := 0.0
	if overallPrec+overallRec > 0 {
		overallF1 = (2 * overallPrec * overallRec) / (overallPrec + overallRec)
	}

	overallP50, overallP95 := calculatePercentiles(allLatencies)
	overallCredits := float64(totalCredits) / float64(len(evalCases))
	overallZeroSearch := (float64(totalZeroSearches) / float64(len(evalCases))) * 100.0

	fmt.Printf("| **Overall** | **%d** | **%.2f** | **%.2f** | **%.2f** | **%.2f** | **%.1f%%** | **%.1f ms** | **%.1f ms** |\n\n",
		len(evalCases), overallPrec, overallRec, overallF1, overallCredits, overallZeroSearch, overallP50, overallP95)
}

func calculatePercentiles(latencies []float64) (p50, p95 float64) {
	if len(latencies) == 0 {
		return 0, 0
	}
	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)
	sort.Float64s(sorted)

	idx50 := int(math.Floor(0.50 * float64(len(sorted))))
	idx95 := int(math.Floor(0.95 * float64(len(sorted))))

	if idx50 >= len(sorted) {
		idx50 = len(sorted) - 1
	}
	if idx95 >= len(sorted) {
		idx95 = len(sorted) - 1
	}

	return sorted[idx50], sorted[idx95]
}

func seedReplayFixtures(c *freshness.TavilyClient) {
	// Seed realistic fixtures for offline deterministic runs
	c.RegisterFixture("gpt-4o", freshness.TavilyResponse{
		Results: []freshness.TavilySearchResultItem{
			{
				Title:         "OpenAI API Pricing Updates",
				URL:           "https://openai.com/pricing",
				Content:       "GPT-4o standard input is priced at $2.50 per million tokens and $10.00 output.",
				PublishedDate: "2024-08-01",
			},
		},
	})
	c.RegisterFixture("python", freshness.TavilyResponse{
		Results: []freshness.TavilySearchResultItem{
			{
				Title:         "Python 3.12 Release Notes",
				URL:           "https://www.python.org/downloads",
				Content:       "Python 3.12 is the current stable release. Python 3.9 is legacy.",
				PublishedDate: "2024-02-01",
			},
		},
	})
	c.RegisterFixture("twitter", freshness.TavilyResponse{
		Results: []freshness.TavilySearchResultItem{
			{
				Title:         "X Corporate Leadership",
				URL:           "https://www.reuters.com",
				Content:       "Linda Yaccarino is the CEO of X (formerly Twitter). Parag Agrawal departed in 2022.",
				PublishedDate: "2024-01-01",
			},
		},
	})
	c.RegisterFixture("amazon", freshness.TavilyResponse{
		Results: []freshness.TavilySearchResultItem{
			{
				Title:         "Amazon Leadership Changes",
				URL:           "https://www.aboutamazon.com",
				Content:       "Andy Jassy succeeded Jeff Bezos as CEO of Amazon in July 2021.",
				PublishedDate: "2024-01-01",
			},
		},
	})
}

