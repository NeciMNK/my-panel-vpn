import { describe, expect, it } from 'vitest';

import { issuedCertPaths } from '@/lib/tls/issuedCertPaths';
import { splitDomains } from '@/pages/settings/TlsCertsTab';
import { TlsCertificateSchema, type TlsCertificate } from '@/schemas/tlsCert';

const cert: TlsCertificate = TlsCertificateSchema.parse({
  id: 1,
  domains: ['*.example.com', 'example.com'],
  status: 'valid',
  certFile: '/etc/x-ui/certs/wildcard.example.com/fullchain.pem',
  keyFile: '/etc/x-ui/certs/wildcard.example.com/privkey.pem',
  notAfter: 1717000000000,
  autoRenew: true,
  applyToPanel: false,
  applyToSub: false,
  lastError: '',
  nodeInstalls: {
    '2': {
      certFile: '/opt/x-ui/certs/wildcard.example.com/fullchain.pem',
      keyFile: '/opt/x-ui/certs/wildcard.example.com/privkey.pem',
      notAfter: 1717000000000,
      error: '',
    },
    '3': { certFile: '', keyFile: '', notAfter: 0, error: 'node offline' },
  },
});

describe('issuedCertPaths', () => {
  it('uses the panel paths for a local inbound', () => {
    expect(issuedCertPaths(cert, null)).toEqual({ certFile: cert.certFile, keyFile: cert.keyFile });
  });

  // The master's paths need not exist on the node, so a node inbound gets the node's copy.
  it("uses the node's own copy for a node inbound", () => {
    expect(issuedCertPaths(cert, 2)?.certFile).toBe(
      '/opt/x-ui/certs/wildcard.example.com/fullchain.pem',
    );
  });

  it('refuses a node whose copy failed or never happened', () => {
    expect(issuedCertPaths(cert, 3)).toBeNull();
    expect(issuedCertPaths(cert, 9)).toBeNull();
  });

  it('accepts the null nodeInstalls a never-pushed certificate serializes to', () => {
    const fresh = TlsCertificateSchema.parse({ ...cert, nodeInstalls: null });
    expect(issuedCertPaths(fresh, 2)).toBeNull();
  });
});

describe('splitDomains', () => {
  it('splits lines, commas and spaces and drops blanks', () => {
    expect(splitDomains(' example.com,\n*.example.com ;  a.example.com\n\n')).toEqual([
      'example.com',
      '*.example.com',
      'a.example.com',
    ]);
  });
});
