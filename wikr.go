package main

import (
	"bufio"
	"encoding/json"
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
	version                    = "0.4.0"
)

type CacheEntry struct {
	Summary   string    `json:"summary"`
	URL       string    `json:"url"`
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

func loadConfig() (Config, error) {
	config := Config{
		Language:   "en",
		MaxResults: 5,
	}
	configPath, err := getConfigPath()
	if err != nil {
		return config, err
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create a default config file if it doesn't exist
		if err := saveConfig(config); err != nil {
			return config, fmt.Errorf("could not create default config: %w", err)
		}
		return config, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config, fmt.Errorf("error reading config file %s: %w", configPath, err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("error decoding config: %w", err)
	}

	return config, nil
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

func getWikipediaSummary(lang, title string) (string, string, bool, error) {
	done := make(chan bool)
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		showLoadingAnimation(done)
	}()

	// Try to get the entry from the cache first
	if summary, url, found := getCachedEntry(lang, title); found {
		close(done)
		wg.Wait()
		fmt.Print("\r") // Clears the loading animation
		return summary, url, true, nil
	}

	encodedTitle := url.PathEscape(title)
	response, err := http.Get(fmt.Sprintf(wikipediaAPITemplate, lang) + encodedTitle)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r")
		return "", "", false, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r")
		return "", "", false, err
	}

	var result map[string]any
	err = json.Unmarshal(body, &result)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r")
		return "", "", false, err
	}

	summary := result["extract"].(string)
	url := result["content_urls"].(map[string]any)["desktop"].(map[string]any)["page"].(string)

	// Shorten the summary to a maximum of 1000 characters
	if len(summary) > 1000 {
		summary = summary[:997] + "..."
	}

	close(done)
	wg.Wait()
	fmt.Print("\r") // Clears the loading animation

	// Cache the new entry
	setCachedEntry(lang, title, summary, url)

	return summary, url, false, nil
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
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("Warning: could not load config: %v\n", err)
		// Set default values if config loading fails
		config = Config{Language: "en", MaxResults: 5}
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <search term>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -lang de -max 10 Golang\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -clearcache\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -version\n", os.Args[0])
	}

	lang := flag.String("lang", config.Language, "language of the Wikipedia to use")
	maxResults := flag.Int("max", config.MaxResults, "maximum amount of result entries")
	isClearCache := flag.Bool("clearcache", false, "clear the cache")
	isVersion := flag.Bool("version", false, "show version")

	flag.Parse()

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

	// Search for possible results
	searchResults, err := searchWikipedia(*lang, encodedSearchTerm)
	if err != nil {
		fmt.Println("Error during search:", err)
		os.Exit(1)
	}

	if len(searchResults) == 0 {
		fmt.Println("No results found.")
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
	if cached {
		color.Yellow("(cached)")
	}
	fmt.Println(summary)
	color.Green("\nURL:")
	fmt.Println(url)
}

func searchWikipedia(lang, term string) ([]string, error) {
	response, err := http.Get(fmt.Sprintf(wikipediaSearchAPITemplate, lang, term))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	searchResults := result["query"].(map[string]any)["search"].([]any)
	titles := make([]string, len(searchResults))
	for i, item := range searchResults {
		titles[i] = item.(map[string]any)["title"].(string)
	}

	return titles, nil
}

func chooseResult(results []string, maxResults *int) string {
	if len(results) > *maxResults {
		results = results[:*maxResults]
	}
	fmt.Print("\nMultiple results found. Please choose one:\n\n")
	for i, result := range results {
		fmt.Printf("%d. %s\n", i+1, result)
	}
	fmt.Println("\nq. quit")

	reader := bufio.NewReader(os.Stdin)
	color.Set(color.FgWhite, color.Bold)
	for {
		fmt.Print("\nEnter the number of the desired result (or 'q' to quit): ")
		color.Unset()
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "q" {
			fmt.Println("\nProgram was exited.")
			os.Exit(0)
		}

		index := 0
		_, err := fmt.Sscanf(input, "%d", &index)
		if err == nil && index > 0 && index <= len(results) {
			return results[index-1]
		}
		fmt.Println("\nInvalid input. Please try again.")
	}
}
