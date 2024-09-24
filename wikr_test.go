package main

import (
	"testing"
	"os"
	"time"
)

func TestMain(m *testing.M) {
	createEmptyCacheFileIfNotExists()

	// Run tests
	code := m.Run()

	// Teardown
	os.Remove(getCachePath())
	os.Exit(code)
}

func TestGetCachePath(t *testing.T) {
	path := getCachePath()
	if path == "" {
		t.Error("getCachePath sollte einen nicht-leeren Pfad zurückgeben")
	}
}

func TestLoadAndSaveCache(t *testing.T) {
	// Erstelle einen Test-Cache
	testCache := Cache{
		"de:Test": CacheEntry{
			Summary:   "Dies ist ein Test",
			URL:       "https://de.wikipedia.org/wiki/Test",
			Timestamp: time.Now(),
		},
	}

	// Speichere den Test-Cache
	saveCache(testCache)

	// Lade den Cache
	loadedCache := loadCache()

	// Überprüfe, ob der geladene Cache den Test-Eintrag enthält
	entry, exists := loadedCache["de:Test"]
	if !exists {
		t.Error("Der geladene Cache sollte den Test-Eintrag enthalten")
	}

	if entry.Summary != "Dies ist ein Test" {
		t.Errorf("Erwartete Zusammenfassung 'Dies ist ein Test', erhielt '%s'", entry.Summary)
	}

	// Lösche den Test-Eintrag aus dem Cache
	delete(loadedCache, "de:Test")
	saveCache(loadedCache)
}

func TestGetAndSetCachedEntry(t *testing.T) {
	// Setze einen Test-Eintrag
	setCachedEntry("de", "TestArtikel", "Dies ist ein Test-Artikel", "https://de.wikipedia.org/wiki/TestArtikel")

	// Hole den Test-Eintrag
	summary, url, found := getCachedEntry("de", "TestArtikel")

	if !found {
		t.Error("Der Test-Eintrag sollte im Cache gefunden werden")
	}

	if summary != "Dies ist ein Test-Artikel" {
		t.Errorf("Erwartete Zusammenfassung 'Dies ist ein Test-Artikel', erhielt '%s'", summary)
	}

	if url != "https://de.wikipedia.org/wiki/TestArtikel" {
		t.Errorf("Erwartete URL 'https://de.wikipedia.org/wiki/TestArtikel', erhielt '%s'", url)
	}

	// Lösche die Test-Cache-Datei
	os.Remove(getCachePath())
}

func TestSearchWikipedia(t *testing.T) {
	results, err := searchWikipedia("de", "Berlin")

	if err != nil {
		t.Errorf("searchWikipedia sollte keinen Fehler zurückgeben: %v", err)
	}

	if len(results) == 0 {
		t.Error("searchWikipedia sollte Ergebnisse für 'Berlin' zurückgeben")
	}

	foundBerlin := false
	for _, result := range results {
		if result == "Berlin" {
			foundBerlin = true
			break
		}
	}

	if !foundBerlin {
		t.Error("'Berlin' sollte in den Suchergebnissen enthalten sein")
	}
}

func TestGetWikipediaSummary(t *testing.T) {
	summary, url, cached, err := getWikipediaSummary("de", "Berlin")

	if err != nil {
		t.Errorf("getWikipediaSummary sollte keinen Fehler zurückgeben: %v", err)
	}

	if summary == "" {
		t.Error("Die Zusammenfassung sollte nicht leer sein")
	}

	if url == "" {
		t.Error("Die URL sollte nicht leer sein")
	}

	if cached {
		t.Error("Der erste Aufruf sollte nicht aus dem Cache kommen")
	}

	// Zweiter Aufruf sollte aus dem Cache kommen
	_, _, cached, _ = getWikipediaSummary("de", "Berlin")
	if !cached {
		t.Error("Der zweite Aufruf sollte aus dem Cache kommen")
	}

	// Lösche den Test-Eintrag aus dem Cache
	cache := loadCache()
	delete(cache, "de:Berlin")
	saveCache(cache)
}
