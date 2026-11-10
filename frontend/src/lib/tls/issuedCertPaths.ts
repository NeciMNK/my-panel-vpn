import type { TlsCertificate } from '@/schemas/tlsCert';

export interface CertPaths {
  certFile: string;
  keyFile: string;
}

// A node-assigned inbound runs on the node, so it needs the node's copy, and
// only once that copy succeeded; the master's own paths may not exist there.
export function issuedCertPaths(cert: TlsCertificate, nodeId?: number | null): CertPaths | null {
  if (typeof nodeId !== 'number') {
    return cert.certFile ? { certFile: cert.certFile, keyFile: cert.keyFile } : null;
  }
  const install = cert.nodeInstalls?.[String(nodeId)];
  if (!install || install.error || !install.certFile) return null;
  return { certFile: install.certFile, keyFile: install.keyFile };
}
