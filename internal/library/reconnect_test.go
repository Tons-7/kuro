package library

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/db"
	"kuro/internal/store"
)

// An expired token used to read as a successful sync with the tracker still
// "Connected"; it has to fail and say reconnect, then clear once it works.
func TestRejectedTokenAsksToReconnect(t *testing.T) {
	var expired atomic.Bool
	expired.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expired.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Invalid token","status":401}]}`)
			return
		}
		io.WriteString(w, `{"data":{"SaveMediaListEntry":{"id":77,"mediaId":100,"progress":3,"updatedAt":1000}}}`)
	}))
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	ctx := context.Background()
	st.ImportList(ctx, []store.Anime{{ID: 100, Romaji: "Frieren"}}, nil, store.ImportMerge)
	st.MarkWatched(ctx, 100, 3)

	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	al.SetToken("tok")
	sync := NewSync(st, al, discard())

	if _, err := sync.Run(ctx); !errors.Is(err, ErrReconnect) {
		t.Fatalf("err = %v, want ErrReconnect", err)
	}
	if v, _ := st.Setting(ctx, AuthErrorSetting); v == "" {
		t.Fatal("reconnect not recorded")
	}

	expired.Store(false)
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.Setting(ctx, AuthErrorSetting); v != "" {
		t.Fatal("reconnect flag kept after a working sync")
	}
}
