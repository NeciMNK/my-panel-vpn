import { describe, expect, it } from 'vitest';

import { buildRowActionsMenu } from '@/pages/inbounds/list/RowActions';
import type { DBInboundRecord } from '@/pages/inbounds/list/types';

function record(network: string): DBInboundRecord {
  return {
    id: 1,
    enable: true,
    remark: 'r',
    subSortIndex: 1,
    port: 443,
    protocol: 'vless',
    up: 0,
    down: 0,
    total: 0,
    expiryTime: 0,
    _expiryTime: null,
    isVLess: true,
    settings: { clients: [] },
    streamSettings: JSON.stringify({ network, security: 'reality' }),
  };
}

function keys(network: string): string[] {
  const items = buildRowActionsMenu({ record: record(network), subEnable: false, t: (k) => k });
  return (items ?? []).map((i) => String(i?.key ?? ''));
}

// gRPC is deprecated in Xray 26.10: only gRPC inbounds offer the XHTTP copy.
describe('inbound row actions: copy as XHTTP', () => {
  it('offers copyAsXhttp on a gRPC inbound', () => {
    expect(keys('grpc')).toContain('copyAsXhttp');
  });
  it('hides copyAsXhttp on other transports', () => {
    expect(keys('xhttp')).not.toContain('copyAsXhttp');
    expect(keys('tcp')).not.toContain('copyAsXhttp');
  });
});
