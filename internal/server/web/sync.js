// Pure timing math shared by app.js and the tests.

// Clock offset (server - local, ms) from the sample with the lowest round trip.
export function bestOffset(samples) {
  let best = null;
  for (const s of samples) {
    const rtt = s.t1 - s.t0;
    if (best === null || rtt < best.rtt) best = { rtt, offset: s.ts - (s.t0 + s.t1) / 2 };
  }
  return best ? best.offset : 0;
}

// Where playback should be (seconds) given the server clock and start time.
export function expectedPosition(serverNowMs, startAtMs, durationS) {
  const t = (serverNowMs - startAtMs) / 1000;
  if (t <= 0) return 0;
  if (!(durationS > 0) || !isFinite(durationS)) return t;
  return t % durationS;
}

// Signed error (actual - expected), taking the short way around the loop.
export function loopError(actualS, expectedS, durationS) {
  let e = actualS - expectedS;
  if (durationS > 0 && isFinite(durationS)) {
    e = ((e % durationS) + durationS) % durationS;
    if (e > durationS / 2) e -= durationS;
  }
  return e;
}

// What to do about an error: nothing, nudge the speed, or jump.
export function correction(errS) {
  const a = Math.abs(errS);
  if (a < 0.04) return { action: 'none', rate: 1 };
  if (a < 0.5) return { action: 'rate', rate: errS > 0 ? 0.95 : 1.05 };
  return { action: 'seek' };
}

// Where to seek so the video is in place once the seek and rebuffer finish.
export function seekTarget(expectedS, leadS, durationS) {
  const t = expectedS + leadS;
  return durationS > 0 && isFinite(durationS) ? t % durationS : t;
}

// Learn the seek lead from the error measured right after a seek landed
// (negative = landed behind). Clamped to 0..3 s.
export function updateSeekLead(leadS, errAfterS) {
  return Math.min(3, Math.max(0, leadS - errAfterS));
}

// True when no pong has arrived for longer than timeoutMs.
export function isStale(nowMs, lastPongMs, timeoutMs) {
  return nowMs - lastPongMs > timeoutMs;
}

// Wrap an unwrapped position (seconds) into the video's length.
export function wrap(posS, durationS) {
  if (!(posS > 0)) return 0;
  return durationS > 0 && isFinite(durationS) ? posS % durationS : posS;
}

// Where a phone should hold still before playback (re)starts, or while paused.
export function holdPosition(state, durationS) {
  if (state.paused) return wrap(state.pausePos || 0, durationS);
  const playAt = state.playAt || state.startAt;
  return expectedPosition(playAt, state.startAt, durationS);
}
