import { afterEach, describe, expect, it } from 'vitest';

import { getXrayTargetNode, setXrayTargetNode, xrayApi } from '@/api/xrayTarget';

describe('xrayApi', () => {
  afterEach(() => setXrayTargetNode(null));

  it('targets the local Xray API by default', () => {
    expect(getXrayTargetNode()).toBeNull();
    expect(xrayApi('/update')).toBe('/panel/api/xray/update');
    expect(xrayApi('/')).toBe('/panel/api/xray/');
  });

  it('routes through the node proxy while a node is selected', () => {
    setXrayTargetNode(7);
    expect(xrayApi('/warp/reg')).toBe('/panel/api/nodes/7/xray/warp/reg');
    expect(xrayApi('geodata/files')).toBe('/panel/api/nodes/7/xray/geodata/files');
  });
});
