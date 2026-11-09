import { describe, expect, it } from 'vitest';

import { emptyCascadeForm, exitOptions } from '@/pages/nodes/CascadesCard';

describe('cascade form helpers', () => {
  it('starts on this panel as the entry with the ping strategy', () => {
    expect(emptyCascadeForm()).toMatchObject({
      entryNodeId: 0,
      exitNodeIds: [],
      strategy: 'leastPing',
    });
  });

  it('never offers the entry server as an exit', () => {
    const servers = [
      { value: 0, label: 'master' },
      { value: 2, label: 'de' },
      { value: 3, label: 'nl' },
    ];
    expect(exitOptions(servers, 2).map((s) => s.value)).toEqual([0, 3]);
  });
});
