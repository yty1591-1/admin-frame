import { z } from 'zod';
import type { LoginOutcome } from './types';

/** 外部响应在边界用 Zod 校验（FE-09）；类型仍以生成契约为唯一来源。 */
export const tokenPairSchema = z.object({
  access_token: z.string().min(1),
  refresh_token: z.string().min(1),
  token_type: z.literal('Bearer'),
  expires_in: z.number().int().positive(),
});

export const mfaChallengeSchema = z.object({
  mfa_required: z.literal(true),
  mfa_ticket: z.string().min(1),
});

export const loginOutcomeSchema = z.union([
  tokenPairSchema.transform((tokens): LoginOutcome => ({ kind: 'tokens', tokens })),
  mfaChallengeSchema.transform((challenge): LoginOutcome => ({ kind: 'mfa', challenge })),
]);

const dataScopeSchema = z.object({
  resource: z.string(),
  scope_type: z.enum(['all', 'merchant', 'self', 'merchant_set']),
  merchant_ids: z.array(z.string()).optional(),
});

export const subjectPermissionsSchema = z.object({
  channel: z.enum(['admin', 'biz']),
  subject_type: z.enum(['staff', 'merchant_staff']),
  subject_id: z.string(),
  merchant_id: z.string().optional(),
  roles: z.array(z.string()),
  permissions: z.array(z.string()),
  data_scopes: z.array(dataScopeSchema),
  snapshot_version: z.number().int(),
  generated_at: z.string(),
});
