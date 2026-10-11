package engine

import (
	"strings"
	"testing"
	"time"
)

func TestHeadcountReportsWhoJoinedAndLeft(t *testing.T) {
	var h headcount
	at := time.Now()

	joined, left, due := h.note(at, []string{"b:1", "a:1"})
	if !due || strings.Join(joined, ",") != "a:1,b:1" || len(left) != 0 {
		t.Fatalf("first look: joined %v left %v due %v, want both peers joined", joined, left, due)
	}

	// Nothing changed: no line, however long it has been.
	if _, _, due := h.note(at.Add(10*censusEvery), []string{"a:1", "b:1"}); due {
		t.Fatal("a line was due with the same peers as before")
	}

	joined, left, due = h.note(at.Add(20*censusEvery), []string{"a:1", "c:1"})
	if !due || strings.Join(joined, ",") != "c:1" || strings.Join(left, ",") != "b:1" {
		t.Fatalf("joined %v left %v due %v, want c joined and b left", joined, left, due)
	}
}

// A swarm that churns every few seconds gets one line per interval, and that line still names everyone.
func TestHeadcountHoldsChangesUntilALineIsDue(t *testing.T) {
	var h headcount
	at := time.Now()
	h.note(at, []string{"a:1"})

	if _, _, due := h.note(at.Add(censusEvery/3), []string{"a:1", "b:1"}); due {
		t.Fatal("a second line inside the interval")
	}
	// b came and went between lines: it is reported as both, not lost.
	if _, _, due := h.note(at.Add(2*censusEvery/3), []string{"c:1"}); due {
		t.Fatal("a second line inside the interval")
	}
	joined, left, due := h.note(at.Add(censusEvery), []string{"c:1"})
	if !due || strings.Join(joined, ",") != "b:1,c:1" || strings.Join(left, ",") != "a:1,b:1" {
		t.Fatalf("joined %v left %v due %v, want b and c joined, a and b left", joined, left, due)
	}
}

func TestCensusNamesOnlyAFewAddresses(t *testing.T) {
	many := []string{"a", "b", "c", "d", "e", "f"}
	if got := firstFew(many); len(got) != censusNamed {
		t.Errorf("named %d addresses, want %d", len(got), censusNamed)
	}
	if got := firstFew(many[:2]); len(got) != 2 {
		t.Errorf("named %d of two addresses", len(got))
	}
}
