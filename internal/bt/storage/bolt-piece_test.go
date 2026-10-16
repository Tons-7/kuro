package storage_test

import (
	"testing"

	"kuro/internal/bt/storage"
	"kuro/internal/bt/test"
)

func TestBoltLeecherStorage(t *testing.T) {
	test.TestLeecherStorage(t, test.LeecherStorageTestCase{Name: "Boltdb", Factory: storage.NewBoltDB})
}
