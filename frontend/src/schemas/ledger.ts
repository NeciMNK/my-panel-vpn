import { z } from 'zod';

const MONTH_PATTERN = /^\d{4}-(0[1-9]|1[0-2])$/;

export const LedgerServerSchema = z.object({
  id: z.number(),
  name: z.string(),
  monthlyCost: z.number(),
  billingDay: z.number(),
  startMonth: z.string(),
  endMonth: z.string(),
  paidThrough: z.string(),
});
export type LedgerServer = z.infer<typeof LedgerServerSchema>;

export const LedgerEntryKindSchema = z.enum(['income', 'expense']);

export const LedgerEntrySchema = z.object({
  id: z.number(),
  month: z.string(),
  kind: LedgerEntryKindSchema,
  amount: z.number(),
  email: z.string(),
  note: z.string(),
  createdAt: z.number(),
});
export type LedgerEntry = z.infer<typeof LedgerEntrySchema>;

export const LedgerNonPayingSchema = z.object({
  email: z.string(),
  reason: z.enum(['client', 'group', 'freePlan']),
  group: z.string(),
});
export type LedgerNonPaying = z.infer<typeof LedgerNonPayingSchema>;

export const LedgerPaymentSchema = z.object({
  price: z
    .number({ error: 'pages.ledger.errPrice' })
    .int('pages.ledger.errPrice')
    .min(0, 'pages.ledger.errPrice'),
  currency: z.string(),
  discounts: z.string(),
  details: z.string(),
  friendDiscount: z
    .number({ error: 'pages.ledger.errFriendDiscount' })
    .int('pages.ledger.errFriendDiscount')
    .min(0, 'pages.ledger.errFriendDiscount')
    .max(99, 'pages.ledger.errFriendDiscount'),
});
export type LedgerPayment = z.infer<typeof LedgerPaymentSchema>;

export const LedgerServerDueSchema = z.object({
  serverId: z.number(),
  name: z.string(),
  amount: z.number(),
  date: z.string(),
});

export const LedgerReportSchema = z.object({
  month: z.string(),
  currency: z.string(),
  price: z.number(),
  payment: LedgerPaymentSchema,
  income: z.number(),
  serverCosts: z.number(),
  otherExpenses: z.number(),
  expenses: z.number(),
  balance: z.number(),
  payingClients: z.number(),
  expectedIncome: z.number(),
  nonPaying: z.array(LedgerNonPayingSchema),
  freeGroups: z.array(z.string()),
  servers: z.array(LedgerServerSchema),
  upcoming: z.array(LedgerServerDueSchema),
  entries: z.array(LedgerEntrySchema),
});
export type LedgerReport = z.infer<typeof LedgerReportSchema>;

export const LedgerServerFormSchema = z
  .object({
    id: z.number(),
    name: z.string().trim().min(1, 'pages.ledger.errName'),
    monthlyCost: z
      .number({ error: 'pages.ledger.errAmount' })
      .int('pages.ledger.errAmount')
      .positive('pages.ledger.errAmount'),
    billingDay: z
      .number({ error: 'pages.ledger.errDay' })
      .int('pages.ledger.errDay')
      .min(1, 'pages.ledger.errDay')
      .max(31, 'pages.ledger.errDay'),
    startMonth: z.string().regex(MONTH_PATTERN, 'pages.ledger.errMonth'),
    endMonth: z.string().regex(MONTH_PATTERN, 'pages.ledger.errMonth').or(z.literal('')),
  })
  .refine((v) => v.endMonth === '' || v.endMonth >= v.startMonth, {
    message: 'pages.ledger.errRange',
    path: ['endMonth'],
  });
export type LedgerServerFormValues = z.infer<typeof LedgerServerFormSchema>;

export const LedgerEntryFormSchema = z.object({
  month: z.string().regex(MONTH_PATTERN, 'pages.ledger.errMonth'),
  kind: LedgerEntryKindSchema,
  amount: z
    .number({ error: 'pages.ledger.errAmount' })
    .int('pages.ledger.errAmount')
    .positive('pages.ledger.errAmount'),
  email: z.string(),
  note: z.string(),
});
export type LedgerEntryFormValues = z.infer<typeof LedgerEntryFormSchema>;
