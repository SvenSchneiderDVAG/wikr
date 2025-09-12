package main

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if cachePath, err := getCachePath(); err == nil {
		os.Remove(cachePath)
	}
	if searchCachePath, err := getSearchCachePath(); err == nil {
		os.Remove(searchCachePath)
	}
	os.Exit(code)
}

func TestGetCachePath(t *testing.T) {
	path, err := getCachePath()
	if err != nil {
		t.Errorf("getCachePath should not return an error: %v", err)
	}
	if path == "" {
		t.Error("getCachePath should return a non-empty path")
	}
}

func TestLoadAndSaveCache(t *testing.T) {
	testCache := Cache{
		"en:Test": CacheEntry{
			Summary:   "This is a test article.",
			URL:       "https://en.wikipedia.org/wiki/Test",
			Timestamp: time.Now(),
		},
	}
	saveCache(testCache)
	loadedCache := loadCache()
	entry, exists := loadedCache["en:Test"]
	if !exists {
		t.Error("The loaded cache should contain the test entry")
	}

	if entry.Summary != "This is a test article." {
		t.Errorf("Expected summary 'This is a test article.', got '%s'", entry.Summary)
	}

	delete(loadedCache, "en:Test")
	saveCache(loadedCache)
}

func TestGetAndSetCachedEntry(t *testing.T) {
	setCachedEntry("en", "TestArticle", "This is a test article.", "https://en.wikipedia.org/wiki/TestArticle")
	summary, url, found := getCachedEntry("en", "TestArticle")

	if !found {
		t.Error("The test entry should be found in the cache")
	}

	if summary != "This is a test article." {
		t.Errorf("Expected summary 'This is a test article.', got '%s'", summary)
	}

	if url != "https://en.wikipedia.org/wiki/TestArticle" {
		t.Errorf("Expected URL 'https://en.wikipedia.org/wiki/TestArticle', got '%s'", url)
	}

	if cachePath, err := getCachePath(); err == nil {
		os.Remove(cachePath)
	}
}

func TestSearchWikipedia(t *testing.T) {
	results, cached, err := searchWikipedia("en", "Berlin")

	if err != nil {
		t.Errorf("searchWikipedia should not return an error: %v", err)
	}

	if len(results) == 0 {
		t.Error("searchWikipedia should return results for 'Berlin'")
	}

	_ = cached
	foundBerlin := false
	for _, r := range results {
		if r == "Berlin" {
			foundBerlin = true
			break
		}
	}

	if !foundBerlin {
		t.Error("'Berlin' should be included in the search results")
	}
}

func TestGetWikipediaSummary(t *testing.T) {
	summary, url, cached, err := getWikipediaSummary("en", "Berlin")

	if err != nil {
		t.Errorf("getWikipediaSummary should not return an error: %v", err)
	}

	if summary == "" {
		t.Error("The summary should not be empty")
	}

	if url == "" {
		t.Error("The URL should not be empty")
	}

	if cached {
		t.Error("The first call should not come from cache")
	}

	_, _, cached, _ = getWikipediaSummary("en", "Berlin")
	if !cached {
		t.Error("The second call should come from cache")
	}
	cache := loadCache()
	delete(cache, "en:Berlin")
	saveCache(cache)
}
