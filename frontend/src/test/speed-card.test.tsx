import { describe, expect, it } from 'vitest';

import { speedServerRows } from '@/pages/nodes/SpeedCard';

describe('speedServerRows', () => {
  it('lists every server and shows load only where a link speed is set', () => {
    const rows = speedServerRows(
      [
        { value: 0, label: 'master' },
        { value: 3, label: 'de' },
      ],
      {
        clients: null,
        servers: [{ nodeId: 3, capacityMbps: 1000 }],
        load: [{ nodeId: 3, capacityMbps: 1000, usedMbps: 400, online: 5, freePerUserMbps: 120 }],
      },
    );
    expect(rows.map((r) => [r.name, r.capacityMbps, r.load?.freePerUserMbps])).toEqual([
      ['master', 0, undefined],
      ['de', 1000, 120],
    ]);
  });
});

describe('speedServerRows speed tests', () => {
  it('attaches each server its own test statistics and running flag', () => {
    const stats = {
      nodeId: 3,
      count: 2,
      avgDownloadMbps: 750,
      avgUploadMbps: 650,
      minDownloadMbps: 600,
      maxDownloadMbps: 900,
    };
    const rows = speedServerRows(
      [
        { value: 0, label: 'master' },
        { value: 3, label: 'de' },
      ],
      { clients: null, servers: null, load: null, testing: [0], testStats: [stats] },
    );
    expect(rows.map((r) => [r.name, r.testing, r.test?.avgDownloadMbps])).toEqual([
      ['master', true, undefined],
      ['de', false, 750],
    ]);
  });
});
