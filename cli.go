package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/fatih/color"
)

func chooseResult(results []string, maxResults *int, lang, source string, out io.Writer, in io.Reader) (string, bool, string) {
	limit := *maxResults
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}

	fmt.Fprintln(out)
	useColor := isTerminal(out)
	header := tr(lang, msgMultipleResults, limit, len(results), source)
	if useColor {
		color.New(color.FgCyan).Fprintln(out, header)
	} else {
		fmt.Fprintln(out, header)
	}
	for i := 0; i < limit; i++ {
		fmt.Fprintf(out, "  [%d] %s\n", i+1, results[i])
	}
	fmt.Fprintln(out)

	reader := readerFrom(in)
	for {
		fmt.Fprint(out, tr(lang, msgSelectPrompt))
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return results[0], false, ""
		}

		lower := strings.ToLower(line)
		if lower == "q" || lower == "quit" {
			return "", true, ""
		}
		if lower == "r" || lower == "refine" {
			fmt.Fprint(out, tr(lang, msgRefinePrompt))
			newLine, _ := reader.ReadString('\n')
			newLine = strings.TrimSpace(newLine)
			if newLine == "" {
				fmt.Fprintln(out, tr(lang, msgSearchTermEmpty))
				continue
			}
			return "", false, newLine
		}

		var idx int
		_, err := fmt.Sscanf(line, "%d", &idx)
		if err == nil && idx >= 1 && idx <= limit {
			return results[idx-1], false, ""
		}

		invalidMsg := tr(lang, msgInvalidSelection, limit)
		if useColor {
			color.New(color.FgYellow).Fprintln(out, invalidMsg)
		} else {
			fmt.Fprintln(out, invalidMsg)
		}
	}
}

func langFromArgs(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-lang" || args[i] == "--lang":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		case strings.HasPrefix(args[i], "-lang="):
			return strings.TrimPrefix(args[i], "-lang="), true
		case strings.HasPrefix(args[i], "--lang="):
			return strings.TrimPrefix(args[i], "--lang="), true
		}
	}
	return "", false
}

func run(out io.Writer, args []string) int {
	fallbackLang := "en"
	if argLang, ok := langFromArgs(args); ok {
		fallbackLang = normalizeLang(argLang)
	}

	config, corrected, err := loadConfig()
	if err != nil {
		fmt.Fprintln(out, tr(fallbackLang, msgWarningConfigLoad, localizeErr(fallbackLang, err)))
		config = Config{Language: fallbackLang, MaxResults: 5}
	} else if corrected {
		if err := saveConfig(config); err != nil && debug {
			fmt.Fprintln(out, tr(config.Language, msgConfigPersistWarning, localizeErr(config.Language, err)))
		}
	}

	fs := flag.NewFlagSet("wikr", flag.ContinueOnError)
	fs.SetOutput(out)
	lang := fs.String("lang", config.Language, tr(config.Language, msgFlagLang))
	source := fs.String("source", config.Source, tr(config.Language, msgFlagSource))
	maxResults := fs.Int("max", config.MaxResults, tr(config.Language, msgFlagMax))
	isClearCache := fs.Bool("clear-cache", false, tr(config.Language, msgFlagClearCache))
	isVersion := fs.Bool("version", false, tr(config.Language, msgFlagVersion))
	isResetConfig := fs.Bool("reset-config", false, tr(config.Language, msgFlagResetConfig))
	fs.Usage = func() {
		fmt.Fprint(out, tr(*lang, msgUsage))
		fmt.Fprint(out, tr(*lang, msgOptions))
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if *isResetConfig {
		defaultCfg := defaultConfig()
		if err := saveConfig(defaultCfg); err != nil {
			fmt.Fprintln(out, tr(*lang, msgErrorWriteDefaultConfig, localizeErr(*lang, err)))
			return 1
		}
		fmt.Fprintln(out, tr(*lang, msgConfigReset))
		return 0
	}

	src := strings.ToLower(strings.TrimSpace(*source))
	if src == "" {
		src = "wikipedia"
	}
	if src != "wikipedia" && src != "grokipedia" {
		fmt.Fprintln(out, tr(*lang, msgInvalidSource, src))
		return 2
	}
	*source = src

	configChanged := false
	fs.Visit(func(f *flag.Flag) {
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
		case "source":
			if *source != config.Source {
				config.Source = *source
				configChanged = true
			}
		}
	})
	if configChanged {
		if err := saveConfig(config); err != nil {
			fmt.Fprintln(out, tr(*lang, msgWarningSaveConfig, localizeErr(*lang, err)))
		}
	}

	if *isClearCache {
		if err := clearCache(); err != nil {
			fmt.Fprintln(out, localizeErr(*lang, err))
			return 1
		}
		fmt.Fprintln(out, tr(*lang, msgCacheCleared))
		return 0
	}
	if *isVersion {
		fmt.Fprintln(out, tr(*lang, msgVersion, version))
		return 0
	}

	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	searchTerm := strings.Join(fs.Args(), " ")
	input := bufio.NewReader(os.Stdin)
	var summary, urlStr string
	var cached bool

	if *source == "grokipedia" {
	searchLoop:
		for {
			grokEscaped := url.QueryEscape(strings.TrimSpace(searchTerm))

			searchResults, cachedSearch, err := searchGrokipedia(*lang, searchTerm)
			if err != nil {
				if errors.Is(err, ErrChromeNotFound) {
					fmt.Fprintln(out, tr(*lang, msgChromeNotFoundError))
					fmt.Fprintln(out, tr(*lang, msgChromeNotFoundDetail))
					fmt.Fprintln(out, tr(*lang, msgChromeNotFoundHint))
					return 1
				}
				if titles, ok := getCachedSearch("grokipedia", grokEscaped); ok {
					fmt.Fprintln(out, tr(*lang, msgNetworkErrorCacheFallback, localizeErr(*lang, err)))
					searchResults = titles
					cachedSearch = true
				} else {
					fmt.Fprintln(out, tr(*lang, msgErrorDuringSearch, localizeErr(*lang, err)))
					return 1
				}
			}
			if len(searchResults) == 0 {
				if cachedSearch {
					fmt.Fprintln(out, tr(*lang, msgCachedSearchEmpty))
				} else {
					fmt.Fprintln(out, tr(*lang, msgNoResults))
				}
				return 1
			}

			if filtered, ferr := filterGrokipediaResults(searchResults, *maxResults); ferr == nil {
				if len(filtered) == 0 {
					fmt.Fprintln(out, tr(*lang, msgNoResults))
					return 1
				}
				searchResults = filtered
			}

			for {
				selectedTitle := ""
				if len(searchResults) == 1 {
					selectedTitle = searchResults[0]
				} else {
					var quit bool
					var refine string
					selectedTitle, quit, refine = chooseResult(searchResults, maxResults, *lang, *source, os.Stderr, input)
					if quit {
						fmt.Fprintln(out, tr(*lang, msgSelectionCanceled))
						return 0
					}
					if refine != "" {
						searchTerm = refine
						continue searchLoop
					}
				}

				summary, urlStr, cached, err = getGrokipediaSummary(selectedTitle)
				if err != nil {
					if isGrokipediaNotFound(err) {
						searchResults = removeTitle(searchResults, selectedTitle)
						action, refine := promptGrokipediaMissing(os.Stderr, input, *lang, len(searchResults) > 0)
						switch action {
						case "another":
							if len(searchResults) == 0 {
								continue
							}
							continue
						case "refine":
							searchTerm = refine
							continue searchLoop
						case "quit":
							fmt.Fprintln(out, tr(*lang, msgSelectionCanceled))
							return 0
						}
					}
					fmt.Fprintln(out, tr(*lang, msgErrorFetchingSummary, localizeErr(*lang, err)))
					return 1
				}
				break searchLoop
			}
		}
	} else {
		for {
			encodedSearchTerm := url.QueryEscape(searchTerm)

			searchResults, cachedSearch, err := searchWikipedia(*lang, encodedSearchTerm)
			if err != nil {
				if titles, ok := getCachedSearch(*lang, encodedSearchTerm); ok {
					fmt.Fprintln(out, tr(*lang, msgNetworkErrorCacheFallback, localizeErr(*lang, err)))
					searchResults = titles
					cachedSearch = true
				} else {
					fmt.Fprintln(out, tr(*lang, msgErrorDuringSearch, localizeErr(*lang, err)))
					return 1
				}
			}
			if len(searchResults) == 0 {
				if cachedSearch {
					fmt.Fprintln(out, tr(*lang, msgCachedSearchEmpty))
				} else {
					fmt.Fprintln(out, tr(*lang, msgNoResults))
				}
				return 1
			}

			selectedTitle := ""
			if len(searchResults) == 1 {
				selectedTitle = searchResults[0]
			} else {
				var quit bool
				var refine string
				selectedTitle, quit, refine = chooseResult(searchResults, maxResults, *lang, *source, os.Stderr, input)
				if quit {
					fmt.Fprintln(out, tr(*lang, msgSelectionCanceled))
					return 0
				}
				if refine != "" {
					searchTerm = refine
					continue
				}
			}

			summary, urlStr, cached, err = getWikipediaSummary(*lang, selectedTitle)
			if err != nil {
				fmt.Fprintln(out, tr(*lang, msgErrorFetchingSummary, localizeErr(*lang, err)))
				return 1
			}
			break
		}
	}

	if isTerminal(out) {
		headerColor := color.New(color.FgGreen, color.Bold)
		cachedColor := color.New(color.FgYellow)
		urlLabelColor := color.New(color.FgMagenta, color.Bold)
		linkColor := color.New(color.FgBlue, color.Underline)
		headerColor.Fprintln(out, "\n\n"+tr(*lang, msgSummaryHeader))
		if cached {
			cachedColor.Fprintln(out, tr(*lang, msgCachedMarker))
		}
		fmt.Fprintln(out, summary)
		urlLabelColor.Fprintln(out, "\n"+tr(*lang, msgURLLabel))
		linkColor.Fprintln(out, urlStr)
		return 0
	}

	fmt.Fprintln(out, "\n\n"+tr(*lang, msgSummaryHeader))
	if cached {
		fmt.Fprintln(out, tr(*lang, msgCachedMarker))
	}
	fmt.Fprintln(out, summary)
	fmt.Fprintln(out, "\n"+tr(*lang, msgURLLabel))
	fmt.Fprintln(out, urlStr)
	return 0
}
