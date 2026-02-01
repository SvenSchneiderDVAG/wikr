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
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/fatih/color"
)

var (
	chromeCheckOnce   sync.Once
	chromeAvailable   bool
	chromeCheckError  error
	chromeWarningOnce sync.Once
)

// checkChromeAvailable checks if Chrome/Chromium is installed on the system.
// The result is cached after the first check.
func checkChromeAvailable() (bool, error) {
	chromeCheckOnce.Do(func() {
		// Try to create an allocator context - this will fail if Chrome is not found
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
		)
		allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
		defer cancel()

		ctx, cancel := chromedp.NewContext(allocCtx)
		defer cancel()

		// Use a short timeout just to check if Chrome can start
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Try to run a minimal action - this will fail immediately if Chrome is not found
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return nil
		}))

		if err != nil {
			chromeCheckError = err
			chromeAvailable = false
		} else {
			chromeAvailable = true
			chromeCheckError = nil
		}
	})
	return chromeAvailable, chromeCheckError
}

// ErrChromeNotFound is returned when Chrome/Chromium is not installed on the system.
var ErrChromeNotFound = errors.New(tr("en", msgErrChromeNotFound))

const (
	wikipediaAPITemplate        = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
	wikipediaSearchAPITemplate  = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"
	grokipediaPageAPITemplate   = "https://grokipedia.com/page/%s"
	grokipediaSearchAPITemplate = "https://grokipedia.com/search?q=%s"
	cacheDuration               = 24 * time.Hour
	summaryMaxLen               = 1000
	version                     = "0.8.0"
)

// debug is a runtime variable (was const) so tests can toggle it to cover debug print branches.
var debug = false

var (
	userAgent = "wikr/" + version + " (+https://github.com/SvenSchneiderDVAG/wikr)"
	// httpGetFunc allows tests to inject a mock for network calls.
	httpGetFunc = httpGet
	httpTimeout = 10 * time.Second
	httpClient  = &http.Client{}
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
		return "", fmt.Errorf(trRaw("en", msgErrCacheDirNotFound), err)
	}
	wikrCacheDir := filepath.Join(cacheDir, "wikr")
	if err := os.MkdirAll(wikrCacheDir, 0755); err != nil {
		return "", fmt.Errorf(trRaw("en", msgErrCacheDirCreate), err)
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
	if err := writeFileAtomic(cachePath, data, 0644); err != nil {
		if debug {
			fmt.Printf("Error writing cache file %s: %v\n", cachePath, err)
		}
	}
}

func getSearchCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf(trRaw("en", msgErrCacheDirNotFound), err)
	}
	wikrCacheDir := filepath.Join(cacheDir, "wikr")
	if err := os.MkdirAll(wikrCacheDir, 0755); err != nil {
		return "", fmt.Errorf(trRaw("en", msgErrCacheDirCreate), err)
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
	if err := writeFileAtomic(path, data, 0644); err != nil {
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
		return "", fmt.Errorf(trRaw("en", msgErrConfigDirNotFound), err)
	}
	wikrConfigDir := filepath.Join(configDir, "wikr")
	if err := os.MkdirAll(wikrConfigDir, 0755); err != nil {
		return "", fmt.Errorf(trRaw("en", msgErrConfigDirCreate), err)
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
			return config, false, fmt.Errorf(trRaw("en", msgErrDefaultConfigCreate), err)
		}
		return config, false, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config, false, fmt.Errorf(trRaw("en", msgErrConfigRead), configPath, err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, false, fmt.Errorf(trRaw("en", msgErrConfigDecode), err)
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
		return fmt.Errorf(trRaw("en", msgErrConfigEncode), err)
	}
	if err := writeFileAtomic(configPath, data, 0644); err != nil {
		return fmt.Errorf(trRaw("en", msgErrConfigWrite), configPath, err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		if removeErr := os.Remove(path); removeErr == nil {
			renameErr := os.Rename(tmpName, path)
			if renameErr == nil {
				return nil
			}
			err = renameErr
		}
		cleanup()
		return err
	}
	return nil
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func readerFrom(in io.Reader) *bufio.Reader {
	if r, ok := in.(*bufio.Reader); ok {
		return r
	}
	return bufio.NewReader(in)
}

func truncateWithEllipsis(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

// httpGet performs an HTTP GET with retries and returns body, status, contentType.
// Retries on network errors and selected transient HTTP statuses (429, 500-503).
func httpGet(endpoint string) ([]byte, int, string, error) {
	var lastErr error
	backoff := 150 * time.Millisecond
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			return nil, 0, "", fmt.Errorf(trRaw("en", msgErrHTTPCreateRequest), err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
		resp, err := httpClient.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf(trRaw("en", msgErrHTTPPerformRequest), err)
		} else {
			ct := resp.Header.Get("Content-Type")
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if rerr != nil {
				return nil, resp.StatusCode, ct, fmt.Errorf(trRaw("en", msgErrHTTPReadBody), rerr)
			}
			// Retry on transient HTTP codes
			if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode <= 503) {
				lastErr = fmt.Errorf(trRaw("en", msgErrHTTPTransientStatus), resp.StatusCode)
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
		return nil, false, fmt.Errorf(trRaw("en", msgErrSearchUnexpectedStatus), status)
	}
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		if titles, ok := getCachedSearch(lang, escapedQuery); ok {
			if debug {
				fmt.Printf("Non-JSON Content-Type '%s' for search; using cache fallback.\n", ct)
			}
			return titles, true, nil
		}
		return nil, false, fmt.Errorf(trRaw("en", msgErrSearchUnexpectedContentType), ct)
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
		return nil, false, fmt.Errorf(trRaw("en", msgErrSearchDecodeJSON), err)
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
// It returns (title, quit, refineQuery).
func chooseResult(results []string, maxResults *int, lang, source string, out io.Writer, in io.Reader) (string, bool, string) {
	limit := *maxResults
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}
	fmt.Fprintln(out)
	useColor := isTerminal(out)
	header := tr(lang, msgMultipleResults, limit, len(results), source)
	if useColor {
		color.New(color.FgCyan).Fprintln(out, header)
	} else {
		fmt.Fprintln(out, header)
	}
	for i := 0; i < limit; i++ {
		fmt.Fprintf(out, "  [%d] %s\n", i+1, results[i])
	}
	fmt.Fprintln(out)

	reader := readerFrom(in)
	for {
		fmt.Fprint(out, tr(lang, msgSelectPrompt))
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return results[0], false, ""
		}
		lower := strings.ToLower(line)
		if lower == "q" || lower == "quit" {
			return "", true, ""
		}
		if lower == "r" || lower == "refine" {
			fmt.Fprint(out, tr(lang, msgRefinePrompt))
			newLine, _ := reader.ReadString('\n')
			newLine = strings.TrimSpace(newLine)
			if newLine == "" {
				fmt.Fprintln(out, tr(lang, msgSearchTermEmpty))
				continue
			}
			return "", false, newLine
		}
		// Attempt to parse numeric selection.
		var idx int
		_, err := fmt.Sscanf(line, "%d", &idx)
		if err == nil && idx >= 1 && idx <= limit {
			return results[idx-1], false, ""
		}
		invalidMsg := tr(lang, msgInvalidSelection, limit)
		if useColor {
			color.New(color.FgYellow).Fprintln(out, invalidMsg)
		} else {
			fmt.Fprintln(out, invalidMsg)
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
		return "", "", false, fmt.Errorf(trRaw("en", msgErrSummaryUnexpectedStatus), status)
	}
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return "", "", false, fmt.Errorf(trRaw("en", msgErrSummaryUnexpectedContentType), ct)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", false, fmt.Errorf(trRaw("en", msgErrSummaryDecodeJSON), err)
	}
	extractVal, ok := result["extract"]
	if !ok {
		return "", "", false, errors.New(tr("en", msgErrSummaryMissingExtract))
	}
	extractStr, ok := extractVal.(string)
	if !ok {
		return "", "", false, errors.New(tr("en", msgErrSummaryExtractNotString))
	}
	contentURLs, ok := result["content_urls"].(map[string]any)
	if !ok {
		return "", "", false, errors.New(tr("en", msgErrSummaryMissingContentURLs))
	}
	desktop, ok := contentURLs["desktop"].(map[string]any)
	if !ok {
		return "", "", false, errors.New(tr("en", msgErrSummaryMissingDesktop))
	}
	pageURL, ok := desktop["page"].(string)
	if !ok {
		return "", "", false, errors.New(tr("en", msgErrSummaryMissingPageURL))
	}
	extractStr = truncateWithEllipsis(extractStr, summaryMaxLen)
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

func isGrokipediaNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), tr("en", msgErrGrokipediaMissingArticleSuffix))
}

func probeGrokipediaTitle(title string) (bool, error) {
	slug := slugifyGrokipedia(title)
	if slug == "" {
		return false, nil
	}
	endpoint := fmt.Sprintf(grokipediaPageAPITemplate, url.PathEscape(slug))
	body, status, _, err := httpGetFunc(endpoint)
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if status != http.StatusOK {
		return false, fmt.Errorf(trRaw("en", msgErrGrokipediaUnexpectedStatus), status)
	}
	summary := extractMetaDescription(string(body))
	return summary != "", nil
}

func filterGrokipediaResults(results []string, maxResults int) ([]string, error) {
	if maxResults <= 0 {
		maxResults = len(results)
	}
	valid := make([]string, 0, maxResults)
	for _, title := range results {
		if len(valid) >= maxResults {
			break
		}
		ok, err := probeGrokipediaTitle(title)
		if err != nil {
			return nil, err
		}
		if ok {
			valid = append(valid, title)
		}
	}
	return valid, nil
}

func getGrokipediaSummary(title string) (string, string, bool, error) {
	slug := slugifyGrokipedia(title)
	if slug == "" {
		return "", "", false, errors.New(tr("en", msgErrGrokipediaEmptyTitle))
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
		return "", "", false, fmt.Errorf(trRaw("en", msgErrGrokipediaMissingArticle), title)
	}

	if status != http.StatusOK {
		return "", "", false, fmt.Errorf(trRaw("en", msgErrGrokipediaUnexpectedStatus), status)
	}

	// Extract summary from meta description tag
	htmlContent := string(body)
	summary := extractMetaDescription(htmlContent)

	if summary == "" {
		return "", "", false, fmt.Errorf(trRaw("en", msgErrGrokipediaMissingArticle), title)
	}

	summary = truncateWithEllipsis(summary, summaryMaxLen)

	setCachedEntry("grokipedia", slug, summary, endpoint)
	return summary, endpoint, false, nil
}

func removeTitle(results []string, title string) []string {
	if len(results) == 0 {
		return results
	}
	updated := results[:0]
	for _, t := range results {
		if t != title {
			updated = append(updated, t)
		}
	}
	return updated
}

func promptGrokipediaMissing(out io.Writer, in io.Reader, lang string, hasAlternatives bool) (string, string) {
	reader := readerFrom(in)
	for {
		promptKey := msgArticleNotFoundNoAlt
		if hasAlternatives {
			promptKey = msgArticleNotFoundAlt
		}
		fmt.Fprint(out, tr(lang, promptKey))
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if err != nil && line == "" {
			return "quit", ""
		}
		switch line {
		case "a", "y", "j":
			if hasAlternatives {
				return "another", ""
			}
			fmt.Fprintln(out, tr(lang, msgNoOtherResults))
		case "r", "refine":
			fmt.Fprint(out, tr(lang, msgRefinePrompt))
			newLine, err := reader.ReadString('\n')
			newLine = strings.TrimSpace(newLine)
			if err != nil && newLine == "" {
				return "quit", ""
			}
			if newLine == "" {
				fmt.Fprintln(out, tr(lang, msgSearchTermEmpty))
				continue
			}
			return "refine", newLine
		case "q", "quit":
			return "quit", ""
		default:
			fmt.Fprintln(out, tr(lang, msgInvalidChoice))
		}
	}
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

	// Test if Chrome is available by running a minimal action
	// This will fail fast if Chrome is not installed
	if err := chromedp.Run(ctx); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "executable file not found") ||
			strings.Contains(errStr, "exec:") ||
			strings.Contains(errStr, "cannot find") ||
			strings.Contains(errStr, "not found") ||
			strings.Contains(errStr, "no such file") {
			return nil, ErrChromeNotFound
		}
		return nil, err
	}

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
		return nil, false, errors.New(tr("en", msgErrGrokipediaEmptySearchQuery))
	}

	if debug {
		fmt.Printf("Launching chromedp search for: %s\n", trimmed)
	}

	// Check if Chrome is available before attempting chromedp
	if available, err := checkChromeAvailable(); !available {
		chromeWarningOnce.Do(func() {
			if err != nil && (strings.Contains(err.Error(), "executable file not found") ||
				strings.Contains(err.Error(), "not found") ||
				strings.Contains(err.Error(), "no such file")) {
				fmt.Fprintf(os.Stderr, "Warning: Chrome/Chromium not found. Grokipedia search requires Chrome for full functionality.\n")
				fmt.Fprintf(os.Stderr, "         Install Chrome or Chromium to enable full search. Falling back to direct page access.\n")
			} else if err != nil && debug {
				fmt.Printf("Chrome check failed: %v\n", err)
			}
		})
		return searchGrokipediaDirectFallback(trimmed, escapedQuery)
	}

	titles, err := searchGrokipediaChromedp(trimmed, escapedQuery)
	if err != nil {
		if debug {
			fmt.Printf("chromedp search error: %v\n", err)
		}
		// If Chrome is not found, return the error directly without fallback
		if errors.Is(err, ErrChromeNotFound) {
			return nil, false, err
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
		return fmt.Errorf(trRaw("en", msgErrCachePath), err)
	}
	err = os.Remove(cachePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(trRaw("en", msgErrCacheDeleteFile), err)
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
		fmt.Fprintln(out, tr("en", msgWarningConfigLoad, err))
		config = Config{Language: "en", MaxResults: 5}
	} else if corrected {
		if err := saveConfig(config); err != nil && debug {
			fmt.Fprintln(out, tr(config.Language, msgConfigPersistWarning, err))
		}
	}

	fs := flag.NewFlagSet("wikr", flag.ContinueOnError)
	fs.SetOutput(out)
	lang := fs.String("lang", config.Language, tr(config.Language, msgFlagLang))
	source := fs.String("source", config.Source, tr(config.Language, msgFlagSource))
	maxResults := fs.Int("max", config.MaxResults, tr(config.Language, msgFlagMax))
	isClearCache := fs.Bool("clear-cache", false, tr(config.Language, msgFlagClearCache))
	isVersion := fs.Bool("version", false, tr(config.Language, msgFlagVersion))
	isResetConfig := fs.Bool("reset-config", false, tr(config.Language, msgFlagResetConfig))
	fs.Usage = func() {
		fmt.Fprint(out, tr(*lang, msgUsage))
		fmt.Fprint(out, tr(*lang, msgOptions))
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if *isResetConfig {
		defaultCfg := Config{Language: "en", MaxResults: 5, Source: "wikipedia"}
		if err := saveConfig(defaultCfg); err != nil {
			fmt.Fprintln(out, tr(*lang, msgErrorWriteDefaultConfig, err))
			return 1
		}
		fmt.Fprintln(out, tr(*lang, msgConfigReset))
		return 0
	}

	src := strings.ToLower(strings.TrimSpace(*source))
	if src == "" {
		src = "wikipedia"
	}
	if src != "wikipedia" && src != "grokipedia" {
		fmt.Fprintln(out, tr(*lang, msgInvalidSource, src))
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
			fmt.Fprintln(out, tr(*lang, msgWarningSaveConfig, err))
		}
	}

	if *isClearCache {
		if err := clearCache(); err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		fmt.Fprintln(out, tr(*lang, msgCacheCleared))
		return 0
	}
	if *isVersion {
		fmt.Fprintln(out, tr(*lang, msgVersion, version))
		return 0
	}

	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	searchTerm := strings.Join(fs.Args(), " ")
	input := bufio.NewReader(os.Stdin)

	var summary, urlStr string
	var cached bool

	if *source == "grokipedia" {
	searchLoop:
		for {
			grokEscaped := url.QueryEscape(strings.TrimSpace(searchTerm))

			var searchResults []string
			var cachedSearch bool
			searchResults, cachedSearch, err = searchGrokipedia(searchTerm)
			if err != nil {
				if errors.Is(err, ErrChromeNotFound) {
					fmt.Fprintln(out, tr(*lang, msgChromeNotFoundError))
					fmt.Fprintln(out, tr(*lang, msgChromeNotFoundDetail))
					fmt.Fprintln(out, tr(*lang, msgChromeNotFoundHint))
					return 1
				}
				if titles, ok := getCachedSearch("grokipedia", grokEscaped); ok {
					fmt.Fprintln(out, tr(*lang, msgNetworkErrorCacheFallback, err))
					searchResults = titles
					cachedSearch = true
				} else {
					fmt.Fprintln(out, tr(*lang, msgErrorDuringSearch, err))
					return 1
				}
			}
			if len(searchResults) == 0 {
				if cachedSearch {
					fmt.Fprintln(out, tr(*lang, msgCachedSearchEmpty))
				} else {
					fmt.Fprintln(out, tr(*lang, msgNoResults))
				}
				return 1
			}

			if filtered, ferr := filterGrokipediaResults(searchResults, *maxResults); ferr == nil {
				if len(filtered) == 0 {
					fmt.Fprintln(out, tr(*lang, msgNoResults))
					return 1
				}
				searchResults = filtered
			}

			for {
				var selectedTitle string
				if len(searchResults) == 1 {
					selectedTitle = searchResults[0]
				} else {
					var quit bool
					var refine string
					selectedTitle, quit, refine = chooseResult(searchResults, maxResults, *lang, *source, os.Stderr, input)
					if quit {
						fmt.Fprintln(out, tr(*lang, msgSelectionCanceled))
						return 0
					}
					if refine != "" {
						searchTerm = refine
						continue searchLoop
					}
				}

				summary, urlStr, cached, err = getGrokipediaSummary(selectedTitle)
				if err != nil {
					if isGrokipediaNotFound(err) {
						searchResults = removeTitle(searchResults, selectedTitle)
						action, refine := promptGrokipediaMissing(os.Stderr, input, *lang, len(searchResults) > 0)
						switch action {
						case "another":
							if len(searchResults) == 0 {
								continue
							}
							continue
						case "refine":
							searchTerm = refine
							continue searchLoop
						case "quit":
							fmt.Fprintln(out, tr(*lang, msgSelectionCanceled))
							return 0
						}
					}
					fmt.Fprintln(out, tr(*lang, msgErrorFetchingSummary, err))
					return 1
				}
				break searchLoop
			}
		}
	} else {
		for {
			encodedSearchTerm := url.QueryEscape(searchTerm)

			var searchResults []string
			var cachedSearch bool
			searchResults, cachedSearch, err = searchWikipedia(*lang, encodedSearchTerm)
			if err != nil {
				if titles, ok := getCachedSearch(*lang, encodedSearchTerm); ok {
					fmt.Fprintln(out, tr(*lang, msgNetworkErrorCacheFallback, err))
					searchResults = titles
					cachedSearch = true
				} else {
					fmt.Fprintln(out, tr(*lang, msgErrorDuringSearch, err))
					return 1
				}
			}
			if len(searchResults) == 0 {
				if cachedSearch {
					fmt.Fprintln(out, tr(*lang, msgCachedSearchEmpty))
				} else {
					fmt.Fprintln(out, tr(*lang, msgNoResults))
				}
				return 1
			}

			var selectedTitle string
			if len(searchResults) == 1 {
				selectedTitle = searchResults[0]
			} else {
				var quit bool
				var refine string
				selectedTitle, quit, refine = chooseResult(searchResults, maxResults, *lang, *source, os.Stderr, input)
				if quit {
					fmt.Fprintln(out, tr(*lang, msgSelectionCanceled))
					return 0
				}
				if refine != "" {
					searchTerm = refine
					continue
				}
			}

			summary, urlStr, cached, err = getWikipediaSummary(*lang, selectedTitle)
			if err != nil {
				fmt.Fprintln(out, tr(*lang, msgErrorFetchingSummary, err))
				return 1
			}
			break
		}
	}

	// Colorized output only when writing to an interactive terminal (stdout).
	if isTerminal(out) {
		// Terminal detected: use colors for labels but leave summary plain (user prefers uncolored summary for better native contrast).
		headerColor := color.New(color.FgGreen, color.Bold)
		cachedColor := color.New(color.FgYellow)
		urlLabelColor := color.New(color.FgMagenta, color.Bold)
		linkColor := color.New(color.FgBlue, color.Underline)
		headerColor.Fprintln(out, "\n\n"+tr(*lang, msgSummaryHeader))
		if cached {
			cachedColor.Fprintln(out, tr(*lang, msgCachedMarker))
		}
		fmt.Fprintln(out, summary)
		urlLabelColor.Fprintln(out, "\n"+tr(*lang, msgURLLabel))
		linkColor.Fprintln(out, urlStr)
		return 0
	}

	// Non-terminal (e.g., tests, piped output): keep plain text.
	fmt.Fprintln(out, "\n\n"+tr(*lang, msgSummaryHeader))
	if cached {
		fmt.Fprintln(out, tr(*lang, msgCachedMarker))
	}
	fmt.Fprintln(out, summary)
	fmt.Fprintln(out, "\n"+tr(*lang, msgURLLabel))
	fmt.Fprintln(out, urlStr)
	return 0
}

func main() {
	os.Exit(run(os.Stdout, os.Args[1:]))
}
