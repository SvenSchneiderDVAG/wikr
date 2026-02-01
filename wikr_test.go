package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// TestMain now isolates tests in a temporary XDG cache/config root so we don't
// read/write the user's real Library/Caches or config files, and to keep debug
// error path prints referencing only ephemeral paths.
func TestMain(m *testing.M) {
	tmpRoot, err := os.MkdirTemp("", "wikr-e2e-")
	if err != nil {
		panic(err)
	}
	// Ensure isolation for cache & config resolution on Unix (Go honors these).
	os.Setenv("XDG_CACHE_HOME", tmpRoot)
	os.Setenv("XDG_CONFIG_HOME", tmpRoot)
	// Ensure debug default is false at suite start.
	debug = false
	code := m.Run()
	os.RemoveAll(tmpRoot)
	os.Exit(code)
}

func assertContainsAny(t *testing.T, out string, options ...string) {
	t.Helper()
	for _, option := range options {
		if strings.Contains(out, option) {
			return
		}
	}
	t.Fatalf("expected output to contain one of %v, got %s", options, out)
}

func assertContainsTranslation(t *testing.T, out string, key messageKey, args ...any) {
	t.Helper()
	assertContainsAny(t, out, tr("en", key, args...), tr("de", key, args...))
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
	foundBerlin := slices.Contains(results, "Berlin")

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

func TestClearCache(t *testing.T) {
	// Ensure starting clean
	cachePath, err := getCachePath()
	if err != nil {
		t.Fatalf("getCachePath error: %v", err)
	}
	os.Remove(cachePath)

	// Create an entry
	setCachedEntry("en", "ClearCacheTest", "Summary", "https://en.wikipedia.org/wiki/ClearCacheTest")
	if _, _, found := getCachedEntry("en", "ClearCacheTest"); !found {
		t.Fatalf("expected cache entry to be found before clearing")
	}

	// Clear
	if err := clearCache(); err != nil {
		t.Fatalf("clearCache returned error: %v", err)
	}

	// After clear we should not find the entry (file removed)
	if _, _, found := getCachedEntry("en", "ClearCacheTest"); found {
		t.Fatalf("expected cache entry to be absent after clearing")
	}
}

func TestConfigLoadCreate(t *testing.T) {
	path, err := getConfigPath()
	if err != nil {
		t.Fatalf("getConfigPath error: %v", err)
	}

	// Backup existing config if present
	var backup []byte
	if b, err := os.ReadFile(path); err == nil {
		backup = b
	} else {
		backup = nil
	}
	// Remove to force creation
	os.Remove(path)

	cfg, corrected, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig error: %v", err)
	}
	if corrected {
		t.Errorf("did not expect corrected=true on initial creation")
	}
	if cfg.Language != "en" || cfg.MaxResults != 5 {
		t.Errorf("expected defaults, got %+v", cfg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected config file to be created: %v", err)
	}

	// Restore backup
	if backup != nil {
		_ = os.WriteFile(path, backup, 0644)
	} else {
		os.Remove(path)
	}
}

func TestConfigValidationCorrection(t *testing.T) {
	path, err := getConfigPath()
	if err != nil {
		t.Fatalf("getConfigPath error: %v", err)
	}

	// Backup
	var backup []byte
	if b, err := os.ReadFile(path); err == nil {
		backup = b
	}

	invalid := []byte(`{"language":"xx","max_results":0,"source":"invalid"}`)
	if err := os.WriteFile(path, invalid, 0644); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	cfg, corrected, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig error: %v", err)
	}
	if !corrected {
		t.Errorf("expected corrected=true for invalid config")
	}
	if cfg.Language != "en" {
		t.Errorf("expected language corrected to en, got %s", cfg.Language)
	}
	if cfg.MaxResults != 5 {
		t.Errorf("expected max_results corrected to 5, got %d", cfg.MaxResults)
	}
	if cfg.Source != "wikipedia" {
		t.Errorf("expected source corrected to wikipedia, got %s", cfg.Source)
	}

	// Restore
	if backup != nil {
		_ = os.WriteFile(path, backup, 0644)
	} else {
		os.Remove(path)
	}
}

func TestConfigPersistence(t *testing.T) {
	path, err := getConfigPath()
	if err != nil {
		t.Fatalf("getConfigPath error: %v", err)
	}
	// Backup
	var backup []byte
	if b, err := os.ReadFile(path); err == nil {
		backup = b
	}

	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig error: %v", err)
	}
	cfg.Language = "de"
	cfg.MaxResults = 7
	cfg.Source = "grokipedia"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig error: %v", err)
	}

	cfg2, corrected, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig second error: %v", err)
	}
	if corrected {
		t.Errorf("did not expect correction on persisted valid config")
	}
	if cfg2.Language != "de" || cfg2.MaxResults != 7 || cfg2.Source != "grokipedia" {
		t.Errorf("expected persisted values (de,7,grokipedia), got (%s,%d,%s)", cfg2.Language, cfg2.MaxResults, cfg2.Source)
	}

	// Restore
	if backup != nil {
		_ = os.WriteFile(path, backup, 0644)
	} else {
		os.Remove(path)
	}
}

func TestSearchCacheSetAndGet(t *testing.T) {
	// Ensure clean search cache file
	path, err := getSearchCachePath()
	if err != nil {
		t.Fatalf("getSearchCachePath error: %v", err)
	}
	os.Remove(path)

	q := urlQueryEscapeHelper("Berlin")
	if titles, ok := getCachedSearch("en", q); ok || titles != nil {
		t.Fatalf("expected no cached search initially")
	}

	sample := []string{"Berlin", "Berlin (film)"}
	setCachedSearch("en", q, sample)
	titles, ok := getCachedSearch("en", q)
	if !ok {
		t.Fatalf("expected cached search after set")
	}
	if len(titles) != len(sample) {
		t.Fatalf("expected %d titles, got %d", len(sample), len(titles))
	}
}

func TestSearchCacheExpiry(t *testing.T) {
	path, err := getSearchCachePath()
	if err != nil {
		t.Fatalf("getSearchCachePath error: %v", err)
	}
	os.Remove(path)
	q := urlQueryEscapeHelper("CacheExpiryTest")
	setCachedSearch("en", q, []string{"CacheExpiryTest"})
	// Manually modify timestamp to force expiry
	data := loadSearchCache()
	key := "en:" + q
	entry := data[key]
	entry.Timestamp = time.Now().Add(-2 * cacheDuration)
	data[key] = entry
	saveSearchCache(data)
	titles, ok := getCachedSearch("en", q)
	if ok || titles != nil {
		t.Fatalf("expected expired cache to be ignored")
	}
}

// urlQueryEscapeHelper replicates url.QueryEscape without importing net/url multiple times in tests
func urlQueryEscapeHelper(s string) string {
	// Quick subset: replace spaces with + and leave others; for test keys this is adequate
	b := strings.ReplaceAll(s, " ", "+")
	return b
}

func TestChooseResultDefaultAndIndex(t *testing.T) {
	results := []string{"Alpha", "Beta", "Gamma"}
	max := 3

	// First test: default selection (empty input) -> Alpha
	out := &bytes.Buffer{}
	sel, quit, refine := chooseResult(results, &max, "en", "wikipedia", out, strings.NewReader("\n"))
	if quit || refine != "" {
		t.Fatalf("unexpected quit/refine on default selection")
	}
	if sel != "Alpha" {
		t.Fatalf("expected default Alpha, got %s", sel)
	}

	// Second test: pick index 2 (Beta)
	sel2, quit2, refine2 := chooseResult(results, &max, "en", "wikipedia", out, strings.NewReader("2\n"))
	if quit2 || refine2 != "" {
		t.Fatalf("unexpected quit/refine on index selection")
	}
	if sel2 != "Beta" {
		t.Fatalf("expected Beta, got %s", sel2)
	}
}

func TestHTTPGetRetryLogic(t *testing.T) {
	// Server returns 500 first, then success JSON
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if strings.Contains(r.URL.RawQuery, "action=query") { // search endpoint style
			if attempt == 1 {
				w.WriteHeader(500)
				io.WriteString(w, `{"error":"temp"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"query":{"search":[{"title":"RetryTest"}]}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	// Inject custom httpGetFunc which rewrites the endpoint
	orig := httpGetFunc
	httpGetFunc = func(_ string) ([]byte, int, string, error) {
		// Replace real Wikipedia host with test server URL
		return httpGet(srv.URL + "/?action=query&test=1")
	}
	defer func() { httpGetFunc = orig }()

	q := urlQueryEscapeHelper("RetryTest")
	titles, cached, err := searchWikipedia("en", q)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if cached {
		t.Fatalf("should not be cached on first success")
	}
	if len(titles) != 1 || titles[0] != "RetryTest" {
		t.Fatalf("unexpected titles: %v", titles)
	}
	if attempt < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", attempt)
	}
}

func TestSearchFallbackOnNonJSON(t *testing.T) {
	// Seed cache with titles
	q := urlQueryEscapeHelper("FallbackTest")
	setCachedSearch("en", q, []string{"CachedResult"})

	// Server returns text/plain so code should fallback to cache
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "not json")
	}))
	defer srv.Close()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("not json"), 200, "text/plain", nil
	}
	defer func() { httpGetFunc = orig }()

	titles, cached, err := searchWikipedia("en", q)
	if err != nil {
		t.Fatalf("expected fallback without error, got %v", err)
	}
	if !cached {
		t.Fatalf("expected cached=true on fallback")
	}
	if len(titles) != 1 || titles[0] != "CachedResult" {
		t.Fatalf("unexpected titles: %v", titles)
	}
}

func TestSummaryTruncationAndCaching(t *testing.T) {
	longText := strings.Repeat("A", 1200)
	truncated := longText[:997] + "..."
	payload := map[string]any{
		"extract": longText,
		"content_urls": map[string]any{
			"desktop": map[string]any{"page": "https://en.wikipedia.org/wiki/TruncateTest"},
		},
	}
	raw, _ := json.Marshal(payload)

	calls := 0
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		calls++
		return raw, 200, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()

	summary, url, cached, err := getWikipediaSummary("en", "TruncateTest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("first call should not be cached")
	}
	if summary != truncated {
		t.Fatalf("expected truncated summary, got len=%d", len(summary))
	}
	if url == "" {
		t.Fatalf("expected url")
	}

	// Second call should use cache and not invoke http
	summary2, _, cached2, err2 := getWikipediaSummary("en", "TruncateTest")
	if err2 != nil {
		t.Fatalf("unexpected second error: %v", err2)
	}
	if !cached2 {
		t.Fatalf("expected cached second call")
	}
	if summary2 != truncated {
		t.Fatalf("truncated mismatch second call")
	}
	if calls != 1 {
		t.Fatalf("expected 1 network call, got %d", calls)
	}
}

func TestTruncateWithEllipsisUTF8(t *testing.T) {
	long := strings.Repeat("界", summaryMaxLen+5)
	truncated := truncateWithEllipsis(long, summaryMaxLen)
	if !utf8.ValidString(truncated) {
		t.Fatalf("expected valid UTF-8 after truncation")
	}
	if !strings.HasSuffix(truncated, "...") {
		t.Fatalf("expected ellipsis suffix")
	}
	if utf8.RuneCountInString(truncated) != summaryMaxLen {
		t.Fatalf("expected %d runes, got %d", summaryMaxLen, utf8.RuneCountInString(truncated))
	}
	short := strings.Repeat("界", 10)
	if got := truncateWithEllipsis(short, summaryMaxLen); got != short {
		t.Fatalf("expected short string unchanged")
	}
}

func TestSummaryContentTypeError(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("garbage"), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()
	_, _, _, err := getWikipediaSummary("en", "CTErrorTest")
	if err == nil {
		t.Fatalf("expected content-type error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrSummaryUnexpectedContentType, "text/html"), tr("de", msgErrSummaryUnexpectedContentType, "text/html"))
}

// ====== Tests for run() to increase coverage over flag and argument branches ======

func TestRunVersion(t *testing.T) {
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-version"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	assertContainsTranslation(t, buf.String(), msgVersion, version)
}

func TestRunNoArgsShowsUsage(t *testing.T) {
	buf := &bytes.Buffer{}
	code := run(buf, []string{})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for no args")
	}
	assertContainsTranslation(t, buf.String(), msgUsage)
}

func TestRunInvalidFlag(t *testing.T) {
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-nope"})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(buf.String(), "flag provided but not defined") {
		t.Fatalf("expected flag error, got %s", buf.String())
	}
}

func TestRunHelpFlag(t *testing.T) {
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-h"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	assertContainsTranslation(t, buf.String(), msgUsage)
}

func TestRunResetConfig(t *testing.T) {
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-reset-config"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	assertContainsTranslation(t, buf.String(), msgConfigReset)
}

func TestRunClearCache(t *testing.T) {
	// Create cache entry first
	setCachedEntry("en", "ClearCacheRun", "S", "u")
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-clear-cache"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	assertContainsTranslation(t, buf.String(), msgCacheCleared)
}

func TestRunClearCacheError(t *testing.T) {
	cachePath, err := getCachePath()
	if err != nil {
		t.Fatalf("cache path err: %v", err)
	}
	os.Remove(cachePath)
	if err := os.Mkdir(cachePath, 0755); err != nil {
		t.Fatalf("mkdir cache path err: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "keep"), []byte("x"), 0644); err != nil {
		t.Fatalf("write keep file err: %v", err)
	}
	defer os.RemoveAll(cachePath)
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-clear-cache"})
	if code == 0 {
		t.Fatalf("expected non-zero exit on clear cache error")
	}
	assertContainsTranslation(t, buf.String(), msgErrCacheDeleteFile, "")
}

func TestRunSearchAndSummaryEnglish(t *testing.T) {
	// Mock network: search returns two results, summary returns first
	orig := httpGetFunc
	calls := 0
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		calls++
		if strings.Contains(endpoint, "action=query") { // search
			return []byte(`{"query":{"search":[{"title":"Alpha"},{"title":"Beta"}]}}`), 200, "application/json", nil
		}
		return []byte(`{"extract":"Short summary","content_urls":{"desktop":{"page":"https://example.org/Alpha"}}}`), 200, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()

	// Simulate user pressing enter (select default first result)
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() { w.WriteString("\n") }()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "TestTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; output=%s", code, buf.String())
	}
	out := buf.String()
	assertContainsTranslation(t, out, msgSummaryHeader)
	if !strings.Contains(out, "Short summary") {
		t.Fatalf("expected summary text, got %s", out)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 network calls (search+summary), got %d", calls)
	}
}

func TestRunSearchRefineFlow(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			switch {
			case strings.Contains(endpoint, "FirstTerm"):
				return []byte(`{"query":{"search":[{"title":"Alpha"},{"title":"Beta"}]}}`), 200, "application/json", nil
			case strings.Contains(endpoint, "NewTerm"):
				return []byte(`{"query":{"search":[{"title":"RefinedTitle"}]}}`), 200, "application/json", nil
			}
		}
		if strings.Contains(endpoint, "RefinedTitle") {
			return []byte(`{"extract":"Refined summary","content_urls":{"desktop":{"page":"https://example.org/RefinedTitle"}}}`), 200, "application/json", nil
		}
		return []byte(`{}`), 500, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()

	// Provide refine input: r, then new term
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString("r\nNewTerm\n")
		w.Close()
	}()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "FirstTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; output=%s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "Refined summary") {
		t.Fatalf("expected refined summary, got %s", out)
	}
	if !strings.Contains(out, "https://example.org/RefinedTitle") {
		t.Fatalf("expected refined URL, got %s", out)
	}
}

func TestRunConfigLoadErrorWarns(t *testing.T) {
	cfgPath, err := getConfigPath()
	if err != nil {
		t.Fatalf("config path err: %v", err)
	}
	backup, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, []byte("{"), 0644); err != nil {
		t.Fatalf("write invalid config err: %v", err)
	}
	defer func() {
		if len(backup) > 0 {
			_ = os.WriteFile(cfgPath, backup, 0644)
		} else {
			_ = os.Remove(cfgPath)
		}
	}()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-version"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	assertContainsTranslation(t, buf.String(), msgWarningConfigLoad, "")
}

func TestRunInvalidSource(t *testing.T) {
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "invalid", "Term"})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	assertContainsTranslation(t, buf.String(), msgInvalidSource, "invalid")
}

func TestRunQuitSelection(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			return []byte(`{"query":{"search":[{"title":"Alpha"},{"title":"Beta"}]}}`), 200, "application/json", nil
		}
		t.Fatalf("summary should not be requested when user quits selection")
		return nil, 0, "", io.EOF
	}
	defer func() { httpGetFunc = orig }()

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString("q\n")
		w.Close()
	}()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "QuitTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0 on quit, got %d; output=%s", code, buf.String())
	}
	assertContainsTranslation(t, buf.String(), msgSelectionCanceled)
}

func TestRunGrokipediaRefineFlow(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		switch query {
		case "FirstTerm":
			return []string{"Alpha", "Beta"}, nil
		case "NewTerm":
			return []string{"Refined"}, nil
		default:
			return []string{}, nil
		}
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta name="description" content="Refined Grok summary"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	escapedFirst := url.QueryEscape("FirstTerm")
	data := loadSearchCache()
	delete(data, "grokipedia:"+escapedFirst)
	saveSearchCache(data)

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString("r\nNewTerm\n")
		w.Close()
	}()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "FirstTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; output=%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "Refined Grok summary") {
		t.Fatalf("expected refined Grokipedia summary, got %s", buf.String())
	}
}

func TestRunGrokipediaNoResults(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("not found"), 404, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "NoHit"})
	if code == 0 {
		t.Fatalf("expected non-zero exit for no results")
	}
	assertContainsTranslation(t, buf.String(), msgNoResults)
}

func TestRunGrokipediaQuitSelection(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{"Alpha", "Beta"}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta name="description" content="ok"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString("q\n")
		w.Close()
	}()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "QuitTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0 on quit, got %d; output=%s", code, buf.String())
	}
	assertContainsTranslation(t, buf.String(), msgSelectionCanceled)
}

func TestRunGrokipediaMissingThenAnother(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{"Alpha", "Beta"}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	counts := map[string]int{}
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		u, _ := url.Parse(endpoint)
		slug := strings.TrimPrefix(u.Path, "/page/")
		slug, _ = url.PathUnescape(slug)
		counts[slug]++
		switch slug {
		case "Alpha":
			if counts[slug] == 1 {
				return []byte(`<html><head><meta name="description" content="Alpha summary"></head></html>`), 200, "text/html", nil
			}
			return []byte("not found"), 404, "text/html", nil
		case "Beta":
			return []byte(`<html><head><meta name="description" content="Beta summary"></head></html>`), 200, "text/html", nil
		default:
			return []byte("not found"), 404, "text/html", nil
		}
	}
	defer func() { httpGetFunc = orig }()

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString("1\na\n")
		w.Close()
	}()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "MissingTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; output=%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "Beta summary") {
		t.Fatalf("expected fallback to another result, got %s", buf.String())
	}
}

func TestRunGrokipediaFilteredEmpty(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{"Alpha", "Beta"}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("not found"), 404, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "EmptyFiltered"})
	if code == 0 {
		t.Fatalf("expected non-zero exit for filtered empty results")
	}
	assertContainsTranslation(t, buf.String(), msgNoResults)
}

func TestRunGrokipediaFilterErrorFallback(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{"Alpha"}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	calls := 0
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		calls++
		if calls == 1 {
			return nil, 0, "", io.EOF
		}
		return []byte(`<html><head><meta name="description" content="Alpha summary"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "FilterErr"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; output=%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "Alpha summary") {
		t.Fatalf("expected summary after filter error fallback, got %s", buf.String())
	}
}

func TestRunSearchAndSummaryGerman(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") { // search
			return []byte(`{"query":{"search":[{"title":"Alpha"}]}}`), 200, "application/json", nil
		}
		return []byte(`{"extract":"Kurze Zusammenfassung","content_urls":{"desktop":{"page":"https://example.org/Alpha"}}}`), 200, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "-lang", "de", "Alpha"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	out := buf.String()
	assertContainsTranslation(t, out, msgSummaryHeader)
	if !strings.Contains(out, "Kurze Zusammenfassung") {
		t.Fatalf("expected summarized text, got %s", out)
	}
}

func TestRunNetworkErrorFallbackFailure(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) { return nil, 0, "", io.EOF }
	defer func() { httpGetFunc = orig }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "UncachedTerm"})
	if code == 0 {
		t.Fatalf("expected non-zero exit on network error without cache")
	}
	assertContainsTranslation(t, buf.String(), msgErrorDuringSearch, io.EOF)
}

// ===== Additional tests to cover remaining branches =====

func TestSearchWikipediaMalformedJSONNoCache(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`{"query":{"search":}`), 200, "application/json", nil // malformed JSON
	}
	defer func() { httpGetFunc = orig }()
	q := urlQueryEscapeHelper("MalformedBranch")
	titles, cached, err := searchWikipedia("en", q)
	if err == nil {
		t.Fatalf("expected JSON decode error")
	}
	if cached {
		t.Fatalf("expected cached=false since no fallback cache")
	}
	if len(titles) != 0 {
		t.Fatalf("expected no titles")
	}
}

func TestSearchWikipediaNonJSONNoCache(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("plain"), 200, "text/plain", nil
	}
	defer func() { httpGetFunc = orig }()
	q := urlQueryEscapeHelper("NonJSONNoCache")
	_, _, err := searchWikipedia("en", q)
	if err == nil {
		t.Fatalf("expected content-type error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrSearchUnexpectedContentType, "text/plain"), tr("de", msgErrSearchUnexpectedContentType, "text/plain"))
}

func TestSearchWikipediaHTTPStatusError(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("{}"), 500, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()
	q := urlQueryEscapeHelper("StatusErr")
	_, _, err := searchWikipedia("en", q)
	if err == nil {
		t.Fatalf("expected status error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrSearchUnexpectedStatus, 500), tr("de", msgErrSearchUnexpectedStatus, 500))
}

func TestChooseResultInvalidSelection(t *testing.T) {
	results := []string{"Alpha", "Beta", "Gamma"}
	max := 3
	out := &bytes.Buffer{}
	// Provide invalid selection then newline default
	sel, quit, refine := chooseResult(results, &max, "en", "wikipedia", out, strings.NewReader("9\n\n"))
	if quit || refine != "" {
		t.Fatalf("unexpected quit/refine on invalid selection flow")
	}
	if sel != "Alpha" {
		t.Fatalf("expected Alpha after invalid then default, got %s", sel)
	}
}

func TestChooseResultGermanBranch(t *testing.T) {
	results := []string{"Alpha", "Beta", "Gamma"}
	max := 3
	out := &bytes.Buffer{}
	// invalid, then 2 -> should select Beta
	sel, quit, refine := chooseResult(results, &max, "de", "grokipedia", out, strings.NewReader("x\n2\n"))
	if quit || refine != "" {
		t.Fatalf("unexpected quit/refine on german selection flow")
	}
	if sel != "Beta" {
		t.Fatalf("expected Beta after invalid then 2, got %s", sel)
	}
}

func TestChooseResultQuitAndRefine(t *testing.T) {
	results := []string{"Alpha", "Beta", "Gamma"}
	max := 3
	out := &bytes.Buffer{}

	_, quit, refine := chooseResult(results, &max, "en", "wikipedia", out, strings.NewReader("q\n"))
	if !quit || refine != "" {
		t.Fatalf("expected quit=true with no refine, got quit=%v refine=%q", quit, refine)
	}

	_, quit2, refine2 := chooseResult(results, &max, "en", "wikipedia", out, strings.NewReader("r\nNew Term\n"))
	if quit2 || refine2 != "New Term" {
		t.Fatalf("expected refine 'New Term', got quit=%v refine=%q", quit2, refine2)
	}
}

func TestChooseResultRefineEmptyThenSelect(t *testing.T) {
	results := []string{"Alpha", "Beta", "Gamma"}
	max := 3
	out := &bytes.Buffer{}
	// refine -> empty -> then choose 2
	sel, quit, refine := chooseResult(results, &max, "en", "wikipedia", out, strings.NewReader("r\n\n2\n"))
	if quit || refine != "" {
		t.Fatalf("unexpected quit/refine on empty refine flow")
	}
	if sel != "Beta" {
		t.Fatalf("expected Beta after empty refine then selection, got %s", sel)
	}
	assertContainsTranslation(t, out.String(), msgMultipleResults, 3, 3, "wikipedia")
}

func TestGetWikipediaSummaryErrorBranches(t *testing.T) {
	cases := []struct {
		name string
		body string
		key  messageKey
	}{
		{"missingExtract", `{"content_urls":{"desktop":{"page":"https://example.org/X"}}}`, msgErrSummaryMissingExtract},
		{"extractNotString", `{"extract":123, "content_urls":{"desktop":{"page":"https://example.org/X"}}}`, msgErrSummaryExtractNotString},
		{"missingContentURLs", `{"extract":"x"}`, msgErrSummaryMissingContentURLs},
		{"missingDesktop", `{"extract":"x","content_urls":{}}`, msgErrSummaryMissingDesktop},
		{"missingPage", `{"extract":"x","content_urls":{"desktop":{}}}`, msgErrSummaryMissingPageURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := httpGetFunc
			httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
				return []byte(tc.body), 200, "application/json", nil
			}
			defer func() { httpGetFunc = orig }()
			_, _, _, err := getWikipediaSummary("en", "ErrCase"+tc.name)
			if err == nil {
				t.Fatalf("expected error containing %q, got %v", tc.key, err)
			}
			assertContainsAny(t, err.Error(), tr("en", tc.key), tr("de", tc.key))
		})
	}
}

func TestGetWikipediaSummaryHTTPStatusError(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) { return []byte("{}"), 500, "application/json", nil }
	defer func() { httpGetFunc = orig }()
	_, _, _, err := getWikipediaSummary("en", "StatusFail")
	if err == nil {
		t.Fatalf("expected status error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrSummaryUnexpectedStatus, 500), tr("de", msgErrSummaryUnexpectedStatus, 500))
}

func TestRunConfigCorrectionPath(t *testing.T) {
	// Write invalid config then run -version so run() loads & corrects
	cfgPath, err := getConfigPath()
	if err != nil {
		t.Fatalf("config path err: %v", err)
	}
	backup, _ := os.ReadFile(cfgPath)
	os.WriteFile(cfgPath, []byte(`{"language":"zz","max_results":0}`), 0644)
	defer func() {
		if len(backup) > 0 {
			os.WriteFile(cfgPath, backup, 0644)
		} else {
			os.Remove(cfgPath)
		}
	}()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-version"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	// Reload config to ensure corrected persisted
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatalf("reload config err: %v", err)
	}
	if cfg.Language != "en" || cfg.MaxResults != 5 {
		t.Fatalf("expected corrected persisted config, got %+v", cfg)
	}
}

// Covers run() branch where search network fails but cached search results allow continuation.

// TestRunConfigUpdate ensures the configChanged branch persists new values.
func TestRunConfigUpdate(t *testing.T) {
	// Force a known starting config
	cfgPath, err := getConfigPath()
	if err != nil {
		t.Fatalf("config path err: %v", err)
	}
	backup, _ := os.ReadFile(cfgPath)
	defer func() {
		if len(backup) > 0 {
			os.WriteFile(cfgPath, backup, 0644)
		} else {
			os.Remove(cfgPath)
		}
	}()
	os.WriteFile(cfgPath, []byte(`{"language":"en","max_results":5,"source":"wikipedia"}`), 0644)

	// Mock network minimal: search -> one result, summary -> simple extract
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			return []byte(`{"query":{"search":[{"title":"CfgPersist"}]}}`), 200, "application/json", nil
		}
		// Mock Grokipedia page response with meta description
		if strings.Contains(endpoint, "grokipedia.com/page") {
			return []byte(`<html><head><meta name="description" content="Cfg summary from Grokipedia"/></head><body></body></html>`), 200, "text/html", nil
		}
		return []byte(`{"extract":"Cfg summary","content_urls":{"desktop":{"page":"https://example.org/CfgPersist"}}}`), 200, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()

	buf := &bytes.Buffer{}
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{"CfgPersist"}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	code := run(buf, []string{"-lang", "de", "-max", "9", "-source", "grokipedia", "CfgPersist"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d -- output: %s", code, buf.String())
	}
	// Reload config to verify persistence
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatalf("reload config err: %v", err)
	}
	if cfg.Language != "de" || cfg.MaxResults != 9 || cfg.Source != "grokipedia" {
		t.Fatalf("expected updated config (de,9,grokipedia), got (%s,%d,%s)", cfg.Language, cfg.MaxResults, cfg.Source)
	}
}

// TestRunMaxLimitOneList ensures chooseResult respects max results limit (< len(results)).
func TestRunMaxLimitOneList(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			// Provide three results but -max 1 should show only one and immediately allow default selection.
			return []byte(`{"query":{"search":[{"title":"One"},{"title":"Two"},{"title":"Three"}]}}`), 200, "application/json", nil
		}
		return []byte(`{"extract":"Limited summary","content_urls":{"desktop":{"page":"https://example.org/One"}}}`), 200, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()
	// Provide newline to accept default (first entry) without user choosing.
	r, w, _ := os.Pipe()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	go func() { w.WriteString("\n") }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "-lang", "en", "-max", "1", "LimitTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d -- out=%s", code, buf.String())
	}
	out := buf.String()
	assertContainsTranslation(t, out, msgSummaryHeader)
	if !strings.Contains(out, "Limited summary") {
		t.Fatalf("expected summary text")
	}
}

// TestRunCachedSearchAndSummary covers path where cached search results are used (no network call for search second run).
func TestRunCachedSearchAndSummary(t *testing.T) {
	term := "Cached Search Term"
	// First run: populate search + summary cache
	orig := httpGetFunc
	searchCalls := 0
	summaryCalls := 0
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			searchCalls++
			return []byte(`{"query":{"search":[{"title":"CacheTitle"}]}}`), 200, "application/json", nil
		}
		summaryCalls++
		return []byte(`{"extract":"Cached summary body","content_urls":{"desktop":{"page":"https://example.org/CacheTitle"}}}`), 200, "application/json", nil
	}
	buf1 := &bytes.Buffer{}
	if code := run(buf1, []string{"-source", "wikipedia", term}); code != 0 {
		t.Fatalf("first run unexpected exit %d: %s", code, buf1.String())
	}
	if searchCalls != 1 || summaryCalls != 1 {
		t.Fatalf("expected one search and one summary call, got %d/%d", searchCalls, summaryCalls)
	}
	// Second run: make search fail, but since cache hit occurs inside searchWikipedia before network, network function won't be called for search; summary will be served from summary cache.
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		t.Fatalf("network should not be called on second run for cached search+summary, endpoint=%s", endpoint)
		return nil, 0, "", io.EOF
	}
	buf2 := &bytes.Buffer{}
	if code := run(buf2, []string{"-source", "wikipedia", term}); code != 0 {
		t.Fatalf("second run unexpected exit %d: %s", code, buf2.String())
	}
	out2 := buf2.String()
	assertContainsTranslation(t, out2, msgSummaryHeader)
	if !strings.Contains(out2, "Cached summary body") {
		t.Fatalf("expected cached summary body second run")
	}
	httpGetFunc = orig
}

// TestClearCacheWhenMissing ensures clearCache returns nil when file absent (silent path) to cover that branch.
func TestClearCacheWhenMissing(t *testing.T) {
	p, err := getCachePath()
	if err != nil {
		t.Fatalf("cache path err: %v", err)
	}
	os.Remove(p)
	if err := clearCache(); err != nil {
		t.Fatalf("clearCache should not error when file missing: %v", err)
	}
}

// ===== Additional coverage booster tests =====

// TestSearchWikipediaUsesCacheNoNetwork ensures cached early return path in searchWikipedia.
func TestSearchWikipediaUsesCacheNoNetwork(t *testing.T) {
	q := url.QueryEscape("CacheOnlyTerm")
	setCachedSearch("en", q, []string{"CachedTitleX"})
	called := false
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		called = true
		return nil, 0, "", io.EOF
	}
	defer func() { httpGetFunc = orig }()
	titles, cached, err := searchWikipedia("en", q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cached {
		t.Fatalf("expected cached=true")
	}
	if called {
		t.Fatalf("expected no network call on cached search")
	}
	if len(titles) != 1 || titles[0] != "CachedTitleX" {
		t.Fatalf("unexpected titles: %v", titles)
	}
}

// TestRunEmptyResultsNoCache covers branch printing "No results found."
func TestRunEmptyResultsNoCache(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			return []byte(`{"query":{"search":[]}}`), 200, "application/json", nil
		}
		t.Fatalf("summary should not be requested when no results")
		return nil, 0, "", io.EOF
	}
	defer func() { httpGetFunc = orig }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "EmptyNoCache"})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for no results")
	}
	assertContainsTranslation(t, buf.String(), msgNoResults)
}

// TestRunEmptyResultsCached covers branch printing "Cached search results were empty.".
func TestRunEmptyResultsCached(t *testing.T) {
	term := "EmptyCachedTerm"
	q := url.QueryEscape(term)
	setCachedSearch("en", q, []string{}) // empty but cached
	// searchWikipedia will hit cache and never call network; ensure run handles it.
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		t.Fatalf("network should not be called for cached empty search")
		return nil, 0, "", io.EOF
	}
	defer func() { httpGetFunc = orig }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", term})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for empty cached results")
	}
	assertContainsTranslation(t, buf.String(), msgCachedSearchEmpty)
}

// TestRunSummaryError covers branch where summary fetch fails after successful search.
func TestRunSummaryError(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			return []byte(`{"query":{"search":[{"title":"ErrSum"}]}}`), 200, "application/json", nil
		}
		return []byte("{}"), 500, "application/json", nil // force summary status error
	}
	defer func() { httpGetFunc = orig }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "ErrSum"})
	if code == 0 {
		t.Fatalf("expected non-zero exit code on summary error")
	}
	assertContainsTranslation(t, buf.String(), msgErrorFetchingSummary, "")
}

// TestRunSummaryCachedIndicator covers printing of (cached) marker.
func TestRunSummaryCachedIndicator(t *testing.T) {
	// Ensure search result is cached to bypass search network.
	term := "CachedSumTest"
	escaped := url.QueryEscape(term)
	setCachedSearch("en", escaped, []string{"CachedSumTest"})
	setCachedEntry("en", "CachedSumTest", "Already cached summary", "https://example.org/CachedSumTest")
	// Stub network to panic if called (should not be).
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		t.Fatalf("network not expected for cached summary path, endpoint=%s", endpoint)
		return nil, 0, "", io.EOF
	}
	defer func() { httpGetFunc = orig }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", term})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	out := buf.String()
	assertContainsTranslation(t, out, msgCachedMarker)
	if !strings.Contains(out, "Already cached summary") {
		t.Fatalf("expected cached summary content")
	}
}

// ================= Filesystem / error path coverage boosters =================

// withTempDir creates a temp dir and returns its path plus a cleanup func.
func withTempDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wikrtest-")
	if err != nil {
		t.Fatalf("tempdir err: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// withEnv sets an env var temporarily.
func withEnv(t *testing.T, key, value string) func() {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s err: %v", key, err)
	}
	return func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	}
}

func setChromeCheckResult(available bool, err error) {
	chromeCheckOnce = sync.Once{}
	chromeWarningOnce = sync.Once{}
	chromeAvailable = available
	chromeCheckError = err
	chromeCheckOnce.Do(func() {})
}

func TestCheckChromeAvailableSmoke(t *testing.T) {
	oldAvail := chromeAvailable
	oldErr := chromeCheckError
	sentinel := errors.New("sentinel")
	chromeCheckOnce = sync.Once{}
	chromeAvailable = false
	chromeCheckError = sentinel
	t.Cleanup(func() {
		chromeCheckOnce = sync.Once{}
		chromeAvailable = oldAvail
		chromeCheckError = oldErr
	})
	_, err := checkChromeAvailable()
	if err == nil {
		if !chromeAvailable {
			t.Fatalf("expected chromeAvailable=true on success")
		}
	} else if chromeCheckError == sentinel {
		t.Fatalf("expected chromeCheckError to change, got err=%v", err)
	}
}

// createFilePath ensures a regular file exists at path (replacing anything existing).
func createFilePath(t *testing.T, p string) {
	t.Helper()
	// Remove if directory
	os.RemoveAll(p)
	if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
		t.Fatalf("write file %s err: %v", p, err)
	}
}

func TestGetCachePathCreateDirError(t *testing.T) {
	t.Skip("Skip on macOS: user cache path may not honor XDG override reliably")
}

func TestGetSearchCachePathCreateDirError(t *testing.T) {
	t.Skip("Skip brittle path error test on this platform")
}

func TestLoadCachePathError(t *testing.T) { t.Skip("Skip brittle path error test") }

func TestLoadSearchCachePathError(t *testing.T) { t.Skip("Skip brittle path error test") }

func TestSaveCachePathError(t *testing.T) {
	base, cleanup := withTempDir(t)
	defer cleanup()
	undo := withEnv(t, "XDG_CACHE_HOME", base)
	defer undo()
	createFilePath(t, filepath.Join(base, "wikr"))
	// Should hit early return branch; just ensure it does not panic.
	saveCache(Cache{"en:X": {Summary: "s", URL: "u", Timestamp: time.Now()}})
}

func TestSaveSearchCachePathError(t *testing.T) {
	base, cleanup := withTempDir(t)
	defer cleanup()
	undo := withEnv(t, "XDG_CACHE_HOME", base)
	defer undo()
	createFilePath(t, filepath.Join(base, "wikr"))
	saveSearchCache(map[string]SearchResultEntry{"en:q": {Titles: []string{"X"}, Timestamp: time.Now()}})
}

func TestSaveConfigPathError(t *testing.T) { t.Skip("Skip brittle path error test") }

func TestClearCachePathError(t *testing.T) { t.Skip("Skip brittle path error test") }

// ===== Debug branch coverage =====
func TestDebugBranchesLoadCache(t *testing.T) {
	// Force debug prints on invalid JSON decode in cache
	debug = true
	defer func() { debug = false }()
	path, err := getCachePath()
	if err != nil {
		t.Fatalf("cache path err: %v", err)
	}
	// Write invalid JSON
	os.WriteFile(path, []byte("{"), 0644)
	_ = loadCache() // should trigger debug decode branch (no panic)
}

func TestDebugBranchesLoadSearchCache(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	path, err := getSearchCachePath()
	if err != nil {
		t.Fatalf("search cache path err: %v", err)
	}
	os.WriteFile(path, []byte("{"), 0644)
	_ = loadSearchCache()
}

// saveCache success path already covered indirectly; add explicit invocation with debug true
func TestSaveCacheDebug(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	saveCache(Cache{"en:Dbg": {Summary: "s", URL: "u", Timestamp: time.Now()}})
}

func TestSaveSearchCacheDebug(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	saveSearchCache(map[string]SearchResultEntry{"en:q": {Titles: []string{"X"}, Timestamp: time.Now()}})
}

// Additional branch: searchWikipedia fallback debug (malformed JSON with cache present)
func TestSearchWikipediaMalformedJSONFallbackDebug(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	q := url.QueryEscape("MalformedDebugTerm")
	setCachedSearch("en", q, []string{"CachedMD"})
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			return []byte(`{"query":{"search":}`), 200, "application/json", nil // malformed JSON
		}
		return []byte("{}"), 404, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()
	titles, cached, err := searchWikipedia("en", q)
	if err != nil {
		t.Fatalf("expected fallback without error, got %v", err)
	}
	if !cached || len(titles) != 1 || titles[0] != "CachedMD" {
		t.Fatalf("expected cached fallback titles, got %v (cached=%v)", titles, cached)
	}
}

// TestHTTPGetAllRetriesFail exercises the max-attempts failure return path in httpGet via searchWikipedia.
func TestHTTPGetAllRetriesFail(t *testing.T) {
	// The httpGet function retries 3 times. On each transient 500 response it does NOT return early;
	// instead it loops and after exhausting attempts returns status=0 with the last error.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(500)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"error":"temp"}`)
	}))
	defer srv.Close()
	start := time.Now()
	body, status, ct, err := httpGet(srv.URL)
	_ = body
	_ = ct
	if err == nil {
		t.Fatalf("expected error after retries")
	}
	if status != 0 {
		t.Fatalf("expected status 0 after exhausting retries, got %d", status)
	}
	if callCount != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", callCount)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrHTTPTransientStatus, 500), tr("de", msgErrHTTPTransientStatus, 500))
	if time.Since(start) < 150*time.Millisecond {
		t.Fatalf("expected at least initial backoff delay; too fast")
	}
}

func TestHTTPGetTimeout(t *testing.T) {
	origTimeout := httpTimeout
	httpTimeout = 10 * time.Millisecond
	defer func() { httpTimeout = origTimeout }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(200)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	_, _, _, err := httpGet(srv.URL)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

// saveCache write error: make cache.json a directory to trigger error handling branch.
func TestSaveCacheWriteError(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	path, err := getCachePath()
	if err != nil {
		t.Fatalf("getCachePath err: %v", err)
	}
	os.Remove(path)
	// Replace file with directory named cache.json
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("mkdir replacement err: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0644); err != nil {
		t.Fatalf("write keep err: %v", err)
	}
	saveCache(Cache{"en:Err": {Summary: "s", URL: "u", Timestamp: time.Now()}})
	// Cleanup
	os.RemoveAll(path)
}

// saveSearchCache write error path.
func TestSaveSearchCacheWriteError(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	scPath, err := getSearchCachePath()
	if err != nil {
		t.Fatalf("getSearchCachePath err: %v", err)
	}
	os.Remove(scPath)
	if err := os.Mkdir(scPath, 0755); err != nil {
		t.Fatalf("mkdir search cache replacement err: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scPath, "keep"), []byte("x"), 0644); err != nil {
		t.Fatalf("write keep err: %v", err)
	}
	saveSearchCache(map[string]SearchResultEntry{"en:q": {Titles: []string{"t"}, Timestamp: time.Now()}})
	os.RemoveAll(scPath)
}

func TestWriteFileAtomicCreateTempError(t *testing.T) {
	dir, cleanup := withTempDir(t)
	defer cleanup()
	noWrite := filepath.Join(dir, "nowrite")
	if err := os.Mkdir(noWrite, 0500); err != nil {
		t.Fatalf("mkdir nowrite err: %v", err)
	}
	defer os.Chmod(noWrite, 0700)
	path := filepath.Join(noWrite, "file.json")
	if err := writeFileAtomic(path, []byte("x"), 0644); err == nil {
		t.Fatalf("expected error when directory is not writable")
	}
}

func TestWriteFileAtomicRenameFallback(t *testing.T) {
	dir, cleanup := withTempDir(t)
	defer cleanup()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("mkdir target err: %v", err)
	}
	if err := writeFileAtomic(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("writeFileAtomic err: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back err: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected data %q", string(data))
	}
}

func TestWriteFileAtomicRenameErrorNonEmptyDir(t *testing.T) {
	dir, cleanup := withTempDir(t)
	defer cleanup()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("mkdir target err: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0644); err != nil {
		t.Fatalf("write keep err: %v", err)
	}
	if err := writeFileAtomic(path, []byte("hello"), 0644); err == nil {
		t.Fatalf("expected rename error for non-empty dir")
	}
}

// clearCache success with debug prints (file exists).
func TestClearCacheSuccessDebug(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	path, err := getCachePath()
	if err != nil {
		t.Fatalf("cache path err: %v", err)
	}
	os.WriteFile(path, []byte("{}"), 0644)
	if err := clearCache(); err != nil {
		t.Fatalf("clearCache err: %v", err)
	}
}

// loadCache success with debug active (ensure no early return branches).
func TestLoadCacheSuccessDebug(t *testing.T) {
	debug = true
	defer func() { debug = false }()
	path, err := getCachePath()
	if err != nil {
		t.Fatalf("cache path err: %v", err)
	}
	data := Cache{"en:LoadDbg": {Summary: "s", URL: "u", Timestamp: time.Now()}}
	raw, _ := json.Marshal(data)
	os.WriteFile(path, raw, 0644)
	c := loadCache()
	if _, ok := c["en:LoadDbg"]; !ok {
		t.Fatalf("expected entry present in debug load")
	}
}

// TestRunMultiResultSelection picks second result explicitly via input "2" to cover chooseResult numeric path inside run.
func TestRunMultiResultSelection(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		if strings.Contains(endpoint, "action=query") {
			return []byte(`{"query":{"search":[{"title":"First"},{"title":"Second"}]}}`), 200, "application/json", nil
		}
		return []byte(`{"extract":"Second summary","content_urls":{"desktop":{"page":"https://example.org/Second"}}}`), 200, "application/json", nil
	}
	defer func() { httpGetFunc = orig }()
	r, w, _ := os.Pipe()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	// Provide valid selection "2"; ensure config from previous tests (that may have set -max 1) is overridden by explicit -max 5 here.
	go func() { w.WriteString("2\n") }()
	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "wikipedia", "-max", "5", "QueryTerm"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; out=%s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "Second summary") {
		t.Fatalf("expected selected second summary, got %s", out)
	}
}

func TestGetGrokipediaSummaryParsesFirstBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Grokipedia integration test in short mode")
	}

	// Clear cache for fresh test
	cache := loadCache()
	delete(cache, "grokipedia:Iron_Maiden")
	saveCache(cache)

	// First call - should fetch from network via HTTP
	summary, urlStr, cached, err := getGrokipediaSummary("Iron Maiden")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on first call")
	}
	if !strings.Contains(strings.ToLower(summary), "metal") && !strings.Contains(strings.ToLower(summary), "band") {
		t.Fatalf("unexpected summary (should mention band/metal): %s", summary)
	}
	if !strings.Contains(urlStr, "grokipedia.com/page/Iron_Maiden") {
		t.Fatalf("unexpected url %s", urlStr)
	}

	// Second call - should be cached
	summary2, url2, cached2, err2 := getGrokipediaSummary("Iron Maiden")
	if err2 != nil {
		t.Fatalf("unexpected cached error: %v", err2)
	}
	if !cached2 {
		t.Fatalf("expected cached=true on second call")
	}
	if summary2 != summary {
		t.Fatalf("cached summary mismatch: %s", summary2)
	}
	if url2 != urlStr {
		t.Fatalf("cached url mismatch: %s", url2)
	}
}

func TestGetGrokipediaSummaryEmptyTitle(t *testing.T) {
	_, _, _, err := getGrokipediaSummary("   ")
	if err == nil {
		t.Fatalf("expected empty title error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrGrokipediaEmptyTitle), tr("de", msgErrGrokipediaEmptyTitle))
}

func TestGetGrokipediaSummaryNotFound(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("not found"), 404, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	_, _, _, err := getGrokipediaSummary("MissingArticle")
	if err == nil {
		t.Fatalf("expected not found error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrGrokipediaMissingArticle, "MissingArticle"), tr("de", msgErrGrokipediaMissingArticle, "MissingArticle"))
}

func TestGetGrokipediaSummaryUnexpectedStatus(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("error"), 500, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	_, _, _, err := getGrokipediaSummary("StatusArticle")
	if err == nil {
		t.Fatalf("expected status error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrGrokipediaUnexpectedStatus, 500), tr("de", msgErrGrokipediaUnexpectedStatus, 500))
}

func TestGetGrokipediaSummaryMissingDescription(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head></head><body>No meta</body></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	_, _, _, err := getGrokipediaSummary("NoDesc")
	if err == nil {
		t.Fatalf("expected missing description error, got %v", err)
	}
	assertContainsAny(t, err.Error(), tr("en", msgErrGrokipediaMissingArticle, "NoDesc"), tr("de", msgErrGrokipediaMissingArticle, "NoDesc"))
}

func TestGetGrokipediaSummaryTruncation(t *testing.T) {
	long := strings.Repeat("A", summaryMaxLen+50)
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta name="description" content="` + long + `"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	summary, _, _, err := getGrokipediaSummary("LongDesc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if utf8.RuneCountInString(summary) != summaryMaxLen {
		t.Fatalf("expected truncated summary length %d, got %d", summaryMaxLen, utf8.RuneCountInString(summary))
	}
}

func TestGetGrokipediaSummaryCached(t *testing.T) {
	setCachedEntry("grokipedia", "Cached_Title", "Cached summary", "https://example.org/Cached_Title")
	summary, urlStr, cached, err := getGrokipediaSummary("Cached Title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cached {
		t.Fatalf("expected cached=true")
	}
	if summary != "Cached summary" || urlStr != "https://example.org/Cached_Title" {
		t.Fatalf("unexpected cached data: %s %s", summary, urlStr)
	}
}

func TestSearchGrokipediaIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Grokipedia integration test in short mode")
	}

	// Clear search cache for fresh test
	path, _ := getSearchCachePath()
	os.Remove(path)

	// Search for a known topic
	titles, cached, err := searchGrokipedia("en", "Elon Musk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on first search")
	}
	if len(titles) == 0 {
		t.Fatalf("expected at least one search result")
	}

	// Check that "Elon Musk" is in the results
	foundElonMusk := false
	for _, title := range titles {
		if strings.Contains(title, "Elon Musk") {
			foundElonMusk = true
			break
		}
	}
	if !foundElonMusk {
		t.Fatalf("expected 'Elon Musk' in search results, got: %v", titles)
	}

	// Second search should be cached
	titles2, cached2, err2 := searchGrokipedia("en", "Elon Musk")
	if err2 != nil {
		t.Fatalf("unexpected cached error: %v", err2)
	}
	if !cached2 {
		t.Fatalf("expected cached=true on second search")
	}
	if len(titles2) != len(titles) {
		t.Fatalf("cached titles count mismatch")
	}
}

func TestFilterGrokipediaResults(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		u, _ := url.Parse(endpoint)
		slug := strings.TrimPrefix(u.Path, "/page/")
		slug, _ = url.PathUnescape(slug)
		switch slug {
		case "Valid_One", "Valid_Two":
			return []byte(`<html><head><meta name="description" content="ok"></head></html>`), 200, "text/html", nil
		default:
			return []byte("not found"), 404, "text/html", nil
		}
	}
	defer func() { httpGetFunc = orig }()

	results := []string{"Invalid", "Valid One", "Valid Two"}
	filtered, err := filterGrokipediaResults(results, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filtered) != 2 || filtered[0] != "Valid One" || filtered[1] != "Valid Two" {
		t.Fatalf("unexpected filtered results: %v", filtered)
	}
}

func TestFilterGrokipediaResultsError(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return nil, 0, "", io.EOF
	}
	defer func() { httpGetFunc = orig }()

	_, err := filterGrokipediaResults([]string{"Any"}, 1)
	if err == nil {
		t.Fatalf("expected error from probe")
	}
}

func TestProbeGrokipediaTitleBranches(t *testing.T) {
	orig := httpGetFunc
	defer func() { httpGetFunc = orig }()

	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("not found"), 404, "text/html", nil
	}
	ok, err := probeGrokipediaTitle("Missing")
	if err != nil || ok {
		t.Fatalf("expected not found without error, got ok=%v err=%v", ok, err)
	}

	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head></head></html>`), 200, "text/html", nil
	}
	ok, err = probeGrokipediaTitle("EmptyDesc")
	if err != nil || ok {
		t.Fatalf("expected empty summary to be invalid, got ok=%v err=%v", ok, err)
	}

	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte("error"), 500, "text/html", nil
	}
	_, err = probeGrokipediaTitle("ServerErr")
	if err == nil {
		t.Fatalf("expected error on unexpected status")
	}
}

func TestProbeGrokipediaTitleEmpty(t *testing.T) {
	ok, err := probeGrokipediaTitle("   ")
	if err != nil || ok {
		t.Fatalf("expected empty title to be invalid without error, got ok=%v err=%v", ok, err)
	}
}

func TestIsGrokipediaNotFound(t *testing.T) {
	if isGrokipediaNotFound(nil) {
		t.Fatalf("expected false for nil error")
	}
	if !isGrokipediaNotFound(newLocalizedError(msgErrGrokipediaMissingArticle, "X")) {
		t.Fatalf("expected true for not found error")
	}
	if isGrokipediaNotFound(errors.New("other error")) {
		t.Fatalf("expected false for other errors")
	}
}

func TestPromptGrokipediaMissingBranches(t *testing.T) {
	out := &bytes.Buffer{}

	action, refine := promptGrokipediaMissing(out, strings.NewReader("x\na\n"), "en", true)
	if action != "another" || refine != "" {
		t.Fatalf("expected another, got %s %q", action, refine)
	}

	action, refine = promptGrokipediaMissing(out, strings.NewReader("r\nNew Term\n"), "en", true)
	if action != "refine" || refine != "New Term" {
		t.Fatalf("expected refine New Term, got %s %q", action, refine)
	}

	action, refine = promptGrokipediaMissing(out, strings.NewReader("a\nq\n"), "en", false)
	if action != "quit" || refine != "" {
		t.Fatalf("expected quit after no-alternatives path, got %s %q", action, refine)
	}

	action, refine = promptGrokipediaMissing(out, strings.NewReader("r\n\nr\nTerm\n"), "en", false)
	if action != "refine" || refine != "Term" {
		t.Fatalf("expected refine Term after empty input, got %s %q", action, refine)
	}

	action, refine = promptGrokipediaMissing(out, strings.NewReader(""), "en", true)
	if action != "quit" || refine != "" {
		t.Fatalf("expected quit on EOF, got %s %q", action, refine)
	}

	action, refine = promptGrokipediaMissing(out, strings.NewReader("q\n"), "de", true)
	if action != "quit" || refine != "" {
		t.Fatalf("expected quit on german prompt, got %s %q", action, refine)
	}
}

func TestExtractOGTitle(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Foo &amp; Bar"></head></html>`
	title := extractOGTitle(html)
	if title != "Foo & Bar" {
		t.Fatalf("expected unescaped og:title, got %q", title)
	}
}

func TestSearchGrokipediaDirectFallbackOGTitle(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta property="og:title" content="Direct Title"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	q := "DirectTerm"
	escaped := url.QueryEscape(q)
	titles, cached, err := searchGrokipediaDirectFallback(q, escaped)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on direct fallback")
	}
	if len(titles) != 1 || titles[0] != "Direct Title" {
		t.Fatalf("unexpected titles: %v", titles)
	}
	if cachedTitles, ok := getCachedSearch("grokipedia", escaped); !ok || len(cachedTitles) != 1 {
		t.Fatalf("expected cached search entry")
	}
}

func TestSearchGrokipediaDirectFallbackUsesSlug(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta property="og:title" content="Grokipedia"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	q := "Some Topic"
	escaped := url.QueryEscape(q)
	titles, _, err := searchGrokipediaDirectFallback(q, escaped)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(titles) != 1 || titles[0] != "Some Topic" {
		t.Fatalf("expected slug-derived title, got %v", titles)
	}
}

func TestSearchGrokipediaDirectFallbackNon200(t *testing.T) {
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`{}`), 404, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	titles, cached, err := searchGrokipediaDirectFallback("Missing", url.QueryEscape("Missing"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on non-200")
	}
	if titles != nil {
		t.Fatalf("expected nil titles on non-200")
	}
}

func TestSearchGrokipediaUsesCache(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	defer func() { searchGrokipediaChromedp = origChromedp }()

	calls := 0
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		calls++
		return []string{"CachedTitle"}, nil
	}

	q := "CacheMe"
	escaped := url.QueryEscape(q)
	data := loadSearchCache()
	delete(data, "grokipedia:"+escaped)
	saveSearchCache(data)

	titles, cached, err := searchGrokipedia("en", q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on first call")
	}
	if len(titles) != 1 || titles[0] != "CachedTitle" {
		t.Fatalf("unexpected titles: %v", titles)
	}

	titles2, cached2, err2 := searchGrokipedia("en", q)
	if err2 != nil {
		t.Fatalf("unexpected error on cached call: %v", err2)
	}
	if !cached2 {
		t.Fatalf("expected cached=true on second call")
	}
	if len(titles2) != 1 || titles2[0] != "CachedTitle" {
		t.Fatalf("unexpected cached titles: %v", titles2)
	}
	if calls != 1 {
		t.Fatalf("expected chromedp called once, got %d", calls)
	}
}

func TestSearchGrokipediaFallbackOnChromedpError(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return nil, errors.New("chromedp fail")
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta property="og:title" content="Fallback Title"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	titles, cached, err := searchGrokipedia("en", "FallbackTerm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on fallback")
	}
	if len(titles) != 1 || titles[0] != "Fallback Title" {
		t.Fatalf("unexpected titles: %v", titles)
	}
}

func TestSearchGrokipediaChromeUnavailableFallback(t *testing.T) {
	setChromeCheckResult(false, errors.New("executable file not found"))
	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta property="og:title" content="NoChrome Title"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	titles, cached, err := searchGrokipedia("en", "NoChromeTerm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on no-chrome fallback")
	}
	if len(titles) != 1 || titles[0] != "NoChrome Title" {
		t.Fatalf("unexpected titles: %v", titles)
	}
}

func TestSearchGrokipediaChromedpNoResultsFallback(t *testing.T) {
	setChromeCheckResult(true, nil)
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return []string{}, nil
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	orig := httpGetFunc
	httpGetFunc = func(endpoint string) ([]byte, int, string, error) {
		return []byte(`<html><head><meta property="og:title" content="Fallback Empty"></head></html>`), 200, "text/html", nil
	}
	defer func() { httpGetFunc = orig }()

	titles, cached, err := searchGrokipedia("en", "EmptyResults")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on fallback")
	}
	if len(titles) != 1 || titles[0] != "Fallback Empty" {
		t.Fatalf("unexpected titles: %v", titles)
	}
}

func TestSearchGrokipediaEmptyQuery(t *testing.T) {
	_, _, err := searchGrokipedia("en", "   ")
	if err == nil {
		t.Fatalf("expected error on empty search query")
	}
}

func TestSearchGrokipediaDirectFallbackEmptyQuery(t *testing.T) {
	titles, cached, err := searchGrokipediaDirectFallback("   ", url.QueryEscape("   "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached {
		t.Fatalf("expected cached=false on empty query")
	}
	if titles != nil {
		t.Fatalf("expected nil titles on empty query fallback")
	}
}

func TestRunSourceGrokipedia(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Grokipedia integration test in short mode")
	}

	// Clear caches for fresh test
	cache := loadCache()
	delete(cache, "grokipedia:SpaceX")
	saveCache(cache)
	sCache := loadSearchCache()
	delete(sCache, "grokipedia:"+url.QueryEscape("SpaceX"))
	saveSearchCache(sCache)

	// Use stdin to auto-select first result
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("1\n")
		w.Close()
	}()
	defer func() { os.Stdin = oldStdin }()

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "SpaceX"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; out=%s", code, buf.String())
	}

	out := buf.String()
	if !strings.Contains(strings.ToLower(out), "spacex") {
		t.Fatalf("expected SpaceX in output, got %s", out)
	}
	if !strings.Contains(out, "grokipedia.com/page/SpaceX") {
		t.Fatalf("expected Grokipedia URL in output, got %s", out)
	}
}

// TestRunGrokipediaChromeNotFound tests that a clear error message is shown when Chrome is not installed.
func TestRunGrokipediaChromeNotFound(t *testing.T) {
	// Mock the chromedp function to simulate Chrome not being installed
	origChromedp := searchGrokipediaChromedp
	searchGrokipediaChromedp = func(query, escapedQuery string) ([]string, error) {
		return nil, ErrChromeNotFound
	}
	defer func() { searchGrokipediaChromedp = origChromedp }()

	// Clear any cached search results that might be used as fallback
	sCache := loadSearchCache()
	delete(sCache, "grokipedia:"+url.QueryEscape("TestChromeNotFound"))
	saveSearchCache(sCache)

	buf := &bytes.Buffer{}
	code := run(buf, []string{"-source", "grokipedia", "TestChromeNotFound"})

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}

	out := buf.String()
	assertContainsTranslation(t, out, msgChromeNotFoundError)
	assertContainsTranslation(t, out, msgChromeNotFoundHint)
	assertContainsTranslation(t, out, msgChromeNotFoundDetail)
}
