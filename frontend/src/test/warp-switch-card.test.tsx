import { describe, expect, it } from 'vitest';

import { warpRows } from '@/pages/nodes/WarpSwitchCard';

describe('warpRows', () => {
  it('lists every server and treats a never-touched one as off', () => {
    const rows = warpRows(
      [
        { value: 0, label: 'master' },
        { value: 3, label: 'de' },
      ],
      [{ nodeId: 3, mode: 'domains', domains: ['geosite:openai'], lastError: '' }],
    );
    expect(rows.map((r) => [r.name, r.mode, r.domains])).toEqual([
      ['master', 'off', []],
      ['de', 'domains', ['geosite:openai']],
    ]);
  });
});
