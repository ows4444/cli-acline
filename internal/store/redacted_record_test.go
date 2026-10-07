package store

import (
	"fmt"
	"strings"
	"testing"
)

const pastedPAT = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

// A spec, decision or memory entry with a pasted secret is stored redacted,
// and its secret_redacted event names it, written with the record itself.
func TestNewRecordsRedactASecretAndSayWhich(t *testing.T) {
	s := humanStore(t)
	specID, err := s.AddSpec("login", "token is "+pastedPAT)
	if err != nil {
		t.Fatal(err)
	}
	decisionID, err := s.AddDecision("auth", DecisionOpts{Rationale: "uses " + pastedPAT})
	if err != nil {
		t.Fatal(err)
	}
	memoryID, err := s.AddMemory("", "", "the CI token is "+pastedPAT)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSpec("clean", "nothing secret"); err != nil {
		t.Fatal(err)
	}

	if sp, _ := s.GetSpec(specID); strings.Contains(sp.Body.String, pastedPAT) {
		t.Error("spec body kept the secret")
	}
	events, err := s.ListEvents(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	for _, e := range events {
		if e.Type == "secret_redacted" {
			notices = append(notices, e.Message)
		}
		if strings.Contains(e.Message, pastedPAT) {
			t.Errorf("event %q kept the secret", e.Message)
		}
	}
	if len(notices) != 3 {
		t.Fatalf("secret_redacted events = %v, want one per record with a secret", notices)
	}
	for _, want := range []string{
		fmt.Sprintf("spec #%d:", specID), fmt.Sprintf("decision #%d:", decisionID), fmt.Sprintf("memory #%d:", memoryID),
	} {
		if !strings.Contains(strings.Join(notices, "\n"), want) {
			t.Errorf("no secret_redacted event naming %q in %v", want, notices)
		}
	}
}

// A spec revision, note or logged event with a pasted secret gets its
// secret_redacted event in the same transaction too.
func TestRevisionsNotesAndLogsRedactASecretAndSayWhich(t *testing.T) {
	s := humanStore(t)
	specID, _ := s.AddSpec("login", "")
	if _, err := s.ReviseSpec(specID, "now with "+pastedPAT); err != nil {
		t.Fatal(err)
	}
	noteID, err := s.AddNoteWithRole(nil, nil, "token "+pastedPAT, "")
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := s.LogEventWithRole(nil, nil, nil, "bug", "leaked "+pastedPAT)
	if err != nil {
		t.Fatal(err)
	}

	events, _ := s.ListEvents(nil, 0)
	var notices []string
	for _, e := range events {
		if strings.Contains(e.Message, pastedPAT) {
			t.Errorf("event %q kept the secret", e.Message)
		}
		if e.Type == "secret_redacted" {
			notices = append(notices, e.Message)
		}
	}
	all := strings.Join(notices, "\n")
	for _, want := range []string{
		fmt.Sprintf("spec #%d:", specID), fmt.Sprintf("note #%d:", noteID), fmt.Sprintf("log event #%d:", eventID),
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no secret_redacted event naming %q in %v", want, notices)
		}
	}
}
