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
)

const wikipediaAPITemplate = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
const wikipediaSearchAPITemplate = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"

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
	summary, url, err := getWikipediaSummary(lang, selectedTitle)
	if err != nil {
		fmt.Println("Fehler beim Abrufen der Zusammenfassung:", err)
		os.Exit(1)
	}

	fmt.Println("\nZusammenfassung:")
	fmt.Println("----------------")
	fmt.Println("\n" + summary)
	fmt.Println("\nURL:")
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
			fmt.Println("\nProgramm wird beendet.")
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

func getWikipediaSummary(lang, title string) (string, string, error) {
	encodedTitle := url.PathEscape(title)
	response, err := http.Get(fmt.Sprintf(wikipediaAPITemplate, lang) + encodedTitle)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", "", err
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", "", err
	}

	summary := result["extract"].(string)
	url := result["content_urls"].(map[string]interface{})["desktop"].(map[string]interface{})["page"].(string)

	return summary, url, nil
}
