# Vanilla Java 26.1 client check

Run on 2026-08-16 against a real Minecraft Java Edition 26.1 client on
loopback. This is the half of M4's release gate that no automated test can
reach: whether the generated protocol 775 codecs read what a real client sends.

## Result

**3,612 packets from a real client, none of which failed to decode.**

```
scripted 6144   written 6130   read 3612   state play

play/tick_end          2630     play/position_look      418
play/position           260     play/look               201
play/player_input        61     play/keep_alive          22
play/entity_action        8     play/block_place          2
play/close_window         1     play/player_loaded        1
play/settings             1     play/teleport_confirm     1
configuration/settings    1     configuration/custom_payload    1
configuration/select_known_packs 1
configuration/finish_configuration 1
login/login_start         1     login/login_acknowledged  1
```

The client completed handshake, login, configuration, and the handoff to play,
spawned in the world, and played until it disconnected.

Eight of these had never been decoded by anything in this repository:
`player_input`, `entity_action`, `block_place`, `close_window`,
`player_loaded`, `tick_end`, and both `settings` packets. `block_place`
carries a `Slot`, which is the most intricate structure protocol 775 defines.

## How it was run

There is no protocol 775 server here to point a client at — `login.Acceptor` is
still hard-coded to protocol 47 — so the client was served from a recording of
a real one:

```bash
mcproto capture --address <paper 26.1> --output script.mcpcap \
  --username scripter --offline --play-for 10s
mcproto serve --script script.mcpcap --address 127.0.0.1:25565
```

`serve` walks the capture: it decodes each recorded clientbound frame, writes
it to the live client through this repository's own encoder, and waits at each
point the recording shows the client speaking. Every packet the client sends is
decoded and counted. The encoder is in the path deliberately — a codec that
reads a packet but writes it back differently is caught by the client rather
than by a test that only ever talks to itself.

The world is a recording, so nothing the player does has any effect. That does
not weaken the result: the client sends the same packets either way, which is
what is being checked.

## What the exercise found

**Network NBT required a compound root, and real servers do not send one.** The
plain-text form of a text component is a bare `TAG_String`. Paper 26.1 sends
its MOTD in `server_data` that way, and the reader rejected the packet — as it
would have rejected every chat message, kick reason, title, and playerlist
header whose component was plain text. A client built on this would have
dropped its connection the first time anybody spoke.

The defect was invisible to every test here and to M4's own live check, which
read a single play packet and stopped. It surfaced immediately when a capture
tried to record ten seconds of play: the recording stalled at eleven packets.
With the reader fixed, the same ten seconds records 6,145 frames across 49
distinct play packet types, all of which decode.

## What it did not cover

- **The acceptor.** This harness replays a recording; it does not implement a
  server. `login.Acceptor` remains protocol 47 only, and giving it the
  role-driven treatment M4 gave the negotiator is M6 work.
- **`window_click`, `chat`, `block_dig`, `use_item`, `held_item_slot`.** The
  session did not produce them. They are reachable by the same method; it takes
  someone at the client doing those things.
- **Encryption.** The recording is offline mode, so no key exchange happened on
  the client's connection.
