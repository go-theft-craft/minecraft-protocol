# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## This repository is public

`minecraft-protocol` is a public repository, and so are `headless-minecraft`,
`minecraft-reference`, `minecraft-simulation`, and `server`. The proxy and
research repositories are private.

Anything committed here is published permanently, not just visibly. Go's module
mirror fetches this repository automatically and serves immutable snapshots
from `proxy.golang.org`, and `sum.golang.org` records their hashes in an
append-only log. Neither can be retracted. Rewriting git history removes
content from GitHub only — the cached module zip keeps serving it. Treat a
commit here as unrecallable.

## Naming the private proxy project

The private proxy targets a third-party server that speaks its own protocol.
Do not name that server, that protocol, or that project in this repository, in
any of the other public repositories, or in commit messages.

Refer to it by role:

- "the legacy protocol" — the wire format the private proxy speaks
- "the legacy proxy" or `proxy` — the repository itself
- "the legacy codec" — its packet encoding and decoding

Do not write the project's codename, the private repository's directory name,
or invented source paths built from either. The 2026-08-13 shared-protocol
extraction plan once named a `proxy/internal/<codename>` directory that does
not exist; describe behaviour instead of guessing at paths in a repository you
cannot see from here.

This is a naming rule for public repositories, not a claim that the work is
secret. Inside the private repositories, use whatever names are accurate.
