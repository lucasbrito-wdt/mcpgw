import { describe, expect, it } from 'vitest';
import { applyCeilingChange, type CeilingState } from './CeilingMatrix';
import { nextPollDelay } from './LiveToggle';

describe('applyCeilingChange', () => {
  const base: CeilingState = { 'u1|github': 'read' };
  it('sets a level', () => expect(applyCeilingChange(base, 'u1', 'github', 'write')).toEqual({ 'u1|github': 'write' }));
  it('none removes the cell', () => expect(applyCeilingChange(base, 'u1', 'github', 'none')).toEqual({}));
  it('does not mutate the input', () => { applyCeilingChange(base, 'u1', 'github', 'none'); expect(base).toEqual({ 'u1|github': 'read' }); });
});
describe('nextPollDelay', () => {
  it('polls every 5 s and backs off after 3 errors', () => {
    expect(nextPollDelay({ errors: 0, visible: true })).toBe(5000);
    expect(nextPollDelay({ errors: 3, visible: true })).toBe(30000);
    expect(nextPollDelay({ errors: 0, visible: false })).toBeNull();
  });
});
