# Live check against a real Java 26.1 server

This directory holds one test. It connects to a running Minecraft Java Edition
26.1 server, logs in without authentication, reaches play, reads one packet,
and disconnects — and while doing so it measures the largest raw frame and the
largest decoded body the connection produced.

Everything else in this repository compares the code against a specification or
against another implementation of one. This compares it against a real server,
which is the only thing that can say whether the resource limits chosen from
the specification survive contact with the registry data a server actually
sends.

No server jar is committed here, and nothing in this directory downloads one.

## Running it

The check is behind the `livecheck` build tag and the `MCPROTO_LIVE_ADDR`
environment variable. With the variable unset it skips, so an accidental
inclusion in a broader run cannot break a build that has no server.

```bash
MCPROTO_LIVE_ADDR=127.0.0.1:25565 devbox run -- go test -tags livecheck -v ./livecheck
```

Or through the task, which passes the tag for you:

```bash
MCPROTO_LIVE_ADDR=127.0.0.1:25565 devbox run -- task check:live
```

## Preparing a server

Any Java Edition 26.1 server works. These are the settings the check needs, and
the reason for each.

1. Download a Paper 26.1 build from <https://papermc.io/downloads> into a
   directory outside this repository.
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

   The check logs in offline. With `online-mode=true` the server asks for
   encryption and expects a session-server join, which would mean putting a
   real account's credentials into a test.

4. Start the server and wait for `Done`, then run the check against
   `127.0.0.1:25565`.

Vanilla works in place of Paper. Record which one was used, and its build
number, alongside any measurement: two server implementations do not send the
same registry data, and the sizes this check reports are a property of the
server as much as of the protocol.

## What to record

The test logs five numbers. All five belong in the milestone record, because
the point is the measurement rather than the pass:

- the largest raw frame, and the packet it belonged to;
- the largest decoded body, and its packet;
- how many packets were observed;
- the frame and decompressed limits in force, so the headroom is visible.

The check runs with the default limits. A failure there is a result: it says
the defaults are wrong for a real server, and the fix is to raise the limit the
measurement touched — rounded up to the next power of two — and to record which
packet forced it. Do not raise a limit the measurement did not touch.

## What it measured

Run on 2026-08-16 against Paper 26.1.2 build 74, offline mode, default world,
default view distance, on loopback.

| Measurement | Value | Limit | Headroom |
| --- | --- | --- | --- |
| Largest raw frame | 12,564 bytes (a compressed `configuration/tags` frame) | 2 MiB | 167x |
| Largest decoded body | 32,316 bytes (`configuration/tags`) | 8 MiB | 259x |
| Packets observed | 41 | | |

Both defaults hold with room to spare through login. The measurement covers the
sequence this check drives — handshake, login, configuration, and the first
play packet — and says nothing about play itself, where chunk data is the
largest thing a server sends and no check here has measured it yet.

The first run of this check did not reach play. It found that the negotiator
never answered `select_known_packs`, and a real server sends no registry data
until it is answered, so the connection stalled in configuration while looking
healthy. Every scripted test passed throughout, because no script sent the
packet. That is the case for keeping this check.
