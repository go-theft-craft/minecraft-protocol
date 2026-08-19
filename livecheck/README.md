# Live check against a real Java 26.1 server

This directory holds two tests. Both connect to a running Minecraft Java
Edition 26.1 server, log in without authentication, and reach play, and both
measure the largest raw frame and the largest decoded body the connection
produced, per state.

- `TestLiveLoginReachesPlay` reads one play packet and disconnects. It measures
  handshaking, login, and configuration, where registry data is the largest
  thing a server sends.
- `TestLivePlayMeasuresLimits` stays in play for a window, answering what a
  server needs answered to keep streaming, and measures play, where chunk data
  is the largest thing a server sends.

Everything else in this repository compares the code against a specification or
against another implementation of one. This compares it against a real server,
which is the only thing that can say whether the resource limits chosen from
the specification survive contact with the data a server actually sends.

No server jar is committed here, and nothing in this directory downloads one.

## Running it

The checks are behind the `livecheck` build tag and the `MCPROTO_LIVE_ADDR`
environment variable. With the variable unset they skip, so an accidental
inclusion in a broader run cannot break a build that has no server.

```bash
MCPROTO_LIVE_ADDR=127.0.0.1:25565 devbox run -- go test -tags livecheck -v ./livecheck
```

Or through the task, which passes the tag for you:

```bash
MCPROTO_LIVE_ADDR=127.0.0.1:25565 devbox run -- task check:live
```

`MCPROTO_LIVE_PLAY_SECONDS` sets how long the play check stays connected. The
default is 60 seconds, which is enough to see chunk data; the measurements
below were taken at 180, which is enough for a default world at view distance
10 to finish streaming its chunks. A farther view distance or a slower disk
wants more.

## Preparing a server

Any Java Edition 26.1 server works. These are the settings the checks need, and
the reason for each.

1. Download a Paper 26.1 build from <https://papermc.io/downloads>, or a
   vanilla 26.1 server jar, into a directory outside this repository.
2. Run it once so it writes its configuration, then accept the licence:

   ```bash
   java -Xmx2G -jar paper-26.1-<build>.jar --nogui
   # then, in eula.txt:
   eula=true
   ```

3. In `server.properties`, set:

   ```properties
   online-mode=false
   ```

   The checks log in offline. With `online-mode=true` the server asks for
   encryption and expects a session-server join, which would mean putting a
   real account's credentials into a test.

   For the play check, also record `level-type`, `level-seed`, `view-distance`,
   and `network-compression-threshold` alongside the result. All four change
   what the numbers mean: a superflat world has far less to send per chunk than
   a default one, and compression is what the raw-frame number measures against.

4. Start the server and wait for `Done`, then run the checks against
   `127.0.0.1:25565`.

Record which implementation was used, and its build number, alongside any
measurement: two server implementations do not send the same registry data or
the same command tree, and the sizes these checks report are a property of the
server as much as of the protocol.

## What to record

The tests log, per state: the largest raw frame and the packet it belonged to,
the largest decoded body and its packet, how many frames and packets were
observed, the totals, the limits in force so the headroom is visible, and the
ten largest packet types by decoded body. All of it belongs in the milestone
record, because the point is the measurement rather than the pass.

The checks run with the default limits. A failure there is a result: it says
the defaults are wrong for a real server, and the fix is to raise the limit the
measurement touched — rounded up to the next power of two — and to record which
packet forced it. Do not raise a limit the measurement did not touch.

## What the play check answers, and why it has to

A server stops sending if it is not answered, and a connection that has stopped
receiving looks healthy while measuring nothing. Four replies keep it going:

- `keep_alive`, or the server disconnects mid-window;
- `teleport_confirm`, owed on every server correction rather than only the
  first;
- `player_loaded`, once, which releases the server's loading hold;
- `chunk_batch_received`, or the server sends exactly one batch and waits
  forever for an acknowledgement that never comes.

The check also sends one `settings` packet asking for view distance 32, which
every server clamps to its own. Nothing in this repository needs a settings
packet to reach play — the negotiator sends none and neither does the headless
client — but a vanilla server defaults an unasked client to a view distance of
2 and streams 49 chunks. Without it, the play measurement would report the
largest chunk out of a corner of spawn as though it were the largest chunk a
server sends.

## What it measured

Three runs across two servers, 2026-08-19, offline mode, default (non-flat)
world, seed `orbit1889`, view distance 10, compression threshold 256, on
loopback, with a 180-second play window. Every run reached play, streamed 473
chunk packets, and disconnected cleanly with every packet decoded under the
default limits.

**Paper 26.1.2 build 74** (`sha256
1d70b1da…95e5f7`), the same build the 2026-08-16 login run used, first of two
runs:

| State | Largest raw frame | Largest decoded body | Frames |
| --- | --- | --- | --- |
| handshaking | 17 bytes (`set_protocol`) | 16 bytes | 1 |
| login | 33 bytes (`success`) | 30 bytes | 4 |
| configuration | 12,643 bytes (`tags`) | 32,316 bytes (`tags`) | 35 |
| **play** | **9,457 bytes (`map_chunk`)** | **60,174 bytes (`map_chunk`)** | 70,813 |

**Vanilla 26.1.2** (`sha1 97ccd4c0…6f9bc51`), the jar the vanilla conformance
lane pins:

| State | Largest raw frame | Largest decoded body | Frames |
| --- | --- | --- | --- |
| handshaking | 17 bytes (`set_protocol`) | 16 bytes | 1 |
| login | 33 bytes (`success`) | 30 bytes | 4 |
| configuration | 12,640 bytes (`tags`) | 32,316 bytes (`tags`) | 35 |
| **play** | **8,475 bytes (`map_chunk`)** | **58,800 bytes (`map_chunk`)** | 78,213 |

A repeat of the Paper run reported 9,777 bytes on the wire and 58,800 decoded.
The world is alive between runs — mobs move, blocks tick, chunks compress
differently — so the numbers wobble by a few percent in both directions and no
single run is *the* measurement. Across all three, nothing exceeded **9,777
bytes on the wire** or **60,174 bytes decoded**.

Against the defaults — 2 MiB per frame, 8 MiB decompressed — that is 214x
headroom on the frame limit and 139x on the decompressed one. **Play is
measured.** Chunk data is the largest thing either server sent, and it is two
orders of magnitude inside the ceilings.

Three things the numbers say that the headline does not:

- **Play raised the ceiling by a factor of two, not an order of magnitude.**
  Configuration's largest body is a 32,316-byte `tags` packet and play's is a
  60,174-byte chunk. What play changes is the volume, not the size: it decoded
  24 MB from 3.8 MB on the wire across 70,813 frames, where configuration
  decoded 140 KB from 30 KB across 35. A limit chosen from login traffic was
  never going to be far wrong for play, and this is why.
- **Real chunk data expands further than the gap between the two defaults.** A
  chunk decodes to 6.4 times its frame; the defaults are 2 MiB and 8 MiB, a
  factor of 4. So the decompressed limit, not the frame limit, is the one a
  large frame reaches first — which is the right way round, because the frame
  limit is what an attacker controls directly and the decompressed one is what
  a decompression bomb has to get past. It does mean the two ceilings are not
  independent: raising the frame limit without raising the decompressed limit
  buys nothing for traffic that compresses like this.
- **A busier server sends more, not bigger.** Paper's command tree is 715 bytes
  against vanilla's 265, and its per-tick block traffic is an order of
  magnitude heavier, but neither moved the largest packet: that stayed a chunk
  on both.

The measurement covers one seed at one view distance on two servers. A world
with more block entities in a column — a village, a base, a chunk full of
chests — can send a larger chunk packet than any of the 473 sampled here, and
nothing in this measurement bounds how much larger. What it bounds is
the shape: chunk data is the thing to watch, and it starts two orders of
magnitude below the ceiling.

The earlier login run recorded 12,564 bytes for the largest configuration
frame; this one records 12,643 on the same Paper build. The difference is the
server's own configuration, not the protocol — `tags` compresses differently
against different world settings — which is why a measurement records the
server it ran against.

The first run of the login check did not reach play. It found that the
negotiator never answered `select_known_packs`, and a real server sends no
registry data until it is answered, so the connection stalled in configuration
while looking healthy. Every scripted test passed throughout, because no script
sent the packet. That is the case for keeping these checks.
