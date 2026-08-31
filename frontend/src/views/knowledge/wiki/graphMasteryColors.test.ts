import test from 'node:test';
import assert from 'node:assert/strict';
import { masteryRing, nodeFill, newlyLitSlugs, tierColor } from './graphMasteryColors.ts';

test('masteryRing maps the four tiers onto the green brand ramp', () => {
  // WeKnora green brand ramp (brand-4 #07c05f main, brand-7 #038626 deep);
  // unseen keeps the spec's neutral gray. The ring is the ONLY mastery channel.
  assert.deepEqual(masteryRing('unseen'), { visible: true, stroke: '#d0d0d0', width: 2, dashed: false, glow: false });
  assert.deepEqual(masteryRing('touched'), { visible: true, stroke: '#8ce0af', width: 2, dashed: false, glow: false });
  assert.deepEqual(masteryRing('familiar'), { visible: true, stroke: '#07c05f', width: 2, dashed: false, glow: false });
  assert.deepEqual(masteryRing('mastered'), { visible: true, stroke: '#038626', width: 3.5, dashed: false, glow: true });
  assert.deepEqual(masteryRing(''), { visible: false, stroke: '', width: 0, dashed: false, glow: false });
  assert.deepEqual(masteryRing(undefined), { visible: false, stroke: '', width: 0, dashed: false, glow: false });
  assert.deepEqual(masteryRing('bogus' as any), { visible: false, stroke: '', width: 0, dashed: false, glow: false });
});

test('masteryRing dashes low-confidence tiers but keeps mastered emphasis', () => {
  assert.equal(masteryRing('touched', true).dashed, true);
  assert.equal(masteryRing('touched', false).dashed, false);
  const mastered = masteryRing('mastered', true);
  assert.equal(mastered.glow, true);
  assert.equal(mastered.dashed, true);
});

test('nodeFill always returns the type color — the fill channel belongs to page type', () => {
  // The old behavior (tier overriding the fill) ate the type information
  // and collided with the entity type green; mastery lives on the ring now.
  assert.equal(nodeFill('concept', '#e37318', 'familiar'), '#e37318');
  assert.equal(nodeFill('concept', '#e37318', 'mastered'), '#e37318');
  assert.equal(nodeFill('entity', '#2ba471', 'familiar'), '#2ba471');
  assert.equal(nodeFill('concept', '#e37318', undefined), '#e37318');
  assert.equal(nodeFill('summary', '#0052d9', ''), '#0052d9');
});

test('tierColor exposes the raw ramp for chips, legends and cards', () => {
  assert.equal(tierColor('mastered'), '#038626');
  assert.equal(tierColor(''), '');
  assert.equal(tierColor(undefined), '');
});

test('newlyLitSlugs detects rises into lit territory only', () => {
  const prev: Record<string, any> = { rag: 'unseen', decay: 'familiar', gone: 'touched' };
  const curr: Record<string, any> = { rag: 'touched', decay: 'familiar', fresh: 'mastered' };
  assert.deepEqual(newlyLitSlugs(prev, curr), ['fresh', 'rag']);
  // decay stayed familiar: not new. gone disappeared: not new.
});
