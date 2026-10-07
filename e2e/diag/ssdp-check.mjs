// Diagnostic for the cast suite's C1: does an M-SEARCH sent from this machine
// actually reach e2e/fakerenderer.exe, and does its reply come back?
//
// It starts the fake renderer itself, fires M-SEARCH from several outgoing
// interfaces, then asks the real server to refresh its device list. After each
// step it reads the renderer's own event log, so we can tell "the datagram never
// arrived" apart from "the reply never came back".
//
//   node e2e/diag/ssdp-check.mjs [server-base-url]
import { spawn } from 'node:child_process'
import dgram from 'node:dgram'
import os from 'node:os'
import { fileURLToPath } from 'node:url'

const serverBase = process.argv[2] || 'http://127.0.0.1:5100'
const fakeBin = fileURLToPath(new URL('../fakerenderer.exe', import.meta.url))
const fakeBase = 'http://127.0.0.1:7920'
const GROUP = '239.255.255.250'
const PORT = 1900
const ST = 'urn:schemas-upnp-org:device:MediaRenderer:1'

function localIPv4() {
  const out = []
  for (const [name, entries] of Object.entries(os.networkInterfaces())) {
    for (const entry of entries || []) {
      if ((entry.family === 'IPv4' || entry.family === 4)) out.push({ name, addr: entry.address, internal: entry.internal })
    }
  }
  return out
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function fakeState() {
  const response = await fetch(fakeBase + '/state')
  return response.json()
}

// sendSearch fires one M-SEARCH, optionally pinning the outgoing interface, and
// collects whatever unicast replies arrive back on the same socket.
function sendSearch(interfaceAddr) {
  return new Promise((resolve) => {
    const socket = dgram.createSocket({ type: 'udp4', reuseAddr: true })
    const replies = []
    const finish = (error) => {
      try {
        socket.close()
      } catch {
        /* already closed */
      }
      resolve({ interfaceAddr, error, replies })
    }
    const message = Buffer.from(
      'M-SEARCH * HTTP/1.1\r\n' +
        'HOST: ' + GROUP + ':' + PORT + '\r\n' +
        'MAN: "ssdp:discover"\r\n' +
        'MX: 1\r\n' +
        'ST: ' + ST + '\r\n\r\n',
    )
    socket.on('message', (payload, rinfo) => {
      replies.push({ from: rinfo.address + ':' + rinfo.port, head: payload.toString().split('\r\n').slice(0, 8).join(' | ') })
    })
    socket.on('error', (error) => finish(error.message))
    socket.bind({ address: '0.0.0.0', port: 0 }, () => {
      try {
        if (interfaceAddr) socket.setMulticastInterface(interfaceAddr)
        socket.setMulticastTTL(2)
      } catch (error) {
        finish(`setsockopt: ${error.message}`)
        return
      }
      socket.send(message, PORT, GROUP, (error) => {
        if (error) {
          finish(`send: ${error.message}`)
          return
        }
        setTimeout(() => finish(null), 2500)
      })
    })
  })
}

const fake = spawn(fakeBin, ['-http', '0.0.0.0:7920', '-advertise', '127.0.0.1'], { stdio: ['ignore', 'pipe', 'pipe'] })
fake.stdout.on('data', (chunk) => process.stdout.write('[fake] ' + chunk))
fake.stderr.on('data', (chunk) => process.stderr.write('[fake] ' + chunk))

try {
  const deadline = Date.now() + 8000
  let ready = false
  while (Date.now() < deadline) {
    try {
      const response = await fetch(fakeBase + '/state')
      if (response.ok) {
        ready = true
        break
      }
    } catch {
      /* not up yet */
    }
    await sleep(300)
  }
  if (!ready) throw new Error('fake renderer never answered on ' + fakeBase)

  const interfaces = localIPv4()
  console.log('\n== local IPv4 interfaces ==')
  for (const entry of interfaces) console.log('  ', entry.internal ? 'loopback' : 'lan', entry.name, entry.addr)

  console.log('\n== phase 1: M-SEARCH sent by this script ==')
  const candidates = [null, ...interfaces.map((entry) => entry.addr)]
  for (const candidate of candidates) {
    const before = (await fakeState()).events.filter((event) => event.action === 'SSDP').length
    const result = await sendSearch(candidate)
    const after = (await fakeState()).events.filter((event) => event.action === 'SSDP').length
    console.log(
      `   iface=${candidate === null ? 'default' : candidate}  sent=${result.error ? 'ERR(' + result.error + ')' : 'ok'}` +
        `  rendererSawSearch=${after > before}  repliesBack=${result.replies.length}`,
    )
    for (const reply of result.replies) console.log('        reply from', reply.from, '->', reply.head)
  }

  console.log('\n== phase 2: the real server refresh (/api/dlna/devices?refresh=1) ==')
  const before = (await fakeState()).events.filter((event) => event.action === 'SSDP').length
  let devices = null
  try {
    const response = await fetch(serverBase + '/api/dlna/devices?refresh=1')
    devices = await response.json()
  } catch (error) {
    console.log('   server call failed:', error.message)
  }
  const after = (await fakeState()).events.filter((event) => event.action === 'SSDP').length
  console.log('   rendererSawSearch=' + (after > before))
  console.log('   devices=' + JSON.stringify(devices && devices.devices ? devices.devices.map((device) => device.udn) : devices))

  console.log('\n== fake renderer event log ==')
  const final = await fakeState()
  for (const event of final.events) console.log('   ', event.action, event.uri || '', event.detail || '')
} finally {
  fake.kill()
  await sleep(300)
}
