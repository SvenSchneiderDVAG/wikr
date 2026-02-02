package main

import "testing"

func TestTranslateFallback(t *testing.T) {
	if got := tr("en", msgSummaryHeader); got != "Summary:" {
		t.Fatalf("expected english summary header, got %q", got)
	}
	if got := tr("de", msgSummaryHeader); got != "Zusammenfassung:" {
		t.Fatalf("expected german summary header, got %q", got)
	}
	if got := tr("xx", msgSummaryHeader); got != "Summary:" {
		t.Fatalf("expected fallback to english, got %q", got)
	}
}

func TestTranslationsCoverage(t *testing.T) {
	for key := range translationsEn {
		if _, ok := translationsDe[key]; !ok {
			t.Fatalf("missing german translation for key %s", key)
		}
	}
	for key := range translationsDe {
		if _, ok := translationsEn[key]; !ok {
			t.Fatalf("missing english translation for key %s", key)
		}
	}
}

func TestLocalizeErrUsesLanguage(t *testing.T) {
	err := newLocalizedError(msgErrSearchUnexpectedStatus, 500)
	if got := localizeErr("de", err); got != tr("de", msgErrSearchUnexpectedStatus, 500) {
		t.Fatalf("expected german localized error, got %q", got)
	}
}
