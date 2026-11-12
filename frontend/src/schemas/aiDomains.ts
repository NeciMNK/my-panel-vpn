import { z } from 'zod';

export const AISuggestStatusSchema = z.enum(['pending', 'added', 'partial', 'declined', 'present']);
export type AISuggestStatus = z.infer<typeof AISuggestStatusSchema>;

export const AIDomainSuggestionSchema = z.object({
  id: z.number(),
  tgUserId: z.number(),
  email: z.string(),
  domains: z.string(),
  status: AISuggestStatusSchema,
  createdAt: z.number(),
  resolvedAt: z.number(),
});
export type AIDomainSuggestion = z.infer<typeof AIDomainSuggestionSchema>;

export const AIDomainsViewSchema = z.object({
  domains: z.string(),
  suggestions: z
    .array(AIDomainSuggestionSchema)
    .nullable()
    .transform((v) => v ?? []),
});
export type AIDomainsView = z.infer<typeof AIDomainsViewSchema>;
