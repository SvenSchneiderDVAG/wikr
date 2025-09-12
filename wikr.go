package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)

const (
	wikipediaAPITemplate       = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
	wikipediaSearchAPITemplate = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"
	cacheDuration              = 24 * time.Hour
	debug                      = false
	version                    = "0.5.0"
	userAgent                  = "wikr/0"
)

type CacheEntry struct {
	Summary   string    `json:"summary"`
	URL       string    `json:"url"`
	Timestamp time.Time `json:"timestamp"`
}

// SearchResultEntry caches a list of titles for a given (lang, query) pair.
type SearchResultEntry struct {
    Titles     []string  `json:"titles"`
    Timestamp  time.Time `json:"timestamp"`
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

// ---------- Search results cache (separate file) ----------

func getSearchCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil { return "", fmt.Errorf("could not find cache directory: %w", err) }
	wikrCacheDir := filepath.Join(cacheDir, "wikr")
	if err := os.MkdirAll(wikrCacheDir, 0755); err != nil {
		return "", fmt.Errorf("could not create cache directory: %w", err)
	}
	return filepath.Join(wikrCacheDir, "search_cache.json"), nil
}

func loadSearchCache() map[string]SearchResultEntry {
	cache := make(map[string]SearchResultEntry)
	path, err := getSearchCachePath()
	if err != nil { if debug { fmt.Printf("Error search cache path: %v\n", err) }; return cache }
	if _, err := os.Stat(path); os.IsNotExist(err) { return cache }
	data, err := os.ReadFile(path)
	if err != nil { if debug { fmt.Printf("Error reading search cache: %v\n", err) }; return cache }
	if err := json.Unmarshal(data, &cache); err != nil { if debug { fmt.Printf("Error decoding search cache: %v\n", err) } }
	return cache
}

func saveSearchCache(c map[string]SearchResultEntry) {
	path, err := getSearchCachePath()
	if err != nil { if debug { fmt.Printf("Error getting search cache path for saving: %v\n", err) }; return }
	data, err := json.Marshal(c)
	if err != nil { if debug { fmt.Printf("Error encoding search cache: %v\n", err) }; return }
	if err := os.WriteFile(path, data, 0644); err != nil { if debug { fmt.Printf("Error writing search cache: %v\n", err) } }
}

func getCachedSearch(lang, escapedQuery string) ([]string, bool) {
	c := loadSearchCache()
	key := lang + ":" + escapedQuery
	entry, ok := c[key]
	if !ok { return nil, false }
	if time.Since(entry.Timestamp) >= cacheDuration { return nil, false }
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

func showLoadingAnimation(done chan bool) {
	animation := []string{"|", "/", "-", "\\"}
	i := 0
	for {
		select {
		case <-done:
			return
		default:
			fmt.Printf("\rLoading data... %s", animation[i])
			i = (i + 1) % len(animation)
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// httpGet performs an HTTP GET with the fixed user agent and JSON accept headers.
// It returns the response body, status code and error (if any). The caller is responsible
// for interpreting non-200 status codes.
func httpGet(endpoint string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// searchWikipedia queries the MediaWiki search API and returns a list of page titles.
// The provided query argument is expected to already be URL-escaped.
func searchWikipedia(lang, escapedQuery string) ([]string, bool, error) {
    // Try cache first
    if titles, ok := getCachedSearch(lang, escapedQuery); ok {
        return titles, true, nil
    }
    endpoint := fmt.Sprintf(wikipediaSearchAPITemplate, lang, escapedQuery)
    body, status, err := httpGet(endpoint)
    if err != nil {
        return nil, false, err
    }
    if status != http.StatusOK {
        return nil, false, fmt.Errorf("unexpected status %d from search endpoint", status)
    }
    var payload struct {
        Query struct {
            Search []struct { Title string `json:"title"` } `json:"search"`
        } `json:"query"`
    }
    if err := json.Unmarshal(body, &payload); err != nil {
        return nil, false, fmt.Errorf("decode search JSON: %w", err)
    }
    titles := make([]string, 0, len(payload.Query.Search))
    for _, s := range payload.Query.Search { if s.Title != "" { titles = append(titles, s.Title) } }
    if len(titles) > 0 { setCachedSearch(lang, escapedQuery, titles) }
    return titles, false, nil
}

// chooseResult lets the user pick one of the returned titles when more than one
// result is available. It displays up to *maxResults entries. If the user presses
// enter without input, the first entry is selected. Continues prompting until
// valid selection is made.
func chooseResult(results []string, maxResults *int) string {
	limit := *maxResults
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}
	color.Cyan("Multiple results found (showing %d of %d):", limit, len(results))
	for i := 0; i < limit; i++ {
		fmt.Printf("  [%d] %s\n", i+1, results[i])
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Select a result number (default 1): ")
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
		color.Yellow("Invalid selection. Please enter a number between 1 and %d.", limit)
	}
}

func getWikipediaSummary(lang, title string) (string, string, bool, error) {
	done := make(chan bool)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); showLoadingAnimation(done) }()

	// Try cache first
	if summary, urlStr, found := getCachedEntry(lang, title); found {
		close(done); wg.Wait(); fmt.Print("\r")
		return summary, urlStr, true, nil
	}

	endpoint := fmt.Sprintf(wikipediaAPITemplate, lang) + url.PathEscape(title)
	body, status, err := httpGet(endpoint)
	if err != nil { close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, err }
	if status != http.StatusOK {
		close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, fmt.Errorf("unexpected status %d from summary endpoint", status)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, fmt.Errorf("decode summary JSON: %w", err)
	}

	extractVal, ok := result["extract"]
	if !ok { close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, errors.New("missing 'extract' in response") }
	extractStr, ok := extractVal.(string)
	if !ok { close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, errors.New("'extract' field not a string") }

	contentURLs, ok := result["content_urls"].(map[string]any)
	if !ok { close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, errors.New("missing content_urls") }
	desktop, ok := contentURLs["desktop"].(map[string]any)
	if !ok { close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, errors.New("missing desktop in content_urls") }
	pageURL, ok := desktop["page"].(string)
	if !ok { close(done); wg.Wait(); fmt.Print("\r"); return "", "", false, errors.New("missing page URL") }

	if len(extractStr) > 1000 { extractStr = extractStr[:997] + "..." }

	close(done); wg.Wait(); fmt.Print("\r")
	setCachedEntry(lang, title, extractStr, pageURL)
	return extractStr, pageURL, false, nil
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

func main() {
	config, corrected, err := loadConfig()
	if err != nil {
		fmt.Printf("Warning: could not load config: %v\n", err)
		config = Config{Language: "en", MaxResults: 5}
	} else if corrected {
		// Auto-save corrections silently
		if err := saveConfig(config); err != nil && debug {
			fmt.Printf("Could not persist corrected config: %v\n", err)
		}
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <search term>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -lang de -max 10 Golang\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -clearcache\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -reset-config\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -version\n", os.Args[0])
	}

	lang := flag.String("lang", config.Language, "language of the Wikipedia to use (en|de)")
	maxResults := flag.Int("max", config.MaxResults, "maximum amount of result entries")
	isClearCache := flag.Bool("clearcache", false, "clear the cache")
	isVersion := flag.Bool("version", false, "show version")
	isResetConfig := flag.Bool("reset-config", false, "regenerate default configuration and exit")

	flag.Parse()

	if *isResetConfig {
		defaultCfg := Config{Language: "en", MaxResults: 5}
		if err := saveConfig(defaultCfg); err != nil {
			fmt.Printf("Error writing default config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Configuration reset to defaults (language=en, max_results=5)")
		return
	}

	// Update config if flags are set
	configChanged := false
	flag.Visit(func(f *flag.Flag) {
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
		}
	})

	if configChanged {
		if err := saveConfig(config); err != nil {
			fmt.Printf("Warning: could not save config: %v\n", err)
		}
	}

	if *isClearCache {
		err := clearCache()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Cache cleared")
		return
	}

	if *isVersion {
		fmt.Println("Version:", version)
		return
	}

	if len(flag.Args()) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	searchTerm := strings.Join(flag.Args(), " ")
	encodedSearchTerm := url.QueryEscape(searchTerm)

	// Search for possible results (with caching + graceful fallback)
	searchResults, cachedSearch, err := searchWikipedia(*lang, encodedSearchTerm)
	if err != nil {
		// Attempt to fall back to any cached results (even if expired) by forcing direct cache load
		if titles, ok := getCachedSearch(*lang, encodedSearchTerm); ok {
			color.Yellow("Network error (%v). Using previously cached search results.", err)
			searchResults = titles
			cachedSearch = true
		} else {
			fmt.Println("Error during search:", err)
			os.Exit(1)
		}
	}

	if len(searchResults) == 0 {
		if cachedSearch {
			fmt.Println("Cached search results were empty.")
		} else {
			fmt.Println("No results found.")
		}
		os.Exit(1)
	}

	var selectedTitle string
	if len(searchResults) == 1 {
		selectedTitle = searchResults[0]
	} else {
		selectedTitle = chooseResult(searchResults, maxResults)
	}

	// Get the summary for the selected title
	summary, url, cached, err := getWikipediaSummary(*lang, selectedTitle)
	if err != nil {
		color.Red("Error fetching summary: %v", err)
		os.Exit(1)
	}

	color.Blue("\n\nSummary:")
	if cached { color.Yellow("(cached)") }
	fmt.Println(summary)
	color.Green("\nURL:")
	fmt.Println(url)
}
