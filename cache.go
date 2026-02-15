package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type CacheEntry struct {
	Summary   string    `json:"summary"`
	URL       string    `json:"url"`
	Timestamp time.Time `json:"timestamp"`
}

type SearchResultEntry struct {
	Titles    []string  `json:"titles"`
	Timestamp time.Time `json:"timestamp"`
}

type Cache map[string]CacheEntry

func getWikrCacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", wrapLocalizedError(msgErrCacheDirNotFound, err)
	}
	wikrCacheDir := filepath.Join(cacheDir, "wikr")
	if err := os.MkdirAll(wikrCacheDir, 0755); err != nil {
		return "", wrapLocalizedError(msgErrCacheDirCreate, err)
	}
	return wikrCacheDir, nil
}

func getCachePath() (string, error) {
	cacheDir, err := getWikrCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "cache.json"), nil
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
		return cache
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		if debug {
			fmt.Printf("Error reading cache file %s: %v\n", cachePath, err)
		}
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil && debug {
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
	if err != nil {
		if debug {
			fmt.Printf("Error encoding cache: %v\n", err)
		}
		return
	}
	if err := writeFileAtomic(cachePath, data, 0644); err != nil && debug {
		fmt.Printf("Error writing cache file %s: %v\n", cachePath, err)
	}
}

func getSearchCachePath() (string, error) {
	cacheDir, err := getWikrCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "search_cache.json"), nil
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
	if err := json.Unmarshal(data, &cache); err != nil && debug {
		fmt.Printf("Error decoding search cache: %v\n", err)
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
	if err := writeFileAtomic(path, data, 0644); err != nil && debug {
		fmt.Printf("Error writing search cache: %v\n", err)
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
	if !exists {
		return "", "", false
	}
	if debug {
		fmt.Printf("Cache entry found, age: %v\n", time.Since(entry.Timestamp))
	}
	if time.Since(entry.Timestamp) >= cacheDuration {
		return "", "", false
	}
	return entry.Summary, entry.URL, true
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

func clearCache() error {
	cachePath, err := getCachePath()
	if err != nil {
		return wrapLocalizedError(msgErrCachePath, err)
	}
	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		return newLocalizedError(msgErrCacheDeleteFile, err)
	}
	if debug {
		fmt.Println("Cache was deleted successfully.")
	}
	return nil
}
