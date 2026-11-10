import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Segmented, Select, Space, Table, Typography } from 'antd';
import { CloudOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { selectTagRender } from '@/components/form';
import type { NodeRecord } from '@/api/queries/useNodesQuery';

export type WarpMode = 'off' | 'all' | 'domains';

export interface WarpSwitchRecord {
  nodeId: number;
  mode: WarpMode;
  domains: string[] | null;
  lastError: string;
}

interface WarpRow {
  nodeId: number;
  name: string;
  mode: WarpMode;
  domains: string[];
  lastError: string;
}

const WARP_KEY = ['warpSwitch', 'list'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

// Every server gets a row, also those whose switch was never moved (= off).
export function warpRows(
  servers: { value: number; label: string }[],
  switches: WarpSwitchRecord[],
): WarpRow[] {
  return servers.map((s) => {
    const sw = switches.find((w) => w.nodeId === s.value);
    return {
      nodeId: s.value,
      name: s.label,
      mode: sw?.mode ?? 'off',
      domains: sw?.domains ?? [],
      lastError: sw?.lastError ?? '',
    };
  });
}

async function fetchSwitches(): Promise<WarpSwitchRecord[]> {
  const msg = await HttpUtil.get<WarpSwitchRecord[]>('/panel/api/warpSwitch/list', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch WARP switches');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

export default function WarpSwitchCard({ nodes }: { nodes: NodeRecord[] }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: switches = [], isFetching } = useQuery({
    queryKey: WARP_KEY,
    queryFn: fetchSwitches,
  });
  const [drafts, setDrafts] = useState<Record<number, string[]>>({});

  const servers = useMemo(
    () => [
      { value: 0, label: t('pages.xray.targetLocal') },
      ...nodes
        .filter((n) => !n.transitive)
        .map((n) => ({ value: n.id, label: n.name || `#${n.id}` })),
    ],
    [nodes, t],
  );
  const rows = warpRows(servers, switches);

  const set = useMutation({
    mutationFn: (req: { nodeId: number; mode: WarpMode; domains: string[] }) =>
      HttpUtil.post('/panel/api/warpSwitch/set', req, JSON_HEADERS),
    onSettled: () => queryClient.invalidateQueries({ queryKey: WARP_KEY }),
  });

  const domainsOf = (r: WarpRow) => drafts[r.nodeId] ?? r.domains;

  const columns = [
    { title: t('pages.nodes.warp.server'), dataIndex: 'name', key: 'name' },
    {
      title: t('pages.nodes.warp.mode'),
      key: 'mode',
      render: (_: unknown, r: WarpRow) => (
        <Segmented
          value={r.mode}
          options={(['off', 'all', 'domains'] as const).map((m) => ({
            value: m,
            label: t(`pages.nodes.warp.modes.${m}`),
            disabled: m === 'domains' && domainsOf(r).length === 0,
          }))}
          onChange={(v) =>
            set.mutate({ nodeId: r.nodeId, mode: v as WarpMode, domains: domainsOf(r) })
          }
        />
      ),
    },
    {
      title: t('pages.nodes.warp.domains'),
      key: 'domains',
      render: (_: unknown, r: WarpRow) => (
        <Space.Compact style={{ minWidth: 320 }}>
          <Select
            aria-label={t('pages.nodes.warp.domains')}
            mode="tags"
            tagRender={selectTagRender}
            style={{ width: '100%' }}
            placeholder="geosite:openai, chatgpt.com"
            value={domainsOf(r)}
            tokenSeparators={[',', ' ']}
            onChange={(v: string[]) => setDrafts({ ...drafts, [r.nodeId]: v })}
          />
          {r.mode === 'domains' && drafts[r.nodeId] && (
            <Button
              type="primary"
              loading={set.isPending}
              onClick={() =>
                set.mutate(
                  { nodeId: r.nodeId, mode: 'domains', domains: drafts[r.nodeId] },
                  {
                    onSuccess: () => {
                      const { [r.nodeId]: _saved, ...rest } = drafts;
                      setDrafts(rest);
                    },
                  },
                )
              }
            >
              {t('save')}
            </Button>
          )}
        </Space.Compact>
      ),
    },
    {
      title: '',
      key: 'error',
      render: (_: unknown, r: WarpRow) =>
        r.lastError && <Typography.Text type="danger">{r.lastError}</Typography.Text>,
    },
  ];

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <CloudOutlined aria-hidden />
          {t('pages.nodes.warp.title')}
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">{t('pages.nodes.warp.desc')}</Typography.Paragraph>
      <Table
        rowKey="nodeId"
        size="small"
        loading={isFetching}
        dataSource={rows}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
}
