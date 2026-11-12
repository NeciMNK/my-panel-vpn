package service

import (
	"errors"
	"reflect"
	"testing"
)

// Clients paste links and mixed case; the admin must see bare hosts, and junk
// must be named so the client can fix it instead of the admin guessing.
func TestParseSuggestedDomains(t *testing.T) {
	got, err := ParseSuggestedDomains("https://Chat.DeepSeek.com/a/b?x=1, www.poe.com\nperplexity.ai:443 poe.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"chat.deepseek.com", "poe.com", "perplexity.ai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	if _, err := ParseSuggestedDomains("poe.com, not a site!"); err == nil || err.Error() != "not a domain: not" {
		t.Fatalf("err = %v, want the bad word named", err)
	}
	if _, err := ParseSuggestedDomains("  "); !errors.Is(err, ErrNoSuggestedDomain) {
		t.Fatalf("err = %v, want ErrNoSuggestedDomain", err)
	}
}

// "Added all" merges the suggestion into the list once, skipping what the list
// already holds; a second answer to the same suggestion changes nothing.
func TestResolveAddedMergesTheSuggestionOnce(t *testing.T) {
	setupSettingTestDB(t)
	s := &AIDomainService{}
	if err := s.Save("domain:openai.com, full:poe.com"); err != nil {
		t.Fatal(err)
	}
	sg, err := s.Suggest(42, "anna", []string{"poe.com", "deepseek.com", "openai.com"})
	if err != nil {
		t.Fatal(err)
	}
	got, claimed, err := s.Resolve(sg.Id, AISuggestAdded)
	if err != nil || !claimed || got.Status != AISuggestAdded || got.TgUserId != 42 {
		t.Fatalf("Resolve = %+v %v %v, want the claimed added suggestion", got, claimed, err)
	}
	want := []string{"domain:openai.com", "full:poe.com", "domain:deepseek.com"}
	if list := (&SettingService{}).GetTgAIExitDomains(); !reflect.DeepEqual(list, want) {
		t.Fatalf("list = %v, want %v", list, want)
	}
	if _, claimed, err := s.Resolve(sg.Id, AISuggestDeclined); err != nil || claimed {
		t.Fatalf("second answer claimed=%v err=%v, want an unclaimed no-op", claimed, err)
	}
	late, err := s.Suggest(43, "boris", []string{"grok.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Resolve(late.Id, AISuggestDeclined); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.Resolve(late.Id, AISuggestAdded); err != nil || claimed {
		t.Fatalf("added after declined claimed=%v err=%v", claimed, err)
	}
	if list := (&SettingService{}).GetTgAIExitDomains(); !reflect.DeepEqual(list, want) {
		t.Fatalf("a declined suggestion reached the list: %v", list)
	}
	view, err := s.View()
	if err != nil || len(view.Suggestions) != 2 || view.Suggestions[1].Status != AISuggestAdded {
		t.Fatalf("view = %+v %v, want the suggestion still marked added", view, err)
	}
}

// Only the admin's answers are verdicts; "pending" or a typo must not close it.
func TestResolveRejectsAnUnknownVerdict(t *testing.T) {
	setupSettingTestDB(t)
	s := &AIDomainService{}
	sg, err := s.Suggest(42, "anna", []string{"poe.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, verdict := range []string{AISuggestPending, "maybe"} {
		if _, _, err := s.Resolve(sg.Id, verdict); !errors.Is(err, ErrAISuggestVerdict) {
			t.Fatalf("Resolve(%q) err = %v, want ErrAISuggestVerdict", verdict, err)
		}
	}
}

// A token Xray cannot parse would make it refuse every entry's config.
func TestSaveAIDomainsRejectsABadToken(t *testing.T) {
	setupSettingTestDB(t)
	s := &AIDomainService{}
	if err := s.Save("openai.com, regexp:("); err == nil || err.Error() != "AI domains: not a domain rule: regexp:(" {
		t.Fatalf("err = %v, want the bad token named", err)
	}
	if err := s.Save("openai.com, domain:claude.ai"); err != nil {
		t.Fatalf("a valid list was refused: %v", err)
	}
	if list := (&SettingService{}).GetTgAIExitDomains(); !reflect.DeepEqual(list, []string{"openai.com", "domain:claude.ai"}) {
		t.Fatalf("list = %v", list)
	}
}
