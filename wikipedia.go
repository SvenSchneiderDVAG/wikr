package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

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
		return nil, false, newLocalizedError(msgErrSearchUnexpectedStatus, status)
	}
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		if titles, ok := getCachedSearch(lang, escapedQuery); ok {
			if debug {
				fmt.Printf("Non-JSON Content-Type '%s' for search; using cache fallback.\n", ct)
			}
			return titles, true, nil
		}
		return nil, false, newLocalizedError(msgErrSearchUnexpectedContentType, ct)
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
		return nil, false, wrapLocalizedError(msgErrSearchDecodeJSON, err)
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
		return "", "", false, newLocalizedError(msgErrSummaryUnexpectedStatus, status)
	}
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return "", "", false, newLocalizedError(msgErrSummaryUnexpectedContentType, ct)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", false, wrapLocalizedError(msgErrSummaryDecodeJSON, err)
	}

	extractVal, ok := result["extract"]
	if !ok {
		return "", "", false, newLocalizedError(msgErrSummaryMissingExtract)
	}
	extractStr, ok := extractVal.(string)
	if !ok {
		return "", "", false, newLocalizedError(msgErrSummaryExtractNotString)
	}
	contentURLs, ok := result["content_urls"].(map[string]any)
	if !ok {
		return "", "", false, newLocalizedError(msgErrSummaryMissingContentURLs)
	}
	desktop, ok := contentURLs["desktop"].(map[string]any)
	if !ok {
		return "", "", false, newLocalizedError(msgErrSummaryMissingDesktop)
	}
	pageURL, ok := desktop["page"].(string)
	if !ok {
		return "", "", false, newLocalizedError(msgErrSummaryMissingPageURL)
	}

	extractStr = truncateWithEllipsis(extractStr, summaryMaxLen)
	setCachedEntry(lang, title, extractStr, pageURL)
	return extractStr, pageURL, false, nil
}
