package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func slugifyGrokipedia(title string) string {
	cleaned := strings.TrimSpace(title)
	parts := strings.Fields(cleaned)
	return strings.Join(parts, "_")
}

func extractMetaDescription(htmlContent string) string {
	re := regexp.MustCompile(`<meta\s+name="description"\s+content="([^"]*)"`)
	matches := re.FindStringSubmatch(htmlContent)
	if len(matches) < 2 {
		return ""
	}
	return html.UnescapeString(matches[1])
}

func extractOGTitle(htmlContent string) string {
	re := regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]*)"`)
	matches := re.FindStringSubmatch(htmlContent)
	if len(matches) < 2 {
		return ""
	}
	return html.UnescapeString(matches[1])
}

func isGrokipediaNotFound(err error) bool {
	if err == nil {
		return false
	}
	return hasMessageKey(err, msgErrGrokipediaMissingArticle)
}

func probeGrokipediaTitle(title string) (bool, error) {
	slug := slugifyGrokipedia(title)
	if slug == "" {
		return false, nil
	}

	endpoint := fmt.Sprintf(grokipediaPageAPITemplate, url.PathEscape(slug))
	body, status, _, err := httpGetFunc(endpoint)
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if status != http.StatusOK {
		return false, newLocalizedError(msgErrGrokipediaUnexpectedStatus, status)
	}

	summary := extractMetaDescription(string(body))
	return summary != "", nil
}

func filterGrokipediaResults(results []string, maxResults int) ([]string, error) {
	if maxResults <= 0 {
		maxResults = len(results)
	}
	valid := make([]string, 0, maxResults)
	for _, title := range results {
		if len(valid) >= maxResults {
			break
		}
		ok, err := probeGrokipediaTitle(title)
		if err != nil {
			return nil, err
		}
		if ok {
			valid = append(valid, title)
		}
	}
	return valid, nil
}

func getGrokipediaSummary(title string) (string, string, bool, error) {
	slug := slugifyGrokipedia(title)
	if slug == "" {
		return "", "", false, newLocalizedError(msgErrGrokipediaEmptyTitle)
	}

	if summary, urlStr, found := getCachedEntry("grokipedia", slug); found {
		return summary, urlStr, true, nil
	}

	endpoint := fmt.Sprintf(grokipediaPageAPITemplate, url.PathEscape(slug))
	body, status, _, err := httpGetFunc(endpoint)
	if err != nil {
		if debug {
			fmt.Printf("HTTP error fetching Grokipedia page: %v\n", err)
		}
		return "", "", false, err
	}
	if status == http.StatusNotFound {
		return "", "", false, newLocalizedError(msgErrGrokipediaMissingArticle, title)
	}
	if status != http.StatusOK {
		return "", "", false, newLocalizedError(msgErrGrokipediaUnexpectedStatus, status)
	}

	summary := extractMetaDescription(string(body))
	if summary == "" {
		return "", "", false, newLocalizedError(msgErrGrokipediaMissingArticle, title)
	}

	summary = truncateWithEllipsis(summary, summaryMaxLen)
	setCachedEntry("grokipedia", slug, summary, endpoint)
	return summary, endpoint, false, nil
}

func removeTitle(results []string, title string) []string {
	if len(results) == 0 {
		return results
	}
	updated := results[:0]
	for _, t := range results {
		if t != title {
			updated = append(updated, t)
		}
	}
	return updated
}

func promptGrokipediaMissing(out io.Writer, in io.Reader, lang string, hasAlternatives bool) (string, string) {
	reader := readerFrom(in)
	for {
		promptKey := msgArticleNotFoundNoAlt
		if hasAlternatives {
			promptKey = msgArticleNotFoundAlt
		}
		fmt.Fprint(out, tr(lang, promptKey))
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if err != nil && line == "" {
			return "quit", ""
		}

		switch line {
		case "a", "y", "j":
			if hasAlternatives {
				return "another", ""
			}
			fmt.Fprintln(out, tr(lang, msgNoOtherResults))
		case "r", "refine":
			fmt.Fprint(out, tr(lang, msgRefinePrompt))
			newLine, err := reader.ReadString('\n')
			newLine = strings.TrimSpace(newLine)
			if err != nil && newLine == "" {
				return "quit", ""
			}
			if newLine == "" {
				fmt.Fprintln(out, tr(lang, msgSearchTermEmpty))
				continue
			}
			return "refine", newLine
		case "q", "quit":
			return "quit", ""
		default:
			fmt.Fprintln(out, tr(lang, msgInvalidChoice))
		}
	}
}

var searchGrokipediaChromedp func(query string, escapedQuery string) ([]string, error) = searchGrokipediaChromedpImpl

func searchGrokipediaChromedpImpl(query string, escapedQuery string) ([]string, error) {
	endpoint := fmt.Sprintf(grokipediaSearchAPITemplate, escapedQuery)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "executable file not found") ||
			strings.Contains(errStr, "exec:") ||
			strings.Contains(errStr, "cannot find") ||
			strings.Contains(errStr, "not found") ||
			strings.Contains(errStr, "no such file") {
			return nil, ErrChromeNotFound
		}
		return nil, err
	}

	titles := make([]string, 0)
	err := chromedp.Run(ctx,
		chromedp.Navigate(endpoint),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			for {
				select {
				case <-waitCtx.Done():
					return nil
				default:
					var hasResults bool
					chromedp.Evaluate(`document.body.innerText.includes('yielded') || document.querySelectorAll('[class*="result"]').length > 0`, &hasResults).Do(ctx)
					if hasResults {
						return nil
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
		}),
		chromedp.Evaluate(`
			(function() {
				var titles = [];
				var bodyText = document.body.innerText;
				var resultsMatch = bodyText.match(/yielded\s+[\d.,]+\s+results?:\s*([\s\S]*?)(?:‹\s*Previous|Next\s*›|$)/i);
				if (resultsMatch && resultsMatch[1]) {
					var lines = resultsMatch[1].split('\n');
					for (var i = 0; i < lines.length; i++) {
						var line = lines[i].trim();
						if (line &&
							line.length > 1 &&
							line.length < 200 &&
							!/^[\d]+$/.test(line) &&
							!/^[\.]+$/.test(line) &&
							line !== 'Previous' &&
							line !== 'Next' &&
							!titles.includes(line)) {
							titles.push(line);
						}
					}
				}
				if (titles.length === 0) {
					var resultEls = document.querySelectorAll('[class*="result"], [class*="item"], [class*="title"]');
					resultEls.forEach(function(el) {
						var text = el.textContent.trim();
						if (text && text.length > 1 && text.length < 200 && !titles.includes(text)) {
							titles.push(text);
						}
					});
				}
				return titles;
			})()
		`, &titles),
	)

	if debug {
		fmt.Printf("Grokipedia search found %d titles: %v\n", len(titles), titles)
	}
	if err != nil {
		if debug {
			fmt.Printf("chromedp error: %v\n", err)
		}
		return nil, err
	}

	return titles, nil
}

func searchGrokipedia(lang string, query string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(query)
	escapedQuery := url.QueryEscape(trimmed)
	if titles, ok := getCachedSearch("grokipedia", escapedQuery); ok {
		return titles, true, nil
	}
	if trimmed == "" {
		return nil, false, newLocalizedError(msgErrGrokipediaEmptySearchQuery)
	}
	if debug {
		fmt.Printf("Launching chromedp search for: %s\n", trimmed)
	}

	if available, err := checkChromeAvailable(); !available {
		chromeWarningOnce.Do(func() {
			if err != nil && (strings.Contains(err.Error(), "executable file not found") ||
				strings.Contains(err.Error(), "not found") ||
				strings.Contains(err.Error(), "no such file")) {
				fmt.Fprintln(os.Stderr, tr(lang, msgChromeNotFoundWarning))
				fmt.Fprintln(os.Stderr, tr(lang, msgChromeNotFoundWarningHint))
			} else if err != nil && debug {
				fmt.Printf("Chrome check failed: %v\n", err)
			}
		})
		return searchGrokipediaDirectFallback(trimmed, escapedQuery)
	}

	titles, err := searchGrokipediaChromedp(trimmed, escapedQuery)
	if err != nil {
		if debug {
			fmt.Printf("chromedp search error: %v\n", err)
		}
		if errors.Is(err, ErrChromeNotFound) {
			return nil, false, err
		}
		return searchGrokipediaDirectFallback(trimmed, escapedQuery)
	}
	if len(titles) == 0 {
		if debug {
			fmt.Printf("chromedp found no results, trying direct fallback\n")
		}
		return searchGrokipediaDirectFallback(trimmed, escapedQuery)
	}

	setCachedSearch("grokipedia", escapedQuery, titles)
	if debug {
		fmt.Printf("Grokipedia chromedp search found: %v\n", titles)
	}
	return titles, false, nil
}

func searchGrokipediaDirectFallback(query, escapedQuery string) ([]string, bool, error) {
	slug := slugifyGrokipedia(query)
	if slug == "" {
		return nil, false, nil
	}

	endpoint := fmt.Sprintf(grokipediaPageAPITemplate, url.PathEscape(slug))
	body, status, _, err := httpGetFunc(endpoint)
	if err != nil {
		if debug {
			fmt.Printf("HTTP error in direct fallback: %v\n", err)
		}
		return nil, false, err
	}

	if status == http.StatusOK {
		htmlContent := string(body)
		pageTitle := extractOGTitle(htmlContent)
		if pageTitle == "" || pageTitle == "Grokipedia" {
			pageTitle = strings.ReplaceAll(slug, "_", " ")
		}
		titles := []string{pageTitle}
		setCachedSearch("grokipedia", escapedQuery, titles)
		if debug {
			fmt.Printf("Grokipedia direct fallback found: %v\n", titles)
		}
		return titles, false, nil
	}
	if debug {
		fmt.Printf("Grokipedia search for '%s' found no results\n", query)
	}
	return nil, false, nil
}
