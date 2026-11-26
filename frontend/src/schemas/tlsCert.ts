import { z } from 'zod';

export const TlsCertStatusSchema = z.enum(['pending', 'valid', 'error']);

export const TlsCertNodeInstallSchema = z.object({
  certFile: z.string(),
  keyFile: z.string(),
  notAfter: z.number(),
  error: z.string(),
});
export type TlsCertNodeInstall = z.infer<typeof TlsCertNodeInstallSchema>;

// nodeInstalls is a Go map keyed by node id: JSON object keys, null until a push ran.
export const TlsCertificateSchema = z.object({
  id: z.number(),
  domains: z.array(z.string()),
  status: TlsCertStatusSchema,
  certFile: z.string(),
  keyFile: z.string(),
  notAfter: z.number(),
  autoRenew: z.boolean(),
  applyToPanel: z.boolean(),
  applyToSub: z.boolean(),
  lastError: z.string(),
  nodeInstalls: z.record(z.string(), TlsCertNodeInstallSchema).nullish(),
  createdAt: z.number().optional(),
  updatedAt: z.number().optional(),
});
export type TlsCertificate = z.infer<typeof TlsCertificateSchema>;

export const TlsCertListSchema = z.array(TlsCertificateSchema);

export const TlsCertSettingsSchema = z.object({
  cloudflareTokenSet: z.boolean(),
});
export type TlsCertSettings = z.infer<typeof TlsCertSettingsSchema>;
