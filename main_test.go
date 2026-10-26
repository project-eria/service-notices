package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "notices.db"))
	if err != nil {
		t.Fatal(err)
	}
	door := Notice{Key: "garage-open", Source: "house", Severity: "warning", Title: "Porte du garage ouverte", Audience: "profile"}

	first, created, err := st.raise(door)
	if err != nil || !created {
		t.Fatalf("first raise: created=%v err=%v", created, err)
	}
	// Same key while active: updated in place, not a new notice
	door.Severity = "alert"
	second, created, err := st.raise(door)
	if err != nil || created || second.ID != first.ID || second.At != first.At {
		t.Fatalf("second raise: %+v created=%v err=%v", second, created, err)
	}

	if err := st.markRead("cedric", []string{first.ID, "999"}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := st.readIDs("cedric"); len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("read ids: %v", ids)
	}
	if ids, _ := st.readIDs("guest"); len(ids) != 0 {
		t.Fatalf("guest read ids: %v", ids)
	}

	if done, _ := st.resolve("garage-open"); !done {
		t.Fatal("resolve should close the active notice")
	}
	if done, _ := st.resolve("garage-open"); done {
		t.Fatal("nothing left to resolve")
	}
	// Raised again after resolution: a new notice, the old one stays in history
	third, created, _ := st.raise(door)
	if !created || third.ID == first.ID {
		t.Fatalf("third raise: %+v created=%v", third, created)
	}
	list, _ := st.list()
	if len(list) != 2 || list[0].ID != third.ID || list[1].Resolved == "" || list[1].Severity != "alert" {
		t.Fatalf("list: %+v", list)
	}

	// Purge: only the resolved notice goes, with its read state; the active one stays
	if n, err := st.purge(time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if list, _ := st.list(); len(list) != 1 || list[0].ID != third.ID {
		t.Fatalf("after purge: %+v", list)
	}
	if ids, _ := st.readIDs("cedric"); len(ids) != 0 {
		t.Fatalf("read ids after purge: %v", ids)
	}
}

func TestParseRaise(t *testing.T) {
	n, err := parseRaise(map[string]interface{}{"key": "k", "title": "T"})
	if err != nil || n.Source != "house" || n.Severity != "info" || n.Audience != "profile" {
		t.Fatalf("defaults: %+v %v", n, err)
	}
	for _, in := range []map[string]interface{}{
		{"title": "T"},
		{"key": "k", "title": "T", "severity": "critical"},
		{"key": "k", "title": "T", "audience": "guests"},
	} {
		if _, err := parseRaise(in); err == nil {
			t.Fatalf("expected an error for %v", in)
		}
	}
}

func TestReaches(t *testing.T) {
	if !reaches("", "info") || !reaches("warning", "alert") || reaches("alert", "warning") {
		t.Fatal("severity filter")
	}
}
