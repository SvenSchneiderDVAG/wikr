package main

import (
	"net/http"
	"time"
)

const (
	wikipediaAPITemplate        = "https://%s.wikipedia.org/api/rest_v1/page/summary/"
	wikipediaSearchAPITemplate  = "https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json"
	grokipediaPageAPITemplate   = "https://grokipedia.com/page/%s"
	grokipediaSearchAPITemplate = "https://grokipedia.com/search?q=%s"
	cacheDuration               = 24 * time.Hour
	summaryMaxLen               = 1000
	version                     = "0.8.2"
)

var debug = false

var (
	userAgent   = "wikr/" + version + " (+https://github.com/SvenSchneiderDVAG/wikr)"
	httpGetFunc = httpGet
	httpTimeout = 10 * time.Second
	httpClient  = &http.Client{}
)
