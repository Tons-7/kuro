package bt

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kuro/internal/bt/internal/testutil"
	"kuro/internal/bt/metainfo"
	"kuro/internal/bt/storage"
)

func TestHashPieceAfterStorageClosed(t *testing.T) {
	cl := newTestingClient(t)
	td := t.TempDir()
	cs := storage.NewFile(td)
	defer cs.Close()
	tt := cl.newTorrent(metainfo.Hash{1}, cs)
	mi := testutil.GreetingMetaInfo()
	require.NoError(t, tt.SetInfoBytes(mi.InfoBytes))
	go tt.piece(0).VerifyDataContext(t.Context())
	require.NoError(t, tt.storage.Close())
}
