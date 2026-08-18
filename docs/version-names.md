# Which name a protocol version goes by

Protocol 47 has two names in this module, four lines apart in
`generated/java/v1_8/version.go`, and the difference is a decision rather than
a drift. This is the record of it; the test that pins both names together is
`TestProtocol47HasTwoNamesAndTheContractSaysWhichIsWhich` in
`generated/java/v1_8/data_test.go`.

**`1.8.9` names the dataset.** It is the version PrismarineJS published the
data under, the version `mcreference` prepares a workspace for, and therefore
the name in every dataset path: `data.Load("java/1.8.9")`, the generated
package's `VersionName`, and a `task server:vanilla VERSION=1.8.9`
invocation. Anything that fetches, generates, or addresses game data speaks
this name.

**`1.8.8` is what a client is told.** It is `data.Version.MinecraftVersion`,
the string that belongs in a status response's version field and anywhere else
the wire states a version name, because it is what protocol 47 clients call
themselves and what the independent Node implementation lists for protocol 47
(`minecraft-protocol@1.66.2` drives it as `1.8.8`). M3 reconciled the two
without changing a byte and called the reconciliation a decision of its own;
this document is that decision written down.

The two consumers of the rule, named because the record is for them:
`server`'s `internal/server/protocolinfo/protocolinfo.go` advertises `1.8.8`
and pins it with a test, and its `interop/node/runner.mjs` drives Node at
`1.8.8` because that is the name the independent implementation knows the
protocol by.

For protocol 775 the names coincide and the split moves one level up, which M4
already settled: `26.1` names the family — the dataset, the generated package
`generated/java/v26_1`, the ProtoDef schema — and `26.1.2` names a build, the
jar a lane runs against. A status response there advertises the build.
