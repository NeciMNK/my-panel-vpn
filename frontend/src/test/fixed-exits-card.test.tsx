import { describe, expect, it } from 'vitest';

import { fixedExitDown } from '@/pages/nodes/FixedExitsCard';

describe('fixedExitDown', () => {
  const nodes = [
    { id: 2, enable: true, status: 'online' },
    { id: 3, enable: true, status: 'offline' },
    { id: 4, enable: false, status: 'online' },
  ];

  it('flags a pinned exit the sticky pass skips', () => {
    expect([2, 3, 4, 9].map((id) => fixedExitDown(id, nodes))).toEqual([false, true, true, true]);
  });

  it('never flags this panel, which has no node row', () => {
    expect(fixedExitDown(0, [])).toBe(false);
  });
});
