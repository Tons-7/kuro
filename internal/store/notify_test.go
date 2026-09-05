package store

import (
	"context"
	"testing"
)

func TestNotificationLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	animeID, episode := 1, 7
	id, err := s.AddNotification(ctx, Notification{
		Kind: NotifyRelease, AnimeID: &animeID, Episode: &episode,
		Title: "Episode 7 is available", Body: "[SubsPlease] Show - 07",
		Payload: map[string]any{"infoHash": "abc"},
	})
	if err != nil || id == 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}

	items, err := s.Notifications(ctx, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d notifications", len(items))
	}

	n := items[0]
	if n.Read {
		t.Error("a new notification should be unread")
	}
	if n.Episode == nil || *n.Episode != 7 {
		t.Errorf("episode = %v", n.Episode)
	}
	if n.Payload["infoHash"] != "abc" {
		t.Errorf("payload = %v", n.Payload)
	}

	if count, _ := s.UnreadCount(ctx); count != 1 {
		t.Errorf("unread = %d", count)
	}
	if err := s.MarkRead(ctx, id); err != nil {
		t.Fatal(err)
	}
	if count, _ := s.UnreadCount(ctx); count != 0 {
		t.Errorf("unread after marking = %d", count)
	}
}

func TestMarkAllRead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for range 3 {
		s.AddNotification(ctx, Notification{Kind: NotifyRelease, Title: "x"})
	}
	if err := s.MarkRead(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if count, _ := s.UnreadCount(ctx); count != 0 {
		t.Fatalf("unread = %d after marking all", count)
	}

	unread, _ := s.Notifications(ctx, true, 0)
	if len(unread) != 0 {
		t.Fatalf("unread filter returned %d", len(unread))
	}
	all, _ := s.Notifications(ctx, false, 0)
	if len(all) != 3 {
		t.Fatalf("marking read should not delete: got %d", len(all))
	}
}

// Polling runs every half hour; without this the same release is announced
// again on every pass.
func TestNotifiedReleaseIsRecordedOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if seen, _ := s.AlreadyNotified(ctx, "hash1", 0, 0); seen {
		t.Fatal("an unseen release reported as notified")
	}

	for range 3 {
		if err := s.RecordNotified(ctx, "hash1", 1, 7); err != nil {
			t.Fatal(err)
		}
	}
	if seen, _ := s.AlreadyNotified(ctx, "hash1", 0, 0); !seen {
		t.Fatal("a recorded release should report as notified")
	}
	if seen, _ := s.AlreadyNotified(ctx, "hash2", 0, 0); seen {
		t.Fatal("a different release reported as notified")
	}
}

func TestFollowRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	if err := s.SetFollow(ctx, Follow{AnimeID: 1, Quality: "1080p", Group: "SubsPlease"}, true); err != nil {
		t.Fatal(err)
	}

	got, err := s.Follows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d follows", len(got))
	}
	if got[0].Quality != "1080p" || got[0].Group != "SubsPlease" {
		t.Errorf("follow = %+v", got[0])
	}

	// Following again updates rather than duplicating.
	if err := s.SetFollow(ctx, Follow{AnimeID: 1, Quality: "720p"}, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Follows(ctx)
	if len(got) != 1 || got[0].Quality != "720p" {
		t.Fatalf("follows = %+v", got)
	}

	if err := s.Unfollow(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Follows(ctx); len(got) != 0 {
		t.Fatalf("got %d follows after unfollowing", len(got))
	}
}
