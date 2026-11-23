import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bestOffset, expectedPosition, loopError, correction } from './sync.js';

const close = (a, b) => assert.ok(Math.abs(a - b) < 1e-9, `${a} != ${b}`);

test('expectedPosition wraps by duration', () => {
  close(expectedPosition(11000, 0, 10), 1);
  close(expectedPosition(5000, 0, 10), 5);
});

test('expectedPosition before start is 0', () => {
  assert.equal(expectedPosition(900, 1000, 10), 0);
});

test('expectedPosition with unknown duration does not wrap', () => {
  close(expectedPosition(11000, 0, NaN), 11);
});

test('loopError takes the short way around the loop', () => {
  close(loopError(9.9, 0.1, 10), -0.2);
  close(loopError(0.1, 9.9, 10), 0.2);
  close(loopError(5.3, 5.0, 10), 0.3);
});

test('correction thresholds', () => {
  assert.deepEqual(correction(0.02), { action: 'none', rate: 1 });
  assert.deepEqual(correction(0.1), { action: 'rate', rate: 0.95 });
  assert.deepEqual(correction(-0.1), { action: 'rate', rate: 1.05 });
  assert.deepEqual(correction(0.8), { action: 'seek' });
  assert.deepEqual(correction(-0.8), { action: 'seek' });
});

test('bestOffset uses the lowest round-trip sample', () => {
  const samples = [
    { t0: 0, t1: 100, ts: 1050 },  // rtt 100 -> offset 1000
    { t0: 200, t1: 210, ts: 1305 }, // rtt 10  -> offset 1100
    { t0: 300, t1: 380, ts: 1500 }, // rtt 80
  ];
  close(bestOffset(samples), 1100);
});

import { seekTarget, updateSeekLead, isStale } from './sync.js';

test('seekTarget aims ahead by the lead and wraps', () => {
  close(seekTarget(3, 0.5, 10), 3.5);
  close(seekTarget(9.8, 0.5, 10), 0.3);
});

test('updateSeekLead learns from where the seek landed', () => {
  close(updateSeekLead(0.5, -0.4), 0.9);  // landed 0.4 s behind -> aim further ahead
  close(updateSeekLead(0.5, 0.2), 0.3);   // landed ahead -> aim less far
  assert.equal(updateSeekLead(0.1, 0.5), 0);   // clamped at 0
  assert.equal(updateSeekLead(2.8, -1), 3);    // clamped at 3
});

test('isStale detects a silent socket', () => {
  assert.equal(isStale(30000, 10000, 25000), false);
  assert.equal(isStale(36000, 10000, 25000), true);
});
