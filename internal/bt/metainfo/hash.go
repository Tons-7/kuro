package metainfo

import (
	"kuro/internal/bt/types/infohash"
)

// This type has been moved to allow avoiding importing everything in metainfo to get at it.

const HashSize = infohash.Size

type Hash = infohash.T

var (
	NewHashFromHex = infohash.FromHexString
	HashBytes      = infohash.HashBytes
)
