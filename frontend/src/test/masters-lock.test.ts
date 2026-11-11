import { describe, expect, it } from 'vitest';

import { mastersLock, type MasterStatusRecord } from '@/pages/nodes/MastersCard';

const master = (name: string, extra: Partial<MasterStatusRecord> = {}): MasterStatusRecord => ({
  name,
  priority: 0,
  url: '',
  alive: true,
  leader: false,
  self: false,
  lastSeen: 1,
  pending: false,
  ...extra,
});

// The card must lock exactly when the server would refuse, or buttons either
// fail with a toast or stay greyed out on the primary.
describe('mastersLock', () => {
  const primary = master('a', { self: true, leader: true });

  it('unlocks on the leading primary with every master online', () => {
    expect(mastersLock([primary, master('b')])).toBeNull();
  });

  it('locks while a joined master is offline', () => {
    expect(mastersLock([primary, master('b', { alive: false })])).toEqual({
      reason: 'offline',
      name: 'b',
    });
  });

  it('does not lock for a master that was added but has not joined, even ranked first', () => {
    const pending = master('p', { alive: false, pending: true });
    expect(mastersLock([primary, pending])).toBeNull();
    expect(mastersLock([pending, primary])).toBeNull();
  });

  it('lets the new leader forget a primary that died for good', () => {
    const dead = master('a', { alive: false });
    const b = master('b', { self: true, leader: true });
    expect(mastersLock([dead, b], 'a')).toBeNull();
  });

  it('lets the offline master itself be forgotten, but not while another is down', () => {
    const b = master('b', { alive: false });
    expect(mastersLock([primary, b], 'b')).toBeNull();
    expect(mastersLock([primary, b, master('c', { alive: false })], 'b')).toEqual({
      reason: 'offline',
      name: 'c',
    });
  });

  it('locks on a standby and on a primary that has not taken the lead yet', () => {
    expect(mastersLock([master('a', { leader: true }), master('b', { self: true })])).toEqual({
      reason: 'notPrimary',
      name: 'a',
    });
    expect(mastersLock([master('a', { self: true }), master('b', { leader: true })])).toEqual({
      reason: 'notPrimary',
      name: 'a',
    });
  });
});
