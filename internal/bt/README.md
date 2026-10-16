# bt

kuro's BitTorrent engine: the client, peer wire protocol, trackers, DHT (`dht/`), metainfo and storage. kuro's
engine package (`internal/engine`) drives it.

Files here are under the Mozilla Public License 2.0 (`LICENSE`, and `dht/LICENSE`), unlike the rest of kuro.
They are derived from open-source code under that licence, and changes to them stay under it.

## How it asks peers for data

`request-queue.go` holds the request queue, built for streaming:

- A peer's queue is topped up once half drained, so its line never idles waiting for a refill.
- The queue holds `RequestQueueTime` (2 s) of what the peer recently delivered, at least `MinRequestQueue` (16).
  A piece asked for next, such as the one under a seek, waits about that long rather than behind megabytes.
- A request moves from one peer to another only to one delivering it in under half the time, or off a peer that
  has sent nothing for `RequestQueueTime`. Slow peers can't take the chunks a reader waits on from fast ones.

## Identity

kuro names itself in `version/`: peer IDs start `-KU0001-` (bump it when kuro changes in a way other peers could
care about), and peers, trackers and the router's port mapping see "kuro".
