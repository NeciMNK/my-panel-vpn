import { z } from 'zod';

export const MigrationSourceSchema = z.enum(['remnawave', '3x-ui']);
export type MigrationSource = z.infer<typeof MigrationSourceSchema>;

export const MigrationFormSchema = z.object({
  source: MigrationSourceSchema,
  url: z.string().trim(),
  token: z.string().trim(),
  applySub: z.boolean(),
});
export type MigrationFormValues = z.infer<typeof MigrationFormSchema>;

const nullableList = <T extends z.ZodTypeAny>(item: T) =>
  z
    .array(item)
    .nullish()
    .transform((v) => v ?? []);

export const MigrationPreviewSchema = z.object({
  source: z.string(),
  servers: nullableList(
    z.object({
      key: z.string(),
      name: z.string(),
      address: z.string(),
      inbounds: z.number(),
      suggested: z.string(),
    }),
  ),
  inbounds: nullableList(
    z.object({
      key: z.string(),
      serverKey: z.string(),
      remark: z.string(),
      protocol: z.string(),
      port: z.number(),
      clients: z.number(),
      hosts: z.number(),
      conflict: z.string().optional(),
    }),
  ),
  clients: z.object({
    total: z.number(),
    enabled: z.number(),
    orphaned: z.number(),
  }),
  hosts: z.number(),
  sub: z.object({
    exampleUrl: z.string(),
    domain: z.string(),
    port: z.number(),
    path: z.string(),
  }),
  warnings: nullableList(z.string()),
  targets: nullableList(z.object({ value: z.string(), name: z.string(), address: z.string() })),
});
export type MigrationPreview = z.infer<typeof MigrationPreviewSchema>;

const ItemErrorSchema = z.object({ name: z.string(), error: z.string() });

export const MigrationResultSchema = z.object({
  inboundsCreated: z.number(),
  inboundErrors: nullableList(ItemErrorSchema),
  clients: z.object({
    created: z.number(),
    skipped: nullableList(z.object({ email: z.string(), reason: z.string() })),
  }),
  hostsCreated: z.number(),
  hostErrors: nullableList(ItemErrorSchema),
  subApplied: z.boolean(),
  panelRestart: z.boolean(),
});
export type MigrationResult = z.infer<typeof MigrationResultSchema>;
