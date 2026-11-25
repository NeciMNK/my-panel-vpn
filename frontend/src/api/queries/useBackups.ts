import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  BackupSettingsSchema,
  BackupStatusSchema,
  NodeRestoreStatusSchema,
  StoredFileSchema,
  type BackupSettings,
  type BackupSettingsRequest,
  type BackupStatus,
  type NodeRestoreRequest,
  type NodeRestoreStatus,
  type StoredFile,
} from '@/generated/zod';
import { keys } from '@/api/queryKeys';
import { HttpUtil } from '@/utils';

const JSON_HEADERS = {
  headers: { 'Content-Type': 'application/json' },
} as const;

async function getObj<T>(url: string, parse: (v: unknown) => T): Promise<T> {
  const msg = await HttpUtil.get(url, undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || `Failed to fetch ${url}`);
  return parse(msg.obj);
}

export function useBackupSettingsQuery() {
  return useQuery({
    queryKey: keys.backups.settings(),
    queryFn: () =>
      getObj<BackupSettings>('/panel/api/backup/settings', (v) => BackupSettingsSchema.parse(v)),
  });
}

// A started backup runs in the background, so poll until it is done.
export function useBackupStatusQuery() {
  return useQuery({
    queryKey: keys.backups.status(),
    queryFn: () =>
      getObj<BackupStatus>('/panel/api/backup/status', (v) => BackupStatusSchema.parse(v)),
    refetchInterval: (query) => (query.state.data?.running ? 3000 : false),
  });
}

export function useNodeBackupsQuery(nodeId: number | undefined) {
  return useQuery({
    queryKey: keys.backups.node(nodeId ?? 0),
    enabled: !!nodeId,
    retry: false,
    queryFn: () =>
      getObj<StoredFile[]>(`/panel/api/backup/node/${nodeId}`, (v) =>
        StoredFileSchema.array().parse(v),
      ),
  });
}

export function useNodeRestoreStatusQuery(enabled: boolean) {
  return useQuery({
    queryKey: keys.backups.restore(),
    enabled,
    queryFn: () =>
      getObj<NodeRestoreStatus>('/panel/api/backup/restoreStatus', (v) =>
        NodeRestoreStatusSchema.parse(v),
      ),
    refetchInterval: (query) => (query.state.data?.running ? 2000 : false),
  });
}

export function useBackupMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: keys.backups.root() });
  const onSuccess = (msg: { success?: boolean } | undefined) => {
    if (msg?.success) void invalidate();
  };
  const save = useMutation({
    mutationFn: (req: BackupSettingsRequest) =>
      HttpUtil.post('/panel/api/backup/settings', req, JSON_HEADERS),
    onSuccess,
  });
  const test = useMutation({
    mutationFn: () => HttpUtil.post('/panel/api/backup/test'),
    onSuccess,
  });
  const run = useMutation({
    mutationFn: (what: 'db' | 'nodes') => HttpUtil.post(`/panel/api/backup/run/${what}`),
    onSuccess,
  });
  const restore = useMutation({
    mutationFn: ({ nodeId, req }: { nodeId: number; req: NodeRestoreRequest }) =>
      HttpUtil.post(`/panel/api/backup/restoreNode/${nodeId}`, req, JSON_HEADERS),
    onSuccess,
  });
  return {
    save: (req: BackupSettingsRequest) => save.mutateAsync(req),
    test: () => test.mutateAsync(),
    run: (what: 'db' | 'nodes') => run.mutateAsync(what),
    restore: (nodeId: number, req: NodeRestoreRequest) => restore.mutateAsync({ nodeId, req }),
    busy: save.isPending || test.isPending || run.isPending || restore.isPending,
  };
}
