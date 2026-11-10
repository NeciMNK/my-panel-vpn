import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import {
  TlsCertListSchema,
  TlsCertSettingsSchema,
  type TlsCertificate,
  type TlsCertSettings,
} from '@/schemas/tlsCert';

async function fetchTlsCerts(): Promise<TlsCertificate[]> {
  const msg = await HttpUtil.get('/panel/api/certs/list', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch certificates');
  const validated = parseMsg(msg, TlsCertListSchema, 'certs');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

async function fetchTlsCertSettings(): Promise<TlsCertSettings> {
  const msg = await HttpUtil.get('/panel/api/certs/settings', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch certificate settings');
  return parseMsg(msg, TlsCertSettingsSchema, 'certs/settings').obj as TlsCertSettings;
}

// Issuance runs in the background, so poll while any certificate is pending.
export function useTlsCertsQuery() {
  return useQuery({
    queryKey: keys.tlsCerts.list(),
    queryFn: fetchTlsCerts,
    refetchInterval: (query) =>
      query.state.data?.some((c) => c.status === 'pending') ? 5000 : false,
  });
}

export function useTlsCertSettingsQuery() {
  return useQuery({
    queryKey: keys.tlsCerts.settings(),
    queryFn: fetchTlsCertSettings,
  });
}

export interface IssueTlsCertInput {
  domains: string[];
  autoRenew: boolean;
  applyToPanel: boolean;
}

export function useTlsCertMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: keys.tlsCerts.root() });
  const onSuccess = (msg: { success?: boolean } | undefined) => {
    if (msg?.success) invalidate();
  };

  const saveToken = useMutation({
    mutationFn: (cloudflareToken: string) =>
      HttpUtil.post('/panel/api/certs/settings', { cloudflareToken }),
    onSuccess,
  });
  const issue = useMutation({
    mutationFn: (input: IssueTlsCertInput) => HttpUtil.post('/panel/api/certs/issue', input),
    onSuccess,
  });
  const renew = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/certs/renew/${id}`),
    onSuccess,
  });
  const setAutoRenew = useMutation({
    mutationFn: ({ id, autoRenew }: { id: number; autoRenew: boolean }) =>
      HttpUtil.post(`/panel/api/certs/update/${id}`, { autoRenew }),
    onSuccess,
  });
  const remove = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/certs/del/${id}`),
    onSuccess,
  });

  return {
    saveToken: (token: string) => saveToken.mutateAsync(token),
    issue: (input: IssueTlsCertInput) => issue.mutateAsync(input),
    renew: (id: number) => renew.mutateAsync(id),
    setAutoRenew: (id: number, autoRenew: boolean) => setAutoRenew.mutateAsync({ id, autoRenew }),
    remove: (id: number) => remove.mutateAsync(id),
    busy: saveToken.isPending || issue.isPending,
  };
}
