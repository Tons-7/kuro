// Seeds a scratch database for the E2E run with two finished downloads, one
// cached and one kept, so the Downloads page has both tiers to show without the
// engine holding anything.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"kuro/internal/db"
	"kuro/internal/metadata"
	"kuro/internal/store"
)

func main() {
	path := flag.String("db", "", "database to seed")
	anime := flag.Int("anime", 0, "anime id the episodes belong to")
	pack := flag.Bool("pack", false, "also seed a two-episode season pack")
	extras := flag.Bool("extras", false, "also mark episode 2 filler and add a release notification")
	flag.Parse()
	if *path == "" || *anime <= 0 {
		fmt.Fprintln(os.Stderr, "usage: e2e-seed -db kuro.db -anime 127230 [-pack] [-extras]")
		os.Exit(2)
	}
	if err := seed(*path, *anime, *pack, *extras); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seed(path string, anime int, withPack, withExtras bool) error {
	conn, err := db.Open(path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Migrate(); err != nil {
		return err
	}
	st := store.New(conn)
	ctx := context.Background()

	rows := []struct {
		tag     string
		episode int
		kept    bool
	}{{"cached", 2, false}, {"kept", 3, true}}
	for _, r := range rows {
		hash := strings.ToLower(fmt.Sprintf("e2e%037x", r.episode))
		name := fmt.Sprintf("[E2E] Kuro Test Show - %02d [1080p].mkv", r.episode)
		const size = 700 << 20
		if err := st.RecordTorrent(ctx, store.TorrentRecord{
			InfoHash: hash, Name: name, TotalSize: size, AnimeID: anime,
			EpKey: fmt.Sprint(r.episode), FilePath: name,
		}); err != nil {
			return err
		}
		if err := st.SetCacheBytes(ctx, hash, 0, size, true); err != nil {
			return err
		}
		if r.kept {
			if _, err := st.KeepDownload(ctx, hash, true); err != nil {
				return err
			}
		}
	}

	if withExtras {
		// Filler is keyed on the MAL id the series page already stored.
		malID, err := st.MalID(ctx, anime)
		if err != nil || malID == 0 {
			return fmt.Errorf("anime %d has no mal id yet; open its page first", anime)
		}
		if _, err := st.SaveFillers(ctx, []metadata.Filler{{MalID: malID, Episode: 2, Kind: "filler"}}); err != nil {
			return err
		}
		if _, err := st.AddNotification(ctx, store.Notification{
			Kind: store.NotifyRelease, AnimeID: &anime, Episode: new(int(7)),
			Title: "Episode 7 is available", Body: "[E2E] Kuro Test Show - 07 [1080p].mkv",
		}); err != nil {
			return err
		}
	}
	if !withPack {
		return nil
	}
	// A season pack holding two episodes, so the list folds them into one row.
	pack := strings.ToLower(fmt.Sprintf("e2e%037x", 999))
	for i, episode := range []int{4, 5} {
		if err := st.RecordTorrent(ctx, store.TorrentRecord{
			InfoHash: pack, Name: "[E2E] Kuro Test Show - Batch [1080p]", TotalSize: 700 << 20,
			AnimeID: anime, EpKey: fmt.Sprint(episode), FileIndex: i, FilePath: fmt.Sprintf("%02d.mkv", episode),
		}); err != nil {
			return err
		}
		if err := st.SetCacheBytes(ctx, pack, i, 700<<20, true); err != nil {
			return err
		}
	}
	return nil
}
