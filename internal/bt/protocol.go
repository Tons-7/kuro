package bt

import (
	pp "kuro/internal/bt/peer_protocol"
)

func makeCancelMessage(r Request) pp.Message {
	return pp.MakeCancelMessage(r.Index, r.Begin, r.Length)
}
