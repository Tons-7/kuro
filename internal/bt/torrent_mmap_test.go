//go:build !wasm
// +build !wasm

package bt

import (
	"testing"

	"kuro/internal/bt/storage"
)

func TestEmptyFilesAndZeroPieceLengthWithMMapStorage(t *testing.T) {
	cfg := TestingConfig(t)
	ci := storage.NewMMap(cfg.DataDir)
	defer ci.Close()
	cfg.DefaultStorage = ci
	testEmptyFilesAndZeroPieceLength(t, cfg)
}
