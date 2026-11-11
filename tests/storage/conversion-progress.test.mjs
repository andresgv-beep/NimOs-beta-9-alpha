import test from 'node:test';
import assert from 'node:assert/strict';
import { conversionResult } from '../../src/lib/apps/storage/conversion-progress.js';

const operation = status => ({ id: 'conversion-1', type: 'convert_profile', status });
test('does not announce success before the exact conversion completes', () => {
  for (const ops of [undefined, [], [operation('pending')], [operation('in_progress')],
    [{ ...operation('completed'), id: 'other-operation' }],
    [{ ...operation('completed'), type: 'start_scrub' }]]) {
    assert.equal(conversionResult(ops, 'conversion-1').phase, 'converting');
  }
});
test('accepts confirmed completion even when a later journal entry exists', () => {
  assert.equal(conversionResult([{ ...operation('failed'), id: 'other' }, operation('completed')], 'conversion-1').phase, 'done');
});
test('reports failure, cancellation and rollback without success', () => {
  for (const status of ['failed', 'cancelled', 'rolled_back']) {
    const result = conversionResult({ operations: [{ ...operation(status), error: 'Recovery required' }] }, 'conversion-1');
    assert.deepEqual(result, { phase: 'error', error: 'Recovery required' });
  }
});
