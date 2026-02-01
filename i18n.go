package main

import (
	"fmt"
	"strings"
)

type messageKey string

const (
	msgMultipleResults                   messageKey = "multiple_results"
	msgSelectPrompt                      messageKey = "select_prompt"
	msgRefinePrompt                      messageKey = "refine_prompt"
	msgSearchTermEmpty                   messageKey = "search_term_empty"
	msgInvalidSelection                  messageKey = "invalid_selection"
	msgArticleNotFoundAlt                messageKey = "article_not_found_alt"
	msgArticleNotFoundNoAlt              messageKey = "article_not_found_no_alt"
	msgNoOtherResults                    messageKey = "no_other_results"
	msgInvalidChoice                     messageKey = "invalid_choice"
	msgWarningConfigLoad                 messageKey = "warning_config_load"
	msgConfigPersistWarning              messageKey = "config_persist_warning"
	msgUsage                             messageKey = "usage"
	msgOptions                           messageKey = "options"
	msgFlagLang                          messageKey = "flag_lang"
	msgFlagSource                        messageKey = "flag_source"
	msgFlagMax                           messageKey = "flag_max"
	msgFlagClearCache                    messageKey = "flag_clear_cache"
	msgFlagVersion                       messageKey = "flag_version"
	msgFlagResetConfig                   messageKey = "flag_reset_config"
	msgErrorWriteDefaultConfig           messageKey = "error_write_default_config"
	msgConfigReset                       messageKey = "config_reset"
	msgInvalidSource                     messageKey = "invalid_source"
	msgWarningSaveConfig                 messageKey = "warning_save_config"
	msgCacheCleared                      messageKey = "cache_cleared"
	msgVersion                           messageKey = "version"
	msgChromeNotFoundError               messageKey = "chrome_not_found_error"
	msgChromeNotFoundDetail              messageKey = "chrome_not_found_detail"
	msgChromeNotFoundHint                messageKey = "chrome_not_found_hint"
	msgNetworkErrorCacheFallback         messageKey = "network_error_cache_fallback"
	msgErrorDuringSearch                 messageKey = "error_during_search"
	msgCachedSearchEmpty                 messageKey = "cached_search_empty"
	msgNoResults                         messageKey = "no_results"
	msgSelectionCanceled                 messageKey = "selection_canceled"
	msgErrorFetchingSummary              messageKey = "error_fetching_summary"
	msgSummaryHeader                     messageKey = "summary_header"
	msgCachedMarker                      messageKey = "cached_marker"
	msgURLLabel                          messageKey = "url_label"
	msgErrChromeNotFound                 messageKey = "err_chrome_not_found"
	msgErrCacheDirNotFound               messageKey = "err_cache_dir_not_found"
	msgErrCacheDirCreate                 messageKey = "err_cache_dir_create"
	msgErrConfigDirNotFound              messageKey = "err_config_dir_not_found"
	msgErrConfigDirCreate                messageKey = "err_config_dir_create"
	msgErrDefaultConfigCreate            messageKey = "err_default_config_create"
	msgErrConfigRead                     messageKey = "err_config_read"
	msgErrConfigDecode                   messageKey = "err_config_decode"
	msgErrConfigEncode                   messageKey = "err_config_encode"
	msgErrConfigWrite                    messageKey = "err_config_write"
	msgErrHTTPCreateRequest              messageKey = "err_http_create_request"
	msgErrHTTPPerformRequest             messageKey = "err_http_perform_request"
	msgErrHTTPReadBody                   messageKey = "err_http_read_body"
	msgErrHTTPTransientStatus            messageKey = "err_http_transient_status"
	msgErrSearchUnexpectedStatus         messageKey = "err_search_unexpected_status"
	msgErrSearchUnexpectedContentType    messageKey = "err_search_unexpected_content_type"
	msgErrSearchDecodeJSON               messageKey = "err_search_decode_json"
	msgErrSummaryUnexpectedStatus        messageKey = "err_summary_unexpected_status"
	msgErrSummaryUnexpectedContentType   messageKey = "err_summary_unexpected_content_type"
	msgErrSummaryDecodeJSON              messageKey = "err_summary_decode_json"
	msgErrSummaryMissingExtract          messageKey = "err_summary_missing_extract"
	msgErrSummaryExtractNotString        messageKey = "err_summary_extract_not_string"
	msgErrSummaryMissingContentURLs      messageKey = "err_summary_missing_content_urls"
	msgErrSummaryMissingDesktop          messageKey = "err_summary_missing_desktop"
	msgErrSummaryMissingPageURL          messageKey = "err_summary_missing_page_url"
	msgErrGrokipediaUnexpectedStatus     messageKey = "err_grokipedia_unexpected_status"
	msgErrGrokipediaEmptyTitle           messageKey = "err_grokipedia_empty_title"
	msgErrGrokipediaMissingArticle       messageKey = "err_grokipedia_missing_article"
	msgErrGrokipediaMissingArticleSuffix messageKey = "err_grokipedia_missing_article_suffix"
	msgErrGrokipediaEmptySearchQuery     messageKey = "err_grokipedia_empty_search_query"
	msgErrCachePath                      messageKey = "err_cache_path"
	msgErrCacheDeleteFile                messageKey = "err_cache_delete_file"
)

var translations = map[string]map[messageKey]string{
	"en": translationsEn,
	"de": translationsDe,
}

func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "de" {
		return "de"
	}
	return "en"
}

func tr(lang string, key messageKey, args ...any) string {
	return fmt.Sprintf(trRaw(lang, key), args...)
}

func trRaw(lang string, key messageKey) string {
	normalized := normalizeLang(lang)
	if tmpl, ok := translations[normalized][key]; ok {
		return tmpl
	}
	if tmpl, ok := translations["en"][key]; ok {
		return tmpl
	}
	return fmt.Sprintf("missing translation: %s", key)
}
