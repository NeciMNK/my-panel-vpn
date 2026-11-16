import { describe, expect, it } from 'vitest';

import { certMissingOnNode } from '@/pages/nodes/SubEdgesCard';
import type { TlsCertificate } from '@/schemas/tlsCert';

const cert = {
  id: 1,
  domains: ['sub.example.com'],
  nodeInstalls: {
    '2': {
      certFile: '/etc/x-ui/certs/sub/fullchain.pem',
      keyFile: '/etc/x-ui/certs/sub/privkey.pem',
    },
  },
} as unknown as TlsCertificate;

// The form warns before saving an edge whose node cannot serve the certificate yet.
describe('certMissingOnNode', () => {
  it('warns for a node the certificate has not reached', () => {
    expect(certMissingOnNode(cert, 3)).toBe(true);
  });
  it('stays quiet for a node that has the certificate', () => {
    expect(certMissingOnNode(cert, 2)).toBe(false);
  });
  it('stays quiet for plain HTTP', () => {
    expect(certMissingOnNode(undefined, 3)).toBe(false);
  });
});
