import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Form, Select, Space, Table, Tag, Typography } from 'antd';
import { DeleteOutlined, PushpinOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { useClientOptions } from '@/api/queries/useClientOptions';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import type { CascadeFixedExit } from '@/generated/types';
import { countryFlag } from './CascadesCard';

const FIXED_EXITS_KEY = ['cascades', 'fixedExits'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

interface FixedExitDraft {
  email: string;
  nodeId: number | undefined;
}

// A pinned exit that is off or offline is skipped: the user is on another exit until it is back.
export function fixedExitDown(
  nodeId: number,
  nodes: Pick<NodeRecord, 'id' | 'enable' | 'status'>[],
): boolean {
  if (nodeId === 0) return false;
  const n = nodes.find((x) => x.id === nodeId);
  return !n || n.enable === false || n.status !== 'online';
}

async function fetchFixedExits(): Promise<CascadeFixedExit[]> {
  const msg = await HttpUtil.get<CascadeFixedExit[]>('/panel/api/cascades/fixedExits', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch fixed exits');
  return msg.obj ?? [];
}

export default function FixedExitsCard({ nodes }: { nodes: NodeRecord[] }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: rows = [], isFetching } = useQuery({
    queryKey: FIXED_EXITS_KEY,
    queryFn: fetchFixedExits,
  });
  const { data: emails = [] } = useClientOptions();
  const [draft, setDraft] = useState<FixedExitDraft>({
    email: '',
    nodeId: undefined,
  });

  const servers = useMemo(
    () => [
      { value: 0, label: t('pages.xray.targetLocal') },
      ...nodes
        .filter((n) => !n.transitive)
        .map((n) => ({
          value: n.id,
          label: `${n.country ? `${countryFlag(n.country)} ${n.country} · ` : ''}${n.name || `#${n.id}`}`,
        })),
    ],
    [nodes, t],
  );
  const serverLabel = (id: number) => servers.find((s) => s.value === id)?.label ?? `#${id}`;
  const invalidate = () => queryClient.invalidateQueries({ queryKey: FIXED_EXITS_KEY });
  const pin = useMutation({
    mutationFn: (req: { email: string; nodeId: number }) =>
      HttpUtil.post('/panel/api/cascades/fixedExits/set', req, JSON_HEADERS),
    onSettled: invalidate,
  });
  const unpin = useMutation({
    mutationFn: (req: { email: string; nodeId: number }) =>
      HttpUtil.post('/panel/api/cascades/fixedExits/del', req, JSON_HEADERS),
    onSettled: invalidate,
  });

  const columns = [
    {
      title: t('pages.nodes.fixedExits.user'),
      dataIndex: 'email',
      key: 'email',
    },
    {
      title: t('pages.nodes.fixedExits.exit'),
      key: 'exit',
      render: (_: unknown, r: CascadeFixedExit) => (
        <Space wrap>
          {serverLabel(r.nodeId)}
          {fixedExitDown(r.nodeId, nodes) && (
            <Tag color="warning">{t('pages.nodes.fixedExits.fallback')}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, r: CascadeFixedExit) => (
        <Button
          size="small"
          danger
          aria-label={`${t('delete')} ${r.email}`}
          icon={<DeleteOutlined aria-hidden />}
          loading={unpin.isPending}
          onClick={() => unpin.mutate({ email: r.email, nodeId: r.nodeId })}
        />
      ),
    },
  ];

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <PushpinOutlined aria-hidden />
          {t('pages.nodes.fixedExits.title')}
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">
        {t('pages.nodes.fixedExits.desc')}
      </Typography.Paragraph>
      <Form layout="inline" style={{ marginBottom: 8, rowGap: 8 }}>
        <Form.Item label={t('pages.nodes.fixedExits.user')} htmlFor="fixed-exit-user">
          <Select
            id="fixed-exit-user"
            showSearch
            style={{ minWidth: 200 }}
            placeholder={t('pages.nodes.fixedExits.user')}
            value={draft.email || undefined}
            options={emails.map((e) => ({ value: e, label: e }))}
            onChange={(email: string) => setDraft({ ...draft, email })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.fixedExits.exit')} htmlFor="fixed-exit-node">
          <Select
            id="fixed-exit-node"
            showSearch
            optionFilterProp="label"
            style={{ minWidth: 220 }}
            placeholder={t('pages.nodes.fixedExits.exit')}
            value={draft.nodeId}
            options={servers}
            onChange={(nodeId: number) => setDraft({ ...draft, nodeId })}
          />
        </Form.Item>
        <Form.Item>
          <Button
            type="primary"
            disabled={!draft.email || draft.nodeId === undefined}
            loading={pin.isPending}
            onClick={() =>
              draft.nodeId !== undefined &&
              pin.mutate(
                { email: draft.email, nodeId: draft.nodeId },
                { onSuccess: () => setDraft({ email: '', nodeId: undefined }) },
              )
            }
          >
            {t('pages.nodes.fixedExits.pin')}
          </Button>
        </Form.Item>
      </Form>
      <Table
        rowKey="id"
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
