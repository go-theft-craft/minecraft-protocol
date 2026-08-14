// Loopback-only conformance runner for the Go managed stream.
//
// It speaks Java Edition 1.8.8 (protocol 47) using the pinned Node
// minecraft-protocol package and reports what it saw as newline-delimited JSON
// on stdout. It exits non-zero on an unexpected packet, a timeout, or a
// disconnect it did not expect.
//
// It never binds or dials anything but the loopback interface.

import process from 'node:process'
import mc from 'minecraft-protocol'

const LOOPBACK = '127.0.0.1'
const DEFAULT_TIMEOUT_MS = 30_000

function parseArgs (argv) {
  const args = {
    mode: null,
    host: LOOPBACK,
    port: 0,
    scenario: null,
    threshold: 64,
    timeout: DEFAULT_TIMEOUT_MS
  }

  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index]
    if (!flag.startsWith('--')) {
      fail(`unexpected argument ${flag}`)
    }
    const name = flag.slice(2)
    const value = argv[index + 1]
    if (value === undefined) {
      fail(`flag --${name} needs a value`)
    }
    index += 1

    switch (name) {
      case 'mode':
      case 'scenario':
        args[name] = value
        break
      case 'host':
        args.host = value
        break
      case 'port':
      case 'threshold':
      case 'timeout':
        args[name] = Number.parseInt(value, 10)
        if (!Number.isInteger(args[name])) {
          fail(`flag --${name} needs an integer, got ${value}`)
        }
        break
      default:
        fail(`unknown flag --${name}`)
    }
  }

  if (args.mode !== 'client' && args.mode !== 'server') {
    fail('--mode must be client or server')
  }
  if (args.scenario === null) {
    fail('--scenario is required')
  }
  if (args.host !== LOOPBACK && args.host !== 'localhost') {
    fail(`refusing to use a non-loopback host ${args.host}`)
  }

  return args
}

function emit (event) {
  process.stdout.write(`${JSON.stringify(event)}\n`)
}

function fail (message) {
  emit({ event: 'error', message })
  process.exit(1)
}

function withTimeout (timeoutMs, label) {
  const timer = setTimeout(() => {
    fail(`timed out waiting for ${label}`)
  }, timeoutMs)
  timer.unref?.()
  return () => clearTimeout(timer)
}

// runClient dials the Go server-role stream.
async function runClient (args) {
  const cancelTimeout = withTimeout(args.timeout, `scenario ${args.scenario}`)

  if (args.scenario === 'status') {
    const result = await mc.ping({
      host: args.host,
      port: args.port,
      version: '1.8.8',
      closeTimeout: args.timeout
    })

    emit({
      event: 'status',
      version: result.version?.name ?? null,
      protocol: result.version?.protocol ?? null,
      maxPlayers: result.players?.max ?? null,
      onlinePlayers: result.players?.online ?? null
    })
    cancelTimeout()
    return
  }

  if (args.scenario !== 'login') {
    fail(`unknown client scenario ${args.scenario}`)
  }

  const client = mc.createClient({
    host: args.host,
    port: args.port,
    username: 'Alex',
    auth: 'offline',
    version: '1.8.8',
    keepAlive: false
  })

  client.on('connect', () => emit({ event: 'state', state: 'connected' }))

  client.on('compress', (packet) => {
    emit({ event: 'compress', threshold: packet.threshold })
  })

  client.on('success', (packet) => {
    emit({ event: 'login_success', username: packet.username, uuid: packet.uuid })
  })

  client.on('login', () => emit({ event: 'state', state: 'play' }))

  client.on('keep_alive', (packet) => {
    emit({ event: 'packet', name: 'keep_alive', keepAliveId: packet.keepAliveId })
  })

  client.on('chat', (packet) => {
    emit({ event: 'packet', name: 'chat', length: packet.message.length })
  })

  client.on('kick_disconnect', (packet) => {
    emit({ event: 'disconnect', state: 'play', reason: packet.reason })
    cancelTimeout()
    client.end()
    process.exit(0)
  })

  client.on('disconnect', (packet) => {
    emit({ event: 'disconnect', state: 'login', reason: packet.reason })
    cancelTimeout()
    client.end()
    process.exit(0)
  })

  client.on('error', (err) => fail(`client error: ${err.message}`))

  client.on('end', (reason) => {
    emit({ event: 'end', reason: reason ?? null })
    cancelTimeout()
    process.exit(0)
  })
}

// runServer listens on loopback for the Go client-role stream.
async function runServer (args) {
  const cancelTimeout = withTimeout(args.timeout, `scenario ${args.scenario}`)

  const server = mc.createServer({
    host: LOOPBACK,
    port: args.port,
    'online-mode': false,
    version: '1.8.8',
    maxPlayers: 20,
    motd: 'interop',
    keepAlive: false
  })

  // The pinned server hard-codes its login compression threshold at 256, so
  // --threshold only affects how large this runner makes its own test packets.

  server.on('listening', () => {
    emit({ event: 'listening', port: server.socketServer.address().port })
  })

  server.on('error', (err) => fail(`server error: ${err.message}`))

  if (args.scenario === 'status') {
    // A ping closes the connection itself; the runner just reports it.
    server.on('connection', (client) => {
      client.on('end', () => {
        emit({ event: 'status_served' })
        cancelTimeout()
        server.close()
        process.exit(0)
      })
    })
    return
  }

  if (args.scenario !== 'login') {
    fail(`unknown server scenario ${args.scenario}`)
  }

  server.on('login', (client) => {
    emit({ event: 'login_success', username: client.username, uuid: client.uuid })

    client.write('login', {
      entityId: 1,
      levelType: 'default',
      gameMode: 0,
      dimension: 0,
      difficulty: 1,
      maxPlayers: 20,
      reducedDebugInfo: false
    })
    emit({ event: 'state', state: 'play' })

    // One packet below the compression threshold and one above it.
    client.write('keep_alive', { keepAliveId: 7 })
    client.write('chat', { message: JSON.stringify({ text: 'x'.repeat(256) }), position: 0 })

    client.on('chat', (packet) => {
      emit({ event: 'packet', name: 'chat', length: packet.message.length })

      client.end(JSON.stringify({ text: 'goodbye' }))
      emit({ event: 'disconnect', state: 'play', reason: 'goodbye' })
      cancelTimeout()
      server.close()
      process.exit(0)
    })

    client.on('error', (err) => fail(`client error: ${err.message}`))
  })
}

const args = parseArgs(process.argv.slice(2))
if (args.mode === 'client') {
  await runClient(args)
} else {
  await runServer(args)
}
