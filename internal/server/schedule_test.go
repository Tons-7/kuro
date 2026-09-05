package server

import (
	"testing"
	"time"
)

// A week of tabs must be seven distinct weekdays. An eighth entry would repeat
// a weekday and break a tab strip keyed on the name.
func TestGroupByDayEmitsExactlyOneTabPerDay(t *testing.T) {
	const offset = 120
	start := midnight(time.Now(), offset).Unix()

	days := groupByDay(nil, offset, start, 7)
	if len(days) != 7 {
		t.Fatalf("got %d days, want 7", len(days))
	}

	seen := map[string]bool{}
	for _, d := range days {
		if seen[d.Weekday] {
			t.Fatalf("weekday %s appears twice", d.Weekday)
		}
		seen[d.Weekday] = true
		if d.Items == nil {
			t.Errorf("%s has nil items; the frontend maps over this", d.Date)
		}
	}
}

func TestGroupByDayFlagsToday(t *testing.T) {
	const offset = -300
	start := midnight(time.Now(), offset).Unix()

	days := groupByDay(nil, offset, start, 7)
	if !days[0].Today {
		t.Error("a window starting at local midnight should open on today")
	}
	for _, d := range days[1:] {
		if d.Today {
			t.Fatalf("%s also claims to be today", d.Date)
		}
	}
}

// A 25:30 JST broadcast is Tuesday in Japan but Monday for a US viewer. It lands
// on the viewer's tab; the JST label rides along because fansub sites quote it.
func TestGroupByDaySplitsLateNightBroadcastFromItsJSTDay(t *testing.T) {
	const offset = -300

	airing := time.Date(2026, 8, 11, 1, 30, 0, 0, jst)
	item := scheduleItem{
		AnimeID:    1,
		Episode:    6,
		AiringAt:   int(airing.Unix()),
		JSTWeekday: airing.In(jst).Format("Monday"),
	}

	start := midnight(airing, offset).Unix()
	days := groupByDay([]scheduleItem{item}, offset, start, 2)

	if days[0].Weekday != "Monday" {
		t.Fatalf("viewer weekday = %s, want Monday", days[0].Weekday)
	}
	if len(days[0].Items) != 1 {
		t.Fatalf("episode landed on %d items for Monday", len(days[0].Items))
	}
	if got := days[0].Items[0].JSTWeekday; got != "Tuesday" {
		t.Errorf("jstWeekday = %s, want Tuesday", got)
	}
	if len(days[1].Items) != 0 {
		t.Error("episode counted twice")
	}
}

// Anything outside the window is dropped rather than folded into an edge day.
func TestGroupByDayIgnoresOutOfWindowItems(t *testing.T) {
	const offset = 0
	start := midnight(time.Now(), offset).Unix()

	items := []scheduleItem{
		{AnimeID: 1, AiringAt: int(start - 3600)},
		{AnimeID: 2, AiringAt: int(start + 3*24*3600)},
		{AnimeID: 3, AiringAt: int(start + 30*24*3600)},
	}

	days := groupByDay(items, offset, start, 7)
	total := 0
	for _, d := range days {
		total += len(d.Items)
	}
	if total != 1 {
		t.Fatalf("kept %d items, want only the in-window one", total)
	}
	if days[3].Items[0].AnimeID != 2 {
		t.Errorf("item landed on the wrong day")
	}
}

func TestMidnightSnapsToLocalDayStart(t *testing.T) {
	// 00:30 UTC on the 11th is still the 10th at UTC-5.
	at := time.Date(2026, 8, 11, 0, 30, 0, 0, time.UTC)

	got := midnight(at, -300)
	if got.Format("2006-01-02 15:04") != "2026-08-10 00:00" {
		t.Fatalf("midnight = %s", got.Format("2006-01-02 15:04"))
	}
	if got.After(at) {
		t.Error("midnight must not land in the future")
	}
}
