package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"bufio"
	"github.com/fatih/color"
	"path/filepath"
	"time"
	"sync"
	"flag"
)

const (
	wikipediaAPITemplate = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
	wikipediaSearchAPITemplate = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"
	cacheFileName = ".wikr_cache.json"
	cacheDuration = 24 * time.Hour
	debug = false
	version = "0.1.0"
)

type CacheEntry struct {
	Summary   string    `json:"summary"`
	URL       string    `json:"url"`
	Timestamp time.Time `json:"timestamp"`
}

type Cache map[string]CacheEntry

type Config struct {
	MaxResults int
}

func getCachePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return cacheFileName
	}
	return filepath.Join(homeDir, cacheFileName)
}

func loadCache() Cache {
	createEmptyCacheFileIfNotExists()
	cache := make(Cache)
	cachePath := getCachePath()
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
	data, err := json.Marshal(cache)
	if err != nil && debug {
		fmt.Printf("Error encoding cache: %v\n", err)
		return
	}
	cachePath := getCachePath()
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

func showLoadingAnimation(done chan bool) {
	animation := []string{"|", "/", "-", "\\"}
	i := 0
	for {
		select {
		case <-done:
			return
		default:
			fmt.Println()
			fmt.Printf("\rLade Daten... %s", animation[i])
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

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r")
		return "", "", false, err
	}

	summary := result["extract"].(string)
	url := result["content_urls"].(map[string]interface{})["desktop"].(map[string]interface{})["page"].(string)

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
	cachePath := getCachePath()
	err := os.Remove(cachePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error deleting cache file: %v", err)
	}
	if debug {
		fmt.Println("Cache was deleted successfully.")
	}
	return nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <search term>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -lang en -max 10 Golang\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -clear-cache\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -version\n", os.Args[0])
	}
	lang := flag.String("lang", "de", "language of the Wikipedia")
	maxResults := flag.Int("max", 5, "maximum amount of result entries")
	isClearCache := flag.Bool("clear-cache", false, "clear cache and exit")
	isVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *isClearCache {
		err := clearCache()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Cache cleared.")
		return
	}

	if len(os.Args) < 2 {
        fmt.Fprintf(os.Stderr, "Error: search term is required\n")
        flag.Usage()
        os.Exit(1)
    }

	if *isVersion {
		fmt.Println("Version:", version)
		return
	}

	var searchTermParts []string

	if os.Args[1] == "de" || os.Args[1] == "en" {
		*lang = os.Args[1]
		searchTermParts = os.Args[2:]
	} else if os.Args[1] == "-lang" {
		*lang = os.Args[2]
		searchTermParts = os.Args[3:]
	} else {
		searchTermParts = os.Args[1:]
	}

	if len(searchTermParts) == 0 {
		fmt.Println("Please provide a search term.")
		os.Exit(1)
	}

	searchTerm := strings.Join(searchTermParts, " ")
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

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	searchResults := result["query"].(map[string]interface{})["search"].([]interface{})
	titles := make([]string, len(searchResults))
	for i, item := range searchResults {
		titles[i] = item.(map[string]interface{})["title"].(string)
	}

	return titles, nil
}

func chooseResult(results []string, maxResults *int) string {
	if len(results) > *maxResults {
		results = results[:*maxResults]
	}
	fmt.Println("\nMultiple results found. Please choose one:")
	for i, result := range results {
		fmt.Printf("%d. %s\n", i+1, result)
	}
	fmt.Println("q. Quit")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nEnter the number of the desired result (or 'q' to quit): ")
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

func createEmptyCacheFileIfNotExists() {
	cachePath := getCachePath()
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		emptyCache := make(Cache)
		data, err := json.Marshal(emptyCache)
		if err != nil && debug {
			fmt.Printf("Error creating empty cache file: %v\n", err)
			return
		}
		err = os.WriteFile(cachePath, data, 0644)
		if err != nil && debug {
			fmt.Printf("Error writing empty cache file %s: %v\n", cachePath, err)
		} else if debug {
			fmt.Printf("Empty cache file was created: %s\n", cachePath)
		}
	}
}
