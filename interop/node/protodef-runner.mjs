// Codec-level differential harness for protocol 775.
//
// It reads newline-delimited JSON commands on stdin and writes one JSON result
// per command on stdout. Each command names a state and direction, and either
// encodes a packet from its fields or decodes one from its bytes, using the
// pinned Node ProtoDef and the same protocol.json this repository generates
// from. Nothing here opens a socket or reads the network: the point is to
// compare two implementations of the same schema, not two peers.
//
// Commands:
//   {"op":"encode","state":"login","direction":"toClient","name":"success","params":{…}}
//     → {"ok":true,"hex":"…"}
//   {"op":"decode","state":"login","direction":"toClient","hex":"…"}
//     → {"ok":true,"name":"success","params":{…}}
//   {"op":"stop"}
//     → exits
//
// A failure is reported as {"ok":false,"error":"…"} rather than by exiting, so
// one bad fixture does not end the run.

import process from 'node:process'
import readline from 'node:readline'
import fs from 'node:fs'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const { ProtoDefCompiler } = require('protodef').Compiler
const nbt = require('prismarine-nbt')
const minecraftDatatypes = require('minecraft-protocol/src/datatypes/compiler-minecraft')

const schemaPath = process.argv[2]
if (!schemaPath) {
  process.stderr.write('usage: protodef-runner.mjs <protocol.json>\n')
  process.exit(2)
}

const schema = JSON.parse(fs.readFileSync(schemaPath, 'utf8'))

// The compiled protocol for one state and direction. Compiling is the
// expensive step, so each pair is built once and reused.
const compiled = new Map()

function protocolFor (state, direction) {
  const key = `${state};${direction}`
  const existing = compiled.get(key)
  if (existing) return existing

  const compiler = new ProtoDefCompiler()
  compiler.addTypes(minecraftDatatypes)
  compiler.addProtocol(schema, [state, direction])
  nbt.addTypesToCompiler('big', compiler)
  const proto = compiler.compileProtoDefSync()
  compiled.set(key, proto)

  return proto
}

// Buffers reach JSON as {"type":"Buffer","data":[…]}, which is not what the Go
// side sends or expects. Both directions are normalized here so a fixture can
// be written as hex.
function toJSON (value) {
  if (value === null || value === undefined) return value
  if (Buffer.isBuffer(value)) return { __buffer: value.toString('hex') }
  if (Array.isArray(value)) return value.map(toJSON)
  if (typeof value === 'bigint') return { __bigint: value.toString() }
  if (typeof value === 'object') {
    const result = {}
    for (const [key, nested] of Object.entries(value)) result[key] = toJSON(nested)
    return result
  }
  return value
}

function fromJSON (value) {
  if (value === null || value === undefined) return value
  if (Array.isArray(value)) return value.map(fromJSON)
  if (typeof value === 'object') {
    if (typeof value.__buffer === 'string') return Buffer.from(value.__buffer, 'hex')
    if (typeof value.__bigint === 'string') return BigInt(value.__bigint)
    const result = {}
    for (const [key, nested] of Object.entries(value)) result[key] = fromJSON(nested)
    return result
  }
  return value
}

function handle (command) {
  switch (command.op) {
    case 'encode': {
      const proto = protocolFor(command.state, command.direction)
      const buffer = proto.createPacketBuffer('packet', {
        name: command.name,
        params: fromJSON(command.params ?? {})
      })
      return { ok: true, hex: buffer.toString('hex') }
    }
    case 'decode': {
      const proto = protocolFor(command.state, command.direction)
      const buffer = Buffer.from(command.hex, 'hex')
      const parsed = proto.parsePacketBuffer('packet', buffer)
      if (parsed.metadata.size !== buffer.length) {
        return {
          ok: false,
          error: `read ${parsed.metadata.size} of ${buffer.length} bytes`
        }
      }
      return { ok: true, name: parsed.data.name, params: toJSON(parsed.data.params) }
    }
    default:
      return { ok: false, error: `unknown op ${command.op}` }
  }
}

const input = readline.createInterface({ input: process.stdin })

for await (const line of input) {
  const trimmed = line.trim()
  if (trimmed === '') continue

  let result
  try {
    const command = JSON.parse(trimmed)
    if (command.op === 'stop') break
    result = handle(command)
  } catch (error) {
    result = { ok: false, error: String(error && error.message ? error.message : error) }
  }
  process.stdout.write(JSON.stringify(result) + '\n')
}
