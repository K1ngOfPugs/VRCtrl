import { bestOffset, expectedPosition, loopError, correction, seekTarget, updateSeekLead, isStale } from './sync.js';

const video = document.getElementById('v');
const statusEl = document.getElementById('status');
video.muted = true;          // required for autoplay on iOS
video.playsInline = true;

let ws = null;
let backoff = 500;
let reconnectTimer = null;
let samples = [];
let offset = 0;              // server clock - local clock, ms
let current = null;          // last State from the server
let loadToken = 0;
let started = false;         // current video has been started
let lastSeek = 0;
let wakeLock = null;
let lastPong = 0;            // local ms of last pong (liveness)
let seekLead = 0.3;          // learned: how far ahead to seek to absorb seek/rebuffer time (s)
let measureSeek = false;     // next settled tick measures where the last seek landed
const STALE_MS = 25000;      // no pong for this long = dead socket

const serverNow = () => Date.now() + offset;

function setStatus(text, tap = false) {
  if (!text) { statusEl.className = 'hidden'; return; }
  statusEl.textContent = text;
  statusEl.className = tap ? 'tap' : '';
}

// ---- connection -----------------------------------------------------------

function connect() {
  clearTimeout(reconnectTimer);
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const sock = new WebSocket(`${proto}://${location.host}/ws`);
  ws = sock;
  sock.onopen = () => { backoff = 500; lastPong = Date.now(); pingBurst(); };
  sock.onmessage = (ev) => {
    let m;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (m.type === 'pong') {
      lastPong = Date.now();
      samples.push({ t0: m.t0, t1: Date.now(), ts: m.ts });
      if (samples.length > 10) samples.shift();
      offset = bestOffset(samples);
    } else if (m.type === 'state') {
      apply(m.state);
    }
  };
  sock.onclose = () => {
    if (ws !== sock) return; // an abandoned socket closing late
    if (!started) setStatus('Connecting…');
    clearTimeout(reconnectTimer);
    reconnectTimer = setTimeout(connect, backoff * (0.75 + Math.random() * 0.5));
    backoff = Math.min(backoff * 2, 5000);
  };
  sock.onerror = () => sock.close();
}

// Drop a socket that looks open but has gone silent (Wi-Fi roam, laptop
// sleep) and reconnect straight away.
function checkAlive() {
  if (ws && ws.readyState === WebSocket.OPEN && isStale(Date.now(), lastPong, STALE_MS)) {
    const dead = ws;
    ws = null;
    try { dead.close(); } catch {}
    connect();
  }
}

function pingBurst() {
  for (let i = 0; i < 5; i++) {
    setTimeout(() => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'ping', t0: Date.now() }));
      }
    }, i * 100);
  }
}
setInterval(() => { checkAlive(); pingBurst(); }, 10000);

// ---- playback -------------------------------------------------------------

function apply(state) {
  const prev = current;
  current = state;
  if (state.stopped) {
    loadToken++;
    started = false;
    video.pause();
    video.removeAttribute('src');
    video.load();
    setStatus('Waiting for operator');
    return;
  }
  const same = prev && !prev.stopped && prev.videoId === state.videoId && prev.startAt === state.startAt;
  if (same && video.getAttribute('src')) return; // reconnect with unchanged state: tick() resyncs
  load(state);
}

function load(state) {
  const token = ++loadToken;
  started = false;
  video.pause();
  video.playbackRate = 1;
  if (video.getAttribute('src') !== state.url) {
    video.src = state.url;
    video.load();
  }
  if (!started) setStatus('');  // keep the headset black while buffering

  const whenReady = () => {
    if (token !== loadToken) return;
    if (video.readyState >= 1) schedule(token); else video.addEventListener('loadedmetadata', () => schedule(token), { once: true });
  };
  whenReady();
}

function schedule(token) {
  if (token !== loadToken || !current || current.stopped) return;
  const wait = current.startAt - serverNow();
  if (wait > 30) {
    if (!video.paused) video.pause();
    if (video.currentTime !== 0) { try { video.currentTime = 0; } catch {} }
    setTimeout(() => schedule(token), Math.max(0, wait - 25));
    return;
  }
  // Fine-grained wait for the last few ms.
  if (wait > 0) { requestAnimationFrame(() => schedule(token)); return; }
  start(token);
}

function start(token) {
  if (token !== loadToken) return;
  const pos = expectedPosition(serverNow(), current.startAt, video.duration);
  const off = Math.abs(video.currentTime - pos);
  if (off > 0.5) seekAhead();                       // late join: allow for seek time
  else if (off > 0.05) { try { video.currentTime = pos; } catch {} }
  started = true;
  play();
}

// Seek ahead of the expected spot by the learned lead, so the phone is in
// place once iOS finishes seeking and rebuffering.
function seekAhead() {
  lastSeek = performance.now();
  video.playbackRate = 1;
  try { video.currentTime = seekTarget(expectedPosition(serverNow(), current.startAt, video.duration), seekLead, video.duration); } catch {}
  measureSeek = true;
}

function play() {
  const token = loadToken;
  const p = video.play();
  if (p && p.catch) {
    p.then(() => { if (token === loadToken && started) { setStatus(''); requestWakeLock(); } })
     .catch((err) => {
       // AbortError = superseded by a pause/switch/stop; not a real refusal.
       if ((err && err.name === 'AbortError') || token !== loadToken || !started) return;
       setStatus('Tap to start', true); // e.g. Low Power Mode blocks autoplay
     });
  }
}

statusEl.addEventListener('click', () => { if (started) play(); });

// Drift correction.
setInterval(() => {
  if (!current || current.stopped || !started) return;
  if (serverNow() < current.startAt) return;
  if (video.paused) { play(); return; }
  if (video.readyState < 2 || video.seeking) return;
  const d = video.duration;
  const exp = expectedPosition(serverNow(), current.startAt, d);
  const err = loopError(video.currentTime, exp, d);
  if (measureSeek) {
    if (video.readyState < 3) return;  // still rebuffering after the seek
    measureSeek = false;
    seekLead = updateSeekLead(seekLead, err);
  }
  const c = correction(err);
  if (c.action === 'seek') {
    if (performance.now() - lastSeek < 1500) return;
    seekAhead();
  } else if (video.playbackRate !== c.rate) {
    video.playbackRate = c.rate;
  }
}, 500);

// ---- screen / lifecycle ---------------------------------------------------

async function requestWakeLock() {
  if (!('wakeLock' in navigator) || wakeLock) return;
  try {
    wakeLock = await navigator.wakeLock.request('screen');
    wakeLock.addEventListener('release', () => { wakeLock = null; });
  } catch { wakeLock = null; }
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  checkAlive();
  connect();
  pingBurst();
  if (current && !current.stopped && started) play();
  requestWakeLock();
});
window.addEventListener('pageshow', connect);

connect();
