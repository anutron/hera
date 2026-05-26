package ops

import (
	"strings"
	"testing"
)

func TestHelpContent_CoversAllFocusStates(t *testing.T) {
	sections := HelpContent()
	titles := make([]string, 0, len(sections))
	for _, s := range sections {
		titles = append(titles, s.Title)
	}
	joined := strings.ToLower(strings.Join(titles, ","))
	for _, want := range []string{"focus traversal", "rail", "modal"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("HelpContent missing section %q; got titles %v", want, titles)
		}
	}
}

func TestHelpContent_MutationKeysDocumented(t *testing.T) {
	keys := map[string]bool{"n": false, "r": false, "^d": false, "a": false, "l": false, "?": false}
	for _, sect := range HelpContent() {
		for _, b := range sect.Bindings {
			if _, ok := keys[b.Key]; ok {
				keys[b.Key] = true
			}
		}
	}
	for k, seen := range keys {
		if !seen {
			t.Errorf("mutation key %q not documented in HelpContent", k)
		}
	}
}

func TestHelpContent_DismissKeyIsQ(t *testing.T) {
	for _, sect := range HelpContent() {
		if sect.Title != "This modal" {
			continue
		}
		for _, b := range sect.Bindings {
			if b.Key == "q" {
				return
			}
		}
	}
	t.Fatalf("expected q to be documented as the modal dismiss key")
}
