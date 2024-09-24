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
)

const wikipediaAPITemplate = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
const wikipediaSearchAPITemplate = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"

type CacheEntry struct {
	Summary   string    `json:"summary"`
	URL       string    `json:"url"`
	Timestamp time.Time `json:"timestamp"`
}

type Cache map[string]CacheEntry

const cacheFileName = ".wikr_cache.json"
const cacheDuration = 24 * time.Hour

func getCachePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return cacheFileName
	}
	return filepath.Join(homeDir, cacheFileName)
}

func loadCache() Cache {
	cache := make(Cache)
	cachePath := getCachePath()
	data, err := os.ReadFile(cachePath)
	if err != nil {
		fmt.Printf("Fehler beim Lesen der Cache-Datei %s: %v\n", cachePath, err)
		return cache
	}
	err = json.Unmarshal(data, &cache)
	if err != nil {
		fmt.Printf("Fehler beim Entschlüsseln des Caches: %v\n", err)
	}
	return cache
}

func saveCache(cache Cache) {
	data, err := json.Marshal(cache)
	if err != nil {
		fmt.Printf("Fehler beim Verschlüsseln des Caches: %v\n", err)
		return
	}
	cachePath := getCachePath()
	err = os.WriteFile(cachePath, data, 0644)
	if err != nil {
		fmt.Printf("Fehler beim Schreiben der Cache-Datei %s: %v\n", cachePath, err)
	}
}

func getCachedEntry(lang, title string) (string, string, bool) {
	cache := loadCache()
	key := lang + ":" + title
	fmt.Printf("\nSuche nach Cache-Eintrag für Schlüssel: %s\n", key) // Debug-Ausgabe
	entry, exists := cache[key]
	if exists {
		fmt.Printf("Cache-Eintrag gefunden, Alter: %v\n", time.Since(entry.Timestamp)) // Debug-Ausgabe
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
	fmt.Printf("Speichere Cache-Eintrag für Schlüssel: %s\n", key) // Debug-Ausgabe
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

	// Versuche zuerst, den Eintrag aus dem Cache zu holen
	if summary, url, found := getCachedEntry(lang, title); found {
		close(done)
		wg.Wait()
		fmt.Print("\r") // Löscht die Ladeanimation
		return summary, url, true, nil
	}

	encodedTitle := url.PathEscape(title)
	response, err := http.Get(fmt.Sprintf(wikipediaAPITemplate, lang) + encodedTitle)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r") // Löscht die Ladeanimation
		return "", "", false, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r") // Löscht die Ladeanimation
		return "", "", false, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		close(done)
		wg.Wait()
		fmt.Print("\r") // Löscht die Ladeanimation
		return "", "", false, err
	}

	summary := result["extract"].(string)
	url := result["content_urls"].(map[string]interface{})["desktop"].(map[string]interface{})["page"].(string)

	// Kürze die Zusammenfassung auf maximal 1000 Zeichen
	if len(summary) > 1000 {
		summary = summary[:997] + "..."
	}

	close(done)
	wg.Wait()
	fmt.Print("\r") // Löscht die Ladeanimation

	// Cache den neuen Eintrag
	setCachedEntry(lang, title, summary, url)

	return summary, url, false, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Verwendung: askw [de|en] Suchbegriff")
		os.Exit(1)
	}

	lang := "de"
	var searchTermParts []string

	if os.Args[1] == "de" || os.Args[1] == "en" {
		lang = os.Args[1]
		searchTermParts = os.Args[2:]
	} else {
		searchTermParts = os.Args[1:]
	}

	if len(searchTermParts) == 0 {
		fmt.Println("Bitte geben Sie einen Suchbegriff ein.")
		os.Exit(1)
	}

	searchTerm := strings.Join(searchTermParts, " ")
	encodedSearchTerm := url.QueryEscape(searchTerm)

	// Suche nach möglichen Ergebnissen
	searchResults, err := searchWikipedia(lang, encodedSearchTerm)
	if err != nil {
		fmt.Println("Fehler bei der Suche:", err)
		os.Exit(1)
	}

	if len(searchResults) == 0 {
		fmt.Println("Keine Ergebnisse gefunden.")
		os.Exit(1)
	}

	var selectedTitle string
	if len(searchResults) == 1 {
		selectedTitle = searchResults[0]
	} else {
		selectedTitle = chooseResult(searchResults)
	}

	// Abrufen der Zusammenfassung für den ausgewählten Titel
	summary, url, cached, err := getWikipediaSummary(lang, selectedTitle)
	if err != nil {
		color.Red("Fehler beim Abrufen der Zusammenfassung: %v", err)
		os.Exit(1)
	}

	color.Blue("\nZusammenfassung:")
	if cached {
		fmt.Print("(cached) ")
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

func chooseResult(results []string) string {
	fmt.Println("\nMehrere Ergebnisse gefunden. Bitte wählen Sie eines aus:")
	for i, result := range results {
		fmt.Printf("%d. %s\n", i+1, result)
	}
	fmt.Println("q. Beenden")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nGeben Sie die Nummer des gewünschten Ergebnisses ein (oder 'q' zum Beenden): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "q" {
			fmt.Println("\nProgramm wurde. beendet.")
			os.Exit(0)
		}

		index := 0
		_, err := fmt.Sscanf(input, "%d", &index)
		if err == nil && index > 0 && index <= len(results) {
			return results[index-1]
		}
		fmt.Println("\nUngültige Eingabe. Bitte versuchen Sie es erneut.")
	}
}
