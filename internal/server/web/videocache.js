// Keeps whole video files on the phone (IndexedDB), so a looping video only
// crosses the Wi-Fi once. The page is served over plain http on the LAN, so
// Service Workers and the Cache API are unavailable; IndexedDB still works.
//
// Files are written in chunks so a multi-GB video never sits in RAM: each
// chunk is a Blob stored on disk by the browser, and the playable file is a
// Blob stitched from those on-disk chunks.

const DB_NAME = 'vrctrl-videos';
const CHUNK = 16 * 1024 * 1024;

let dbPromise = null;
function db() {
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, 1);
      req.onupgradeneeded = () => {
        const d = req.result;
        d.createObjectStore('videos', { keyPath: 'id' }); // {id, size, mtime, type, chunks, lastUsed}
        d.createObjectStore('chunks');                    // `${id}:${n}` -> Blob
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    }).catch((e) => { dbPromise = null; throw e; });
  }
  return dbPromise;
}

function tx(stores, mode, fn) {
  return db().then((d) => new Promise((resolve, reject) => {
    const t = d.transaction(stores, mode);
    let out;
    t.oncomplete = () => resolve(out);
    t.onerror = () => reject(t.error);
    t.onabort = () => reject(t.error || new Error('aborted'));
    out = fn(t);
  }));
}

const reqValue = (req) => new Promise((resolve, reject) => {
  req.onsuccess = () => resolve(req.result);
  req.onerror = () => reject(req.error);
});

async function getMeta(id) {
  let r;
  await tx(['videos'], 'readonly', (t) => { r = reqValue(t.objectStore('videos').get(id)); });
  return r;
}

async function allMeta() {
  let r;
  await tx(['videos'], 'readonly', (t) => { r = reqValue(t.objectStore('videos').getAll()); });
  return r;
}

async function remove(id) {
  const m = await getMeta(id).catch(() => null);
  const n = m ? m.chunks : 0;
  await tx(['videos', 'chunks'], 'readwrite', (t) => {
    t.objectStore('videos').delete(id);
    const cs = t.objectStore('chunks');
    // Partial downloads have no meta, so also sweep any chunk keys for the id.
    cs.delete(IDBKeyRange.bound(`${id}:`, `${id}:￿`));
    for (let i = 0; i < n; i++) cs.delete(`${id}:${i}`);
  });
}

// Make room by dropping the least recently used other videos.
async function evictOne(keepId) {
  const all = (await allMeta()).filter((m) => m.id !== keepId);
  if (!all.length) return false;
  all.sort((a, b) => (a.lastUsed || 0) - (b.lastUsed || 0));
  await remove(all[0].id);
  return true;
}

async function putChunk(id, n, blob) {
  for (;;) {
    try {
      await tx(['chunks'], 'readwrite', (t) => { t.objectStore('chunks').put(blob, `${id}:${n}`); });
      return;
    } catch (e) {
      if (!(e && e.name === 'QuotaExceededError') || !(await evictOne(id))) throw e;
    }
  }
}

// The cached file as one Blob, or null.
async function assemble(id) {
  const m = await getMeta(id);
  if (!m) return null;
  const parts = [];
  await tx(['chunks'], 'readonly', (t) => {
    const cs = t.objectStore('chunks');
    for (let i = 0; i < m.chunks; i++) parts.push(reqValue(cs.get(`${id}:${i}`)));
  });
  const blobs = await Promise.all(parts);
  if (blobs.some((b) => !b)) { await remove(id).catch(() => {}); return null; }
  const blob = new Blob(blobs, { type: m.type });
  if (blob.size !== m.size) { await remove(id).catch(() => {}); return null; }
  tx(['videos'], 'readwrite', (t) => { t.objectStore('videos').put({ ...m, lastUsed: Date.now() }); }).catch(() => {});
  return { blob, meta: m };
}

// Does the server still have the same file? (IDs are by path, so the file
// could have been replaced.) If the laptop can't be reached, trust the cache.
async function stillValid(url, meta) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 3000);
  try {
    const r = await fetch(url, { method: 'HEAD', cache: 'no-store', signal: ctl.signal });
    if (!r.ok) return r.status !== 404;
    const size = Number(r.headers.get('Content-Length'));
    const mtime = r.headers.get('Last-Modified') || '';
    return size === meta.size && mtime === meta.mtime;
  } catch {
    return true;
  } finally {
    clearTimeout(timer);
  }
}

/** Returns a cached Blob for the video, or null if it isn't (validly) cached. */
export async function lookup(id, url) {
  try {
    const got = await assemble(id);
    if (!got) return null;
    if (!(await stillValid(url, got.meta))) { await remove(id).catch(() => {}); return null; }
    return got.blob;
  } catch {
    return null;
  }
}

const inflight = new Map(); // id -> {progress, promise}

/** Fraction of the video downloaded so far (0..1), or 0 if not downloading. */
export function progress(id) {
  const d = inflight.get(id);
  return d ? d.progress : 0;
}

/**
 * Downloads the whole video into the cache (once; repeat calls share the
 * download). Resolves with the cached Blob, or null if it couldn't be cached.
 */
export function download(id, url) {
  if (inflight.has(id)) return inflight.get(id).promise;
  const state = { progress: 0, promise: null };
  state.promise = (async () => {
    try {
      if (!window.indexedDB || !window.ReadableStream) return null;
      try { navigator.storage && navigator.storage.persist && navigator.storage.persist(); } catch {}
      await remove(id).catch(() => {});
      const r = await fetch(url, { cache: 'no-store', priority: 'low' });
      if (!r.ok || !r.body) return null;
      const size = Number(r.headers.get('Content-Length'));
      const mtime = r.headers.get('Last-Modified') || '';
      const type = (r.headers.get('Content-Type') || 'video/mp4').split(';')[0];
      if (!(size > 0)) return null;

      const reader = r.body.getReader();
      let buf = [], bufLen = 0, total = 0, n = 0;
      const flush = async () => {
        if (!bufLen) return;
        const blob = new Blob(buf, { type });
        buf = []; bufLen = 0;
        await putChunk(id, n++, blob);
      };
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf.push(value); bufLen += value.byteLength; total += value.byteLength;
        state.progress = Math.min(0.99, total / size);
        if (bufLen >= CHUNK) await flush();
      }
      await flush();
      if (total !== size) { await remove(id).catch(() => {}); return null; }
      await tx(['videos'], 'readwrite', (t) => {
        t.objectStore('videos').put({ id, size, mtime, type, chunks: n, lastUsed: Date.now() });
      });
      const got = await assemble(id);
      return got ? got.blob : null;
    } catch {
      await remove(id).catch(() => {});
      return null;
    } finally {
      inflight.delete(id);
    }
  })();
  inflight.set(id, state);
  return state.promise;
}
