import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sentText } from './text.ts';

test('Send status names the session the answers went to', () => {
  assert.equal(sentText(3, 'pi: shop'), 'Sent 3 answers to pi: shop.');
  assert.equal(sentText(1, 'pi: shop'), 'Sent 1 answer to pi: shop.');
});

test('Send status without a present owner', () => {
  assert.equal(
    sentText(2, ''),
    'Sent 2 answers. The next agent that opens cull in this project gets them.',
  );
  assert.equal(
    sentText(1, undefined),
    'Sent 1 answer. The next agent that opens cull in this project gets them.',
  );
});

test('nothing to send', () => {
  assert.equal(sentText(0, 'pi: shop'), 'Nothing to send.');
});
