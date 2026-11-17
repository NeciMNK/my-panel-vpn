import { z } from 'zod';

export const ProtocolSchema = z.enum([
  'vmess',
  'vless',
  'trojan',
  'shadowsocks',
  'wireguard',
  'hysteria',
  'http',
  'mixed',
  'tunnel',
  'tun',
  'mtproto',
  'amneziawg',
  'tuic',
  'masque',
]);
export type Protocol = z.infer<typeof ProtocolSchema>;

// Const map matching the legacy models/inbound.ts `Protocols` export so
// call sites can swap the import without touching `Protocols.VLESS`-style
// references throughout the codebase. Frozen so downstream code can't
// mutate the dispatch table.
export const Protocols = Object.freeze({
  VMESS: 'vmess',
  VLESS: 'vless',
  TROJAN: 'trojan',
  SHADOWSOCKS: 'shadowsocks',
  WIREGUARD: 'wireguard',
  HYSTERIA: 'hysteria',
  HTTP: 'http',
  MIXED: 'mixed',
  TUNNEL: 'tunnel',
  TUN: 'tun',
  MTPROTO: 'mtproto',
  AMNEZIAWG: 'amneziawg',
  TUIC: 'tuic',
  MASQUE: 'masque',
});
