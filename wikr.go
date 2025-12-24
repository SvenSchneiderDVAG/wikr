package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/fatih/color"
)

const (
	wikipediaAPITemplate        = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
	wikipediaSearchAPITemplate  = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"
	grokipediaPageAPITemplate   = "https://grokipedia.com/page/%s"
	grokipediaSearchAPITemplate = "https://grokipedia.com/search?q=%s"
	cacheDuration               = 24 * time.Hour
	version                     = "0.7.0"
)

// debug is a runtime variable (was const) so tests can toggle it to cover debug print branches.
var debug = false

var (
	userAgent = "wikr/" + version + " (+https://github.com/SvenSchneiderDVAG/wikr)"
	// httpGetFunc allows tests to inject a mock for network calls.
	httpGetFunc = httpGet
)

type CacheEntry struct {
	Summary   string    `json:"summary"`
	URL       string    `json:"url"`
	Timestamp time.Time `json:"timestamp"`
}

// SearchResultEntry caches a list of titles for a given (lang, query) pair.
type SearchResultEntry struct {
	Titles    []string  `json:"titles"`
	Timestamp time.Time `json:"timestamp"`
}

type Cache map[string]CacheEntry

func getCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not find cache directory: %w", err)
	}
	wikrCacheDir := filepath.Join(cacheDir, "wikr")
	if err := os.MkdirAll(wikrCacheDir, 0755); err != nil {
		return "", fmt.Errorf("could not create cache directory: %w", err)
	}
	return filepath.Join(wikrCacheDir, "cache.json"), nil
}

func loadCache() Cache {
	cache := make(Cache)
	cachePath, err := getCachePath()
	if err != nil {
		if debug {
			fmt.Printf("Error getting cache path: %v\n", err)
		}
		return cache
	}

	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return cache // No cache file yet, return empty cache
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		if debug {
			fmt.Printf("Error reading cache file %s: %v\n", cachePath, err)
		}
		return cache
	}
	err = json.Unmarshal(data, &cache)
	if err != nil && debug {
		fmt.Printf("Error decoding cache: %v\n", err)
	}
	return cache
}

func saveCache(cache Cache) {
	cachePath, err := getCachePath()
	if err != nil {
		if debug {
			fmt.Printf("Error getting cache path for saving: %v\n", err)
		}
		return
	}
	data, err := json.Marshal(cache)
	if err != nil && debug {
		fmt.Printf("Error encoding cache: %v\n", err)
		return
	}
	err = os.WriteFile(cachePath, data, 0644)
	if err != nil && debug {
		fmt.Printf("Error writing cache file %s: %v\n", cachePath, err)
	}
}

func getSearchCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not find cache directory: %w", err)
	}
	wikrCacheDir := filepath.Join(cacheDir, "wikr")
	if err := os.MkdirAll(wikrCacheDir, 0755); err != nil {
		return "", fmt.Errorf("could not create cache directory: %w", err)
	}
	return filepath.Join(wikrCacheDir, "search_cache.json"), nil
}

func loadSearchCache() map[string]SearchResultEntry {
	cache := make(map[string]SearchResultEntry)
	path, err := getSearchCachePath()
	if err != nil {
		if debug {
			fmt.Printf("Error search cache path: %v\n", err)
		}
		return cache
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if debug {
			fmt.Printf("Error reading search cache: %v\n", err)
		}
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		if debug {
			fmt.Printf("Error decoding search cache: %v\n", err)
		}
	}
	return cache
}

func saveSearchCache(c map[string]SearchResultEntry) {
	path, err := getSearchCachePath()
	if err != nil {
		if debug {
			fmt.Printf("Error getting search cache path for saving: %v\n", err)
		}
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		if debug {
			fmt.Printf("Error encoding search cache: %v\n", err)
		}
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		if debug {
			fmt.Printf("Error writing search cache: %v\n", err)
		}
	}
}

func getCachedSearch(lang, escapedQuery string) ([]string, bool) {
	c := loadSearchCache()
	key := lang + ":" + escapedQuery
	entry, ok := c[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.Timestamp) >= cacheDuration {
		return nil, false
	}
	return entry.Titles, true
}

func setCachedSearch(lang, escapedQuery string, titles []string) {
	c := loadSearchCache()
	key := lang + ":" + escapedQuery
	c[key] = SearchResultEntry{Titles: titles, Timestamp: time.Now()}
	saveSearchCache(c)
}

func getCachedEntry(lang, title string) (string, string, bool) {
	cache := loadCache()
	key := lang + ":" + title
	if debug {
		fmt.Printf("\nSearch for cache entry for key: %s\n", key)
	}
	entry, exists := cache[key]
	if exists {
		if debug {
			fmt.Printf("Cache entry found, age: %v\n", time.Since(entry.Timestamp))
		}
		if time.Since(entry.Timestamp) < cacheDuration {
			return entry.Summary, entry.URL, true
		}
	}
	return "", "", false
}

func setCachedEntry(lang, title, summary, url string) {
	cache := loadCache()
	key := lang + ":" + title
	cache[key] = CacheEntry{
		Summary:   summary,
		URL:       url,
		Timestamp: time.Now(),
	}
	if debug {
		fmt.Printf("Save cache entry for key: %s\n", key)
	}
	saveCache(cache)
}

type Config struct {
	Language   string `json:"language"`
	MaxResults int    `json:"max_results"`
	Source     string `json:"source"`
}

func getConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not find config directory: %w", err)
	}
	wikrConfigDir := filepath.Join(configDir, "wikr")
	if err := os.MkdirAll(wikrConfigDir, 0755); err != nil {
		return "", fmt.Errorf("could not create config directory: %w", err)
	}
	return filepath.Join(wikrConfigDir, "config.json"), nil
}

func loadConfig() (Config, bool, error) {
	corrected := false
	config := Config{
		Language:   "en",
		MaxResults: 5,
		Source:     "wikipedia",
	}
	configPath, err := getConfigPath()
	if err != nil {
		return config, false, err
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create a default config file if it doesn't exist
		if err := saveConfig(config); err != nil {
			return config, false, fmt.Errorf("could not create default config: %w", err)
		}
		return config, false, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config, false, fmt.Errorf("error reading config file %s: %w", configPath, err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, false, fmt.Errorf("error decoding config: %w", err)
	}

	// Validation: only allow "en" or "de" currently; fallback to en
	if config.Language != "en" && config.Language != "de" {
		config.Language = "en"
		corrected = true
	}
	if config.MaxResults <= 0 {
		config.MaxResults = 5
		corrected = true
	}
	if config.Source != "wikipedia" && config.Source != "grokipedia" {
		config.Source = "wikipedia"
		corrected = true
	}

	return config, corrected, nil
}

func saveConfig(config Config) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("error encoding config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("error writing config file %s: %w", configPath, err)
	}
	return nil
}

// httpGet performs an HTTP GET with retries and returns body, status, contentType.
// Retries on network errors and selected transient HTTP statuses (429, 500-503).
func httpGet(endpoint string) ([]byte, int, string, error) {
	var lastErr error
	backoff := 150 * time.Millisecond
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, 0, "", fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("perform request: %w", err)
		} else {
			ct := resp.Header.Get("Content-Type")
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				return nil, resp.StatusCode, ct, fmt.Errorf("read body: %w", rerr)
			}
			// Retry on transient HTTP codes
			if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode <= 503) {
				lastErr = fmt.Errorf("transient HTTP status %d", resp.StatusCode)
			} else {
				return body, resp.StatusCode, ct, nil
			}
		}
		if attempt < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return nil, 0, "", lastErr
}

// searchWikipedia queries the MediaWiki search API and returns a list of page titles.
func searchWikipedia(lang, escapedQuery string) ([]string, bool, error) {
	if titles, ok := getCachedSearch(lang, escapedQuery); ok {
		return titles, true, nil
	}
	endpoint := fmt.Sprintf(wikipediaSearchAPITemplate, lang, escapedQuery)
	body, status, ct, err := httpGetFunc(endpoint)
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("unexpected status %d from search endpoint", status)
	}
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		if titles, ok := getCachedSearch(lang, escapedQuery); ok {
			if debug {
				fmt.Printf("Non-JSON Content-Type '%s' for search; using cache fallback.\n", ct)
			}
			return titles, true, nil
		}
		return nil, false, fmt.Errorf("unexpected content-type '%s' (expected application/json)", ct)
	}
	var payload struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		if titles, ok := getCachedSearch(lang, escapedQuery); ok {
			if debug {
				fmt.Printf("Malformed JSON for search; using cache fallback: %v\n", err)
			}
			return titles, true, nil
		}
		return nil, false, fmt.Errorf("decode search JSON: %w", err)
	}
	titles := make([]string, 0, len(payload.Query.Search))
	for _, s := range payload.Query.Search {
		if s.Title != "" {
			titles = append(titles, s.Title)
		}
	}
	if len(titles) > 0 {
		setCachedSearch(lang, escapedQuery, titles)
	}
	return titles, false, nil
}

// chooseResult lets the user pick one of the returned titles when more than one
// result is available.
func chooseResult(results []string, maxResults *int, lang string) string {
	limit := *maxResults
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}
	fmt.Println()
	if lang == "de" {
		color.Cyan("Mehrere Ergebnisse gefunden (zeige %d von %d):", limit, len(results))
	} else {
		color.Cyan("Multiple results found (showing %d of %d):", limit, len(results))
	}
	for i := 0; i < limit; i++ {
		fmt.Printf("  [%d] %s\n", i+1, results[i])
	}
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		if lang == "de" {
			fmt.Print("Bitte eine Nummer auswählen (Standard 1): ")
		} else {
			fmt.Print("Select a result number (default 1): ")
		}
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return results[0]
		}
		// Attempt to parse numeric selection.
		var idx int
		_, err := fmt.Sscanf(line, "%d", &idx)
		if err == nil && idx >= 1 && idx <= limit {
			return results[idx-1]
		}
		if lang == "de" {
			color.Yellow("Ungültige Auswahl. Bitte eine Zahl zwischen 1 und %d eingeben.", limit)
		} else {
			color.Yellow("Invalid selection. Please enter a number between 1 and %d.", limit)
		}
	}
}

func getWikipediaSummary(lang, title string) (string, string, bool, error) {
	if summary, urlStr, found := getCachedEntry(lang, title); found {
		return summary, urlStr, true, nil
	}
	endpoint := fmt.Sprintf(wikipediaAPITemplate, lang) + url.PathEscape(title)
	body, status, ct, err := httpGetFunc(endpoint)
	if err != nil {
		return "", "", false, err
	}
	if status != http.StatusOK {
		return "", "", false, fmt.Errorf("unexpected status %d from summary endpoint", status)
	}
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return "", "", false, fmt.Errorf("unexpected content-type '%s'", ct)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", false, fmt.Errorf("decode summary JSON: %w", err)
	}
	extractVal, ok := result["extract"]
	if !ok {
		return "", "", false, errors.New("missing 'extract' in response")
	}
	extractStr, ok := extractVal.(string)
	if !ok {
		return "", "", false, errors.New("'extract' field not a string")
	}
	contentURLs, ok := result["content_urls"].(map[string]any)
	if !ok {
		return "", "", false, errors.New("missing content_urls")
	}
	desktop, ok := contentURLs["desktop"].(map[string]any)
	if !ok {
		return "", "", false, errors.New("missing desktop in content_urls")
	}
	pageURL, ok := desktop["page"].(string)
	if !ok {
		return "", "", false, errors.New("missing page URL")
	}
	if len(extractStr) > 1000 {
		extractStr = extractStr[:997] + "..."
	}
	setCachedEntry(lang, title, extractStr, pageURL)
	return extractStr, pageURL, false, nil
}

func slugifyGrokipedia(title string) string {
	// Preserve original casing from search results, just normalize spaces to underscores
	cleaned := strings.TrimSpace(title)
	// Replace multiple spaces with single space, then spaces with underscores
	parts := strings.Fields(cleaned)
	return strings.Join(parts, "_")
}

// extractMetaDescription extracts the content attribute from <meta name="description"> tag
func extractMetaDescription(htmlContent string) string {
	// Match <meta name="description" content="..."/>
	re := regexp.MustCompile(`<meta\s+name="description"\s+content="([^"]*)"`)
	matches := re.FindStringSubmatch(htmlContent)
	if len(matches) >= 2 {
		// Decode HTML entities like &#x27; -> '
		return html.UnescapeString(matches[1])
	}
	return ""
}

// extractOGTitle extracts the content attribute from <meta property="og:title"> tag
func extractOGTitle(htmlContent string) string {
	re := regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]*)"`)
	matches := re.FindStringSubmatch(htmlContent)
	if len(matches) >= 2 {
		return html.UnescapeString(matches[1])
	}
	return ""
}

func getGrokipediaSummary(title string) (string, string, bool, error) {
	slug := slugifyGrokipedia(title)
	if slug == "" {
		return "", "", false, errors.New("empty title")
	}
	if summary, urlStr, found := getCachedEntry("grokipedia", slug); found {
		return summary, urlStr, true, nil
	}
	endpoint := fmt.Sprintf(grokipediaPageAPITemplate, url.PathEscape(slug))

	// Simple HTTP GET - Grokipedia pages are server-side rendered with meta tags
	body, status, _, err := httpGetFunc(endpoint)
	if err != nil {
		if debug {
			fmt.Printf("HTTP error fetching Grokipedia page: %v\n", err)
		}
		return "", "", false, err
	}

	// Check for 404 status
	if status == http.StatusNotFound {
		return "", "", false, fmt.Errorf("article '%s' does not exist on Grokipedia yet", title)
	}

	if status != http.StatusOK {
		return "", "", false, fmt.Errorf("unexpected status %d from Grokipedia", status)
	}

	// Extract summary from meta description tag
	htmlContent := string(body)
	summary := extractMetaDescription(htmlContent)

	if summary == "" {
		return "", "", false, fmt.Errorf("article '%s' does not exist on Grokipedia yet", title)
	}

	// Truncate if too long
	if len(summary) > 1000 {
		summary = summary[:997] + "..."
	}

	setCachedEntry("grokipedia", slug, summary, endpoint)
	return summary, endpoint, false, nil
}

// searchGrokipediaChromedp is the variable holding the chromedp search function.
// It can be overridden in tests to avoid launching a real browser.
var searchGrokipediaChromedp func(query string, escapedQuery string) ([]string, error) = searchGrokipediaChromedpImpl

// searchGrokipediaChromedpImpl uses chromedp to search Grokipedia via browser automation.
// Grokipedia's search is JS-rendered, so we need a headless browser to interact with it.
func searchGrokipediaChromedpImpl(query string, escapedQuery string) ([]string, error) {
	endpoint := fmt.Sprintf(grokipediaSearchAPITemplate, escapedQuery)

	// Set up chromedp with proper headless options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	// Create context with timeout
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	titles := make([]string, 0)

	// Navigate and wait for page to load, then extract search results
	err := chromedp.Run(ctx,
		chromedp.Navigate(endpoint),
		chromedp.WaitReady("body"),
		// Wait for search results to appear (up to 10s), or fall through if timeout
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Try to wait for results text pattern, with a shorter timeout
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			// Poll until we see "yielded" text or results appear
			for {
				select {
				case <-waitCtx.Done():
					return nil // Timeout is OK, continue with what we have
				default:
					var hasResults bool
					chromedp.Evaluate(`document.body.innerText.includes('yielded') || document.querySelectorAll('[class*="result"]').length > 0`, &hasResults).Do(ctx)
					if hasResults {
						return nil
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
		}),
		chromedp.Evaluate(`
			(function() {
				var titles = [];
				var bodyText = document.body.innerText;

				// Look for the search results pattern: "yielded X results:" followed by result titles
				var resultsMatch = bodyText.match(/yielded\s+[\d.,]+\s+results?:\s*([\s\S]*?)(?:‹\s*Previous|Next\s*›|$)/i);
				if (resultsMatch && resultsMatch[1]) {
					// Split by newlines and extract titles
					var lines = resultsMatch[1].split('\n');
					for (var i = 0; i < lines.length; i++) {
						var line = lines[i].trim();
						// Filter: must have content, not be navigation, not be page numbers
						if (line &&
						    line.length > 1 &&
						    line.length < 200 &&
						    !/^\d+$/.test(line) &&
						    !/^\.+$/.test(line) &&
						    line !== 'Previous' &&
						    line !== 'Next' &&
						    !titles.includes(line)) {
							titles.push(line);
						}
					}
				}

				// Fallback: try to find clickable result elements
				if (titles.length === 0) {
					// Look for any elements that might be search results
					var resultEls = document.querySelectorAll('[class*="result"], [class*="item"], [class*="title"]');
					resultEls.forEach(function(el) {
						var text = el.textContent.trim();
						if (text && text.length > 1 && text.length < 200 && !titles.includes(text)) {
							titles.push(text);
						}
					});
				}

				return titles;
			})()
		`, &titles),
	)

	if debug {
		fmt.Printf("Grokipedia search found %d titles: %v\n", len(titles), titles)
	}
	if err != nil {
		if debug {
			fmt.Printf("chromedp error: %v\n", err)
		}
		return nil, err
	}

	return titles, nil
}

// searchGrokipedia searches Grokipedia using chromedp for browser automation.
// Results are cached to avoid repeated browser launches.
func searchGrokipedia(query string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(query)
	escapedQuery := url.QueryEscape(trimmed)
	if titles, ok := getCachedSearch("grokipedia", escapedQuery); ok {
		return titles, true, nil
	}

	if trimmed == "" {
		return nil, false, errors.New("empty search query")
	}

	if debug {
		fmt.Printf("Launching chromedp search for: %s\n", trimmed)
	}

	titles, err := searchGrokipediaChromedp(trimmed, escapedQuery)
	if err != nil {
		if debug {
			fmt.Printf("chromedp search error: %v\n", err)
		}
		// Fallback: try direct page access as before
		return searchGrokipediaDirectFallback(trimmed, escapedQuery)
	}

	if len(titles) == 0 {
		if debug {
			fmt.Printf("chromedp found no results, trying direct fallback\n")
		}
		// Fallback to direct access
		return searchGrokipediaDirectFallback(trimmed, escapedQuery)
	}

	// Cache the results
	setCachedSearch("grokipedia", escapedQuery, titles)
	if debug {
		fmt.Printf("Grokipedia chromedp search found: %v\n", titles)
	}
	return titles, false, nil
}

// searchGrokipediaDirectFallback tries to find a Grokipedia page by directly accessing it.
// This is used as a fallback when chromedp search doesn't find results.
func searchGrokipediaDirectFallback(query, escapedQuery string) ([]string, bool, error) {
	slug := slugifyGrokipedia(query)
	if slug == "" {
		return nil, false, nil
	}

	endpoint := fmt.Sprintf(grokipediaPageAPITemplate, url.PathEscape(slug))
	body, status, _, err := httpGetFunc(endpoint)
	if err != nil {
		if debug {
			fmt.Printf("HTTP error in direct fallback: %v\n", err)
		}
		return nil, false, err
	}

	if status == http.StatusOK {
		htmlContent := string(body)
		pageTitle := extractOGTitle(htmlContent)
		if pageTitle == "" || pageTitle == "Grokipedia" {
			pageTitle = strings.ReplaceAll(slug, "_", " ")
		}
		titles := []string{pageTitle}
		setCachedSearch("grokipedia", escapedQuery, titles)
		if debug {
			fmt.Printf("Grokipedia direct fallback found: %v\n", titles)
		}
		return titles, false, nil
	}

	if debug {
		fmt.Printf("Grokipedia search for '%s' found no results\n", query)
	}
	return nil, false, nil
}

func clearCache() error {
	cachePath, err := getCachePath()
	if err != nil {
		return fmt.Errorf("error getting cache path: %w", err)
	}
	err = os.Remove(cachePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error deleting cache file: %v", err)
	}
	if debug {
		fmt.Println("Cache was deleted successfully.")
	}
	return nil
}

// run encapsulates the CLI logic; args should exclude the program name (like os.Args[1:]).
// It writes user-facing output to out and returns an exit code (0 success, >0 failure).
func run(out io.Writer, args []string) int {
	// Ensure color output goes to out if it's stdout; we keep using global color functions.
	config, corrected, err := loadConfig()
	if err != nil {
		fmt.Fprintf(out, "Warning: could not load config: %v\n", err)
		config = Config{Language: "en", MaxResults: 5}
	} else if corrected {
		if err := saveConfig(config); err != nil && debug {
			fmt.Fprintf(out, "Could not persist corrected config: %v\n", err)
		}
	}

	fs := flag.NewFlagSet("wikr", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage: wikr [options] <search term>\n\n")
		fmt.Fprintf(out, "Options:\n")
		fs.PrintDefaults()
	}

	lang := fs.String("lang", config.Language, "language of the Wikipedia to use (en|de)")
	source := fs.String("source", config.Source, "content source (wikipedia|grokipedia)")
	maxResults := fs.Int("max", config.MaxResults, "maximum amount of result entries")
	isClearCache := fs.Bool("clear-cache", false, "clear the cache")
	isVersion := fs.Bool("version", false, "show version")
	isResetConfig := fs.Bool("reset-config", false, "regenerate default configuration and exit")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if *isResetConfig {
		defaultCfg := Config{Language: "en", MaxResults: 5, Source: "wikipedia"}
		if err := saveConfig(defaultCfg); err != nil {
			fmt.Fprintf(out, "Error writing default config: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, "Configuration reset to defaults (language=en, max_results=5, source=wikipedia)")
		return 0
	}

	src := strings.ToLower(strings.TrimSpace(*source))
	if src == "" {
		src = "wikipedia"
	}
	if src != "wikipedia" && src != "grokipedia" {
		fmt.Fprintf(out, "Invalid source '%s'. Allowed: wikipedia, grokipedia.\n", src)
		return 2
	}
	*source = src

	// Update config if flags are set
	configChanged := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "lang":
			if *lang != config.Language {
				config.Language = *lang
				configChanged = true
			}
		case "max":
			if *maxResults != config.MaxResults {
				config.MaxResults = *maxResults
				configChanged = true
			}
		case "source":
			if *source != config.Source {
				config.Source = *source
				configChanged = true
			}
		}
	})
	if configChanged {
		if err := saveConfig(config); err != nil {
			fmt.Fprintf(out, "Warning: could not save config: %v\n", err)
		}
	}

	if *isClearCache {
		if err := clearCache(); err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		fmt.Fprintln(out, "Cache cleared")
		return 0
	}
	if *isVersion {
		fmt.Fprintf(out, "Version: %s\n", version)
		return 0
	}

	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	searchTerm := strings.Join(fs.Args(), " ")

	var summary, urlStr string
	var cached bool

	if *source == "grokipedia" {
		grokEscaped := url.QueryEscape(strings.TrimSpace(searchTerm))

		var searchResults []string
		var cachedSearch bool
		searchResults, cachedSearch, err = searchGrokipedia(searchTerm)
		if err != nil {
			if titles, ok := getCachedSearch("grokipedia", grokEscaped); ok {
				fmt.Fprintf(out, "Network error (%v). Using previously cached search results.\n", err)
				searchResults = titles
				cachedSearch = true
			} else {
				fmt.Fprintf(out, "Error during search: %v\n", err)
				return 1
			}
		}
		if len(searchResults) == 0 {
			if cachedSearch {
				fmt.Fprintln(out, "Cached search results were empty.")
			} else {
				fmt.Fprintln(out, "No results found.")
			}
			return 1
		}

		var selectedTitle string
		if len(searchResults) == 1 {
			selectedTitle = searchResults[0]
		} else {
			selectedTitle = chooseResult(searchResults, maxResults, *lang)
		}
		fmt.Printf("Selected title: %s\n", selectedTitle)

		summary, urlStr, cached, err = getGrokipediaSummary(selectedTitle)
		if err != nil {
			fmt.Fprintf(out, "Error fetching summary: %v\n", err)
			return 1
		}
	} else {
		encodedSearchTerm := url.QueryEscape(searchTerm)

		var searchResults []string
		var cachedSearch bool
		searchResults, cachedSearch, err = searchWikipedia(*lang, encodedSearchTerm)
		if err != nil {
			if titles, ok := getCachedSearch(*lang, encodedSearchTerm); ok {
				fmt.Fprintf(out, "Network error (%v). Using previously cached search results.\n", err)
				searchResults = titles
				cachedSearch = true
			} else {
				fmt.Fprintf(out, "Error during search: %v\n", err)
				return 1
			}
		}
		if len(searchResults) == 0 {
			if cachedSearch {
				fmt.Fprintln(out, "Cached search results were empty.")
			} else {
				fmt.Fprintln(out, "No results found.")
			}
			return 1
		}

		var selectedTitle string
		if len(searchResults) == 1 {
			selectedTitle = searchResults[0]
		} else {
			selectedTitle = chooseResult(searchResults, maxResults, *lang)
		}

		summary, urlStr, cached, err = getWikipediaSummary(*lang, selectedTitle)
		if err != nil {
			fmt.Fprintf(out, "Error fetching summary: %v\n", err)
			return 1
		}
	}

	// Colorized output only when writing to an interactive terminal (stdout).
	if f, ok := out.(*os.File); ok {
		if stat, err := f.Stat(); err == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
			// Terminal detected: use colors for labels but leave summary plain (user prefers uncolored summary for better native contrast).
			headerColor := color.New(color.FgGreen, color.Bold)
			cachedColor := color.New(color.FgYellow)
			urlLabelColor := color.New(color.FgMagenta, color.Bold)
			linkColor := color.New(color.FgBlue, color.Underline)
			if *lang == "de" {
				headerColor.Fprintln(out, "\n\nZusammenfassung:")
			} else {
				headerColor.Fprintln(out, "\n\nSummary:")
			}
			if cached {
				cachedColor.Fprintln(out, "(cached)")
			}
			fmt.Fprintln(out, summary)
			urlLabelColor.Fprintln(out, "\nURL:")
			linkColor.Fprintln(out, urlStr)
			return 0
		}
	}

	// Non-terminal (e.g., tests, piped output): keep plain text.
	if *lang == "de" {
		fmt.Fprintln(out, "\n\nZusammenfassung:")
	} else {
		fmt.Fprintln(out, "\n\nSummary:")
	}
	if cached {
		fmt.Fprintln(out, "(cached)")
	}
	fmt.Fprintln(out, summary)
	fmt.Fprintln(out, "\nURL:")
	fmt.Fprintln(out, urlStr)
	return 0
}

func main() {
	os.Exit(run(os.Stdout, os.Args[1:]))
}
