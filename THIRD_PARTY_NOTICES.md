# Third-party notices

## PrismarineJS minecraft-data

The files in `source/java/1.8`, and the Go data generated from them, come from
the PrismarineJS `minecraft-data` repository:

- Repository: <https://github.com/PrismarineJS/minecraft-data>
- Revision: `3f0dd2ac525607b21be7cd6ddd003fa9057a72d2`
- Upstream path: `data/pc/1.8`
- Local path: `source/java/1.8`
- License identifier declared upstream: `MIT`

All 17 local source files match the files at that revision byte for byte. The
upstream repository did not contain a standalone license file at the pinned
revision. Its `README.md` declared `MIT` and noted that some data was extracted
from other sources, whose terms might require a different license. See the
[pinned upstream license section](https://github.com/PrismarineJS/minecraft-data/blob/3f0dd2ac525607b21be7cd6ddd003fa9057a72d2/README.md#license).

### Java 26.1

The files in `source/java/26.1` come from the same repository at a later
revision:

- Revision: `8a80816cbfb3fe2b609f2cde4e57796c8033af61`
- Upstream path: `data/pc/26.1`, plus the aliased directories below
- Local path: `source/java/26.1`
- License identifier declared upstream: `MIT`

All 25 datasets match that revision byte for byte; `manifest.json` records each
one's upstream path and checksum. Upstream resolves six of them from older
directories rather than from `26.1`, which is upstream's decision and not a
fetch error:

| Dataset | Upstream directory |
| --- | --- |
| `blockLoot` | `data/pc/1.20` |
| `entityLoot` | `data/pc/1.20` |
| `commands` | `data/pc/1.20.3` |
| `mapIcons` | `data/pc/1.20.2` |
| `windows` | `data/pc/1.16.1` |
| `proto` | `data/pc/latest` |

Anything reading `windows` is reading a nine-year-old window layout. Run
`mcproto data validate --source source/java/26.1 --format json` to see the
alias flags directly.

The standard MIT license text follows and covers both trees. The pinned
upstream repository did not provide a copyright notice or copyright-holder
line.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## PrismarineJS node-minecraft-protocol

The interoperability tests in `interop/` run the PrismarineJS
`minecraft-protocol` npm package as a separate process on the loopback
interface. No part of it is vendored, linked, or redistributed here: only
`interop/node/package.json` and `interop/node/package-lock.json` reference it.

- Package: <https://www.npmjs.com/package/minecraft-protocol>
- Repository: <https://github.com/PrismarineJS/node-minecraft-protocol>
- Pinned version: `1.66.2`
- License identifier declared upstream: `BSD-3-Clause`

The pinned lock file records the exact integrity hash for that version and for
every transitive dependency it installs.

## Extracted Minecraft constants

`source/java/1.8/physics.json` contains numeric constants measured from a
Minecraft Java Edition 1.8.9 server jar obtained from Mojang, together with
constants transcribed by maintainers from a local research workspace.
`source/java/1.8/blockMovement.json` contains, per block, its registry name and
whether the game's material stops an entity walking into it, measured from the
same jar. The `extracted` block in `source/java/1.8/manifest.json` records the
extraction tool, its revision, the Minecraft version, the side, and the SHA-256
digest of the jar as Mojang published it, so the provenance can be checked
against Mojang's own metadata.

This repository contains no Minecraft jar, no mapping file, no decompiled Java
source, and no game asset. It contains measured values and the registry names
they are keyed by. Minecraft
is a product of Mojang AB. This project is not an official Minecraft product
and is not approved by or associated with Mojang or Microsoft.
