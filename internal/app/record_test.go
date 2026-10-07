package app

import (
	"errors"
	"testing"

	"acline/internal/store"
)

const pastedPAT = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

func TestAddSpecAndDecisionReportARedaction(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}

	spec, err := AddSpec(st, AddSpecRequest{Title: "login", Body: "token " + pastedPAT})
	if err != nil || !spec.Redacted {
		t.Fatalf("AddSpec = %+v, %v; want Redacted", spec, err)
	}
	clean, err := AddSpec(st, AddSpecRequest{Title: "clean"})
	if err != nil || clean.Redacted {
		t.Fatalf("clean AddSpec = %+v, %v; want not Redacted", clean, err)
	}
	dec, err := AddDecision(st, AddDecisionRequest{Title: "auth", Context: pastedPAT})
	if err != nil || !dec.Redacted {
		t.Fatalf("AddDecision = %+v, %v; want Redacted", dec, err)
	}
}

func TestAddSpecAndDecisionRefuseABlankTitle(t *testing.T) {
	st := openTestStore(t)
	if _, err := AddSpec(st, AddSpecRequest{Title: " "}); !errors.Is(err, ErrTitleRequired) {
		t.Errorf("spec: err = %v, want ErrTitleRequired", err)
	}
	if _, err := AddDecision(st, AddDecisionRequest{}); !errors.Is(err, ErrTitleRequired) {
		t.Errorf("decision: err = %v, want ErrTitleRequired", err)
	}
}

func TestAddSpecUsesTheNamedProject(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	pid, _ := st.AddProject("demo", t.TempDir(), "")
	res, err := AddSpec(st, AddSpecRequest{Title: "s", ProjectArg: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if sp, _ := st.GetSpec(res.ID); sp.ProjectID.Int64 != pid {
		t.Errorf("project = %v, want %d", sp.ProjectID, pid)
	}
}

func TestAddMemoryByAnAgentWaitsForReviewAndNamesASimilarEntry(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	first, err := AddMemory(st, AddMemoryRequest{Body: "never run migrations on friday afternoons"})
	if err != nil || first.Pending {
		t.Fatalf("person's AddMemory = %+v, %v; want approved", first, err)
	}

	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	res, err := AddMemory(st, AddMemoryRequest{Body: "Never run migrations on Friday afternoons"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pending || res.Redacted {
		t.Errorf("agent's AddMemory = pending %v redacted %v, want pending only", res.Pending, res.Redacted)
	}
	if res.Similar == nil || res.Similar.ID != first.ID {
		t.Errorf("similar = %v, want #%d", res.Similar, first.ID)
	}
	secret, err := AddMemory(st, AddMemoryRequest{Body: "the CI token is " + pastedPAT})
	if err != nil || !secret.Redacted {
		t.Fatalf("AddMemory with a secret = %+v, %v; want Redacted", secret, err)
	}
}

func TestAddMemoryRefusesABlankBody(t *testing.T) {
	st := openTestStore(t)
	if _, err := AddMemory(st, AddMemoryRequest{Body: "  "}); !errors.Is(err, ErrBodyRequired) {
		t.Fatalf("err = %v, want ErrBodyRequired", err)
	}
}

func TestReviseSpecSaysWhenItWithdrewTheApproval(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	spec, _ := AddSpec(st, AddSpecRequest{Title: "s", Body: "v1"})
	draft, err := ReviseSpec(st, spec.ID, "v2")
	if err != nil || draft.Version != 2 || draft.ApprovalWithdrawn {
		t.Fatalf("draft revise = %+v, %v", draft, err)
	}
	if err := st.ApproveSpec(spec.ID, ""); err != nil {
		t.Fatal(err)
	}
	approved, err := ReviseSpec(st, spec.ID, "v3 "+pastedPAT)
	if err != nil || !approved.ApprovalWithdrawn || !approved.Redacted {
		t.Fatalf("approved revise = %+v, %v; want withdrawn and redacted", approved, err)
	}
	if _, err := ReviseSpec(st, spec.ID, " "); !errors.Is(err, ErrBodyRequired) {
		t.Errorf("blank body: err = %v, want ErrBodyRequired", err)
	}
}

func TestAddNoteReportsARedaction(t *testing.T) {
	st := openTestStore(t)
	res, err := AddNote(st, AddNoteRequest{Body: "ci token " + pastedPAT})
	if err != nil || !res.Redacted {
		t.Fatalf("AddNote = %+v, %v; want Redacted", res, err)
	}
	if _, err := AddNote(st, AddNoteRequest{Body: ""}); !errors.Is(err, ErrBodyRequired) {
		t.Errorf("blank: err = %v, want ErrBodyRequired", err)
	}
}

func TestLogNeedsATypeAndAMessageAndRecordsTheSession(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	if _, err := Log(st, LogRequest{Message: "x"}); !errors.Is(err, ErrLogTypeRequired) {
		t.Errorf("no type: err = %v, want ErrLogTypeRequired", err)
	}
	if _, err := Log(st, LogRequest{Type: "bug", Message: " "}); !errors.Is(err, ErrMessageRequired) {
		t.Errorf("no message: err = %v, want ErrMessageRequired", err)
	}
	if _, err := Log(st, LogRequest{Type: "status_change", Message: "x"}); !errors.Is(err, store.ErrNotAUserLogType) {
		t.Errorf("system type: err = %v, want ErrNotAUserLogType", err)
	}
	sess, err := StartSession(st, StartSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Log(st, LogRequest{Type: "bug", Message: "found it"})
	if err != nil {
		t.Fatal(err)
	}
	events, _ := st.ListEvents(nil, 0)
	for _, e := range events {
		if e.ID == res.ID && e.SessionID.Int64 != sess.ID {
			t.Errorf("event session = %v, want #%d", e.SessionID, sess.ID)
		}
	}
}
