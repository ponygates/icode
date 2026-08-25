package db

import (
	"strings"
	"testing"
)

func TestAgentMessageRoundtrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.SendAgentMessage("sess-a", "sess-b", "please review main.go"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := s.SendAgentMessage("sess-c", "sess-b", "second note"); err != nil {
		t.Fatal(err)
	}
	// Message addressed elsewhere must not appear in sess-b's inbox.
	if err := s.SendAgentMessage("sess-a", "other", "not for b"); err != nil {
		t.Fatal(err)
	}

	inbox, err := s.AgentInbox("sess-b", 20, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 {
		t.Fatalf("inbox len = %d, want 2", len(inbox))
	}
	// Newest first.
	if inbox[0].Body != "second note" || inbox[0].FromID != "sess-c" {
		t.Errorf("inbox[0] = %+v", inbox[0])
	}
	if !inbox[0].ReadAt.IsZero() {
		t.Error("fresh message should be unread")
	}

	unread, _ := s.AgentInbox("sess-b", 20, true)
	if len(unread) != 2 {
		t.Errorf("unread = %d, want 2", len(unread))
	}
	if err := s.MarkAgentMessagesRead("sess-b"); err != nil {
		t.Fatal(err)
	}
	unread2, _ := s.AgentInbox("sess-b", 20, true)
	if len(unread2) != 0 {
		t.Errorf("after mark-read, unread = %d, want 0", len(unread2))
	}
	all, _ := s.AgentInbox("sess-b", 20, false)
	for _, m := range all {
		if m.ReadAt.IsZero() {
			t.Error("message still unread after MarkAgentMessagesRead")
		}
	}
}

func TestSendAgentMessageValidation(t *testing.T) {
	s := newTestStore(t)
	if err := s.SendAgentMessage("", "b", "x"); err == nil {
		t.Error("empty from should error")
	}
	if err := s.SendAgentMessage("a", " ", "x"); err == nil {
		t.Error("blank to should error")
	}
}

func TestAgentInboxLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		if err := s.SendAgentMessage("a", "me", strings.Repeat("m", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.AgentInbox("me", 2, false)
	if len(got) != 2 {
		t.Fatalf("limit ignored: got %d", len(got))
	}
}
