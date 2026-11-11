import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Card,
  Form,
  InputNumber,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { DashboardOutlined, DeleteOutlined, ThunderboltOutlined } from '@ant-design/icons';

import { HttpUtil, IntlUtil } from '@/utils';
import { useClientOptions } from '@/api/queries/useClientOptions';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import type { SpeedTestSettings, SpeedTestStats } from '@/generated/types';

import SpeedTestHistoryModal, { speedTestProviderKey } from './SpeedTestHistoryModal';

export interface ClientSpeedRecord {
  email: string;
  minMbps: number;
  maxMbps: number;
}

export interface ServerLoadRecord {
  nodeId: number;
  capacityMbps: number;
  usedMbps: number;
  online: number;
  freePerUserMbps: number;
}

export interface SpeedOverviewRecord {
  clients: ClientSpeedRecord[] | null;
  servers: { nodeId: number; capacityMbps: number }[] | null;
  load: ServerLoadRecord[] | null;
  testing?: number[] | null;
  testStats?: SpeedTestStats[] | null;
  testSettings?: SpeedTestSettings;
}

export interface SpeedServerRow {
  nodeId: number;
  name: string;
  capacityMbps: number;
  load?: ServerLoadRecord;
  test?: SpeedTestStats;
  testing: boolean;
}

const SPEED_KEY = ['speed', 'overview'] as const;
const SPEED_INPUT_WIDTH = 140;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

// Every server gets a row; one without a capacity has no load to show yet.
export function speedServerRows(
  servers: { value: number; label: string }[],
  overview: SpeedOverviewRecord | undefined,
): SpeedServerRow[] {
  return servers.map((s) => ({
    nodeId: s.value,
    name: s.label,
    capacityMbps: overview?.servers?.find((c) => c.nodeId === s.value)?.capacityMbps ?? 0,
    load: overview?.load?.find((l) => l.nodeId === s.value),
    test: overview?.testStats?.find((st) => st.nodeId === s.value),
    testing: overview?.testing?.includes(s.value) ?? false,
  }));
}

async function fetchOverview(): Promise<SpeedOverviewRecord> {
  const msg = await HttpUtil.get<SpeedOverviewRecord>('/panel/api/speed/overview', undefined, {
    silent: true,
  });
  if (!msg?.success || !msg.obj) throw new Error(msg?.msg || 'Failed to fetch speed limits');
  return msg.obj;
}

export default function SpeedCard({ nodes }: { nodes: NodeRecord[] }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: overview, isFetching } = useQuery({
    queryKey: SPEED_KEY,
    queryFn: fetchOverview,
    refetchInterval: (q) => (q.state.data?.testing?.length ? 4_000 : 30_000),
  });
  const { data: emails = [] } = useClientOptions();
  const [capDrafts, setCapDrafts] = useState<Record<number, number>>({});
  const [draft, setDraft] = useState<ClientSpeedRecord>({ email: '', minMbps: 0, maxMbps: 0 });
  const [historyFor, setHistoryFor] = useState<{ nodeId: number; name: string } | null>(null);
  const [everyHoursDraft, setEveryHoursDraft] = useState<number | null>(null);

  const servers = useMemo(
    () => [
      { value: 0, label: t('pages.xray.targetLocal') },
      ...nodes
        .filter((n) => !n.transitive)
        .map((n) => ({ value: n.id, label: n.name || `#${n.id}` })),
    ],
    [nodes, t],
  );
  const invalidate = () => queryClient.invalidateQueries({ queryKey: SPEED_KEY });
  const setCapacity = useMutation({
    mutationFn: (req: { nodeId: number; capacityMbps: number }) =>
      HttpUtil.post('/panel/api/speed/capacity', req, JSON_HEADERS),
    onSettled: invalidate,
  });
  const setClient = useMutation({
    mutationFn: (req: ClientSpeedRecord) =>
      HttpUtil.post('/panel/api/speed/client', req, JSON_HEADERS),
    onSettled: invalidate,
  });

  const startTest = useMutation({
    mutationFn: (nodeId: number) =>
      HttpUtil.post('/panel/api/speed/test', { nodeId }, JSON_HEADERS),
    onSettled: invalidate,
  });
  const setTestSettings = useMutation({
    mutationFn: (req: SpeedTestSettings) =>
      HttpUtil.post('/panel/api/speed/testSettings', req, JSON_HEADERS),
    onSettled: invalidate,
  });
  const testSettings = overview?.testSettings ?? { autoCapacity: true, everyHours: 0 };

  const serverColumns = [
    { title: t('pages.nodes.speed.server'), dataIndex: 'name', key: 'name' },
    {
      title: t('pages.nodes.speed.capacity'),
      key: 'capacity',
      render: (_: unknown, r: SpeedServerRow) => (
        <Space.Compact>
          <InputNumber
            aria-label={`${t('pages.nodes.speed.capacity')} ${r.name}`}
            style={{ width: SPEED_INPUT_WIDTH }}
            min={0}
            value={capDrafts[r.nodeId] ?? r.capacityMbps}
            onChange={(v) => setCapDrafts({ ...capDrafts, [r.nodeId]: v ?? 0 })}
          />
          {capDrafts[r.nodeId] !== undefined && (
            <Button
              type="primary"
              loading={setCapacity.isPending}
              onClick={() =>
                setCapacity.mutate(
                  { nodeId: r.nodeId, capacityMbps: capDrafts[r.nodeId] },
                  {
                    onSuccess: () => {
                      const { [r.nodeId]: _saved, ...rest } = capDrafts;
                      setCapDrafts(rest);
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
      title: t('pages.nodes.speed.load'),
      key: 'load',
      render: (_: unknown, r: SpeedServerRow) =>
        r.load
          ? t('pages.nodes.speed.loadValue', {
              used: r.load.usedMbps.toFixed(0),
              online: r.load.online,
              free: r.load.freePerUserMbps.toFixed(1),
            })
          : '—',
    },
    {
      title: t('pages.nodes.speed.test'),
      key: 'test',
      render: (_: unknown, r: SpeedServerRow) => {
        const last = r.test?.last;
        return (
          <Space wrap>
            <Button
              size="small"
              icon={<DashboardOutlined aria-hidden />}
              loading={r.testing}
              onClick={() => startTest.mutate(r.nodeId)}
            >
              {r.testing ? t('pages.nodes.speed.testing') : t('pages.nodes.speed.testNow')}
            </Button>
            {last && last.error !== '' && (
              <Tooltip title={last.error}>
                <Tag color="error">{t('pages.nodes.speed.testFailed')}</Tag>
              </Tooltip>
            )}
            {last && last.error === '' && (
              <Tooltip
                title={`${t(speedTestProviderKey(last.provider))}: ${last.server} · ${IntlUtil.formatDate(last.createdAt)}`}
              >
                <span>
                  {t('pages.nodes.speed.testResult', {
                    down: last.downloadMbps.toFixed(0),
                    up: last.uploadMbps.toFixed(0),
                    ping: last.pingMs.toFixed(0),
                  })}
                </span>
              </Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: t('pages.nodes.speed.stats'),
      key: 'stats',
      render: (_: unknown, r: SpeedServerRow) => (
        <Space wrap>
          {r.test && r.test.count > 0 ? (
            <span>
              {t('pages.nodes.speed.statsValue', {
                down: r.test.avgDownloadMbps.toFixed(0),
                up: r.test.avgUploadMbps.toFixed(0),
                min: r.test.minDownloadMbps.toFixed(0),
                max: r.test.maxDownloadMbps.toFixed(0),
                count: r.test.count,
              })}
            </span>
          ) : (
            '—'
          )}
          {r.test && (
            <Button
              size="small"
              type="link"
              onClick={() => setHistoryFor({ nodeId: r.nodeId, name: r.name })}
            >
              {t('pages.nodes.speed.history')}
            </Button>
          )}
        </Space>
      ),
    },
  ];

  const clientColumns = [
    { title: t('pages.nodes.speed.user'), dataIndex: 'email', key: 'email' },
    { title: t('pages.nodes.speed.min'), dataIndex: 'minMbps', key: 'min' },
    { title: t('pages.nodes.speed.max'), dataIndex: 'maxMbps', key: 'max' },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, r: ClientSpeedRecord) => (
        <Space>
          <Button size="small" onClick={() => setDraft(r)}>
            {t('edit')}
          </Button>
          <Button
            size="small"
            danger
            aria-label={`${t('delete')} ${r.email}`}
            icon={<DeleteOutlined aria-hidden />}
            onClick={() => setClient.mutate({ email: r.email, minMbps: 0, maxMbps: 0 })}
          />
        </Space>
      ),
    },
  ];

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <ThunderboltOutlined aria-hidden />
          {t('pages.nodes.speed.title')}
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">{t('pages.nodes.speed.desc')}</Typography.Paragraph>
      <Typography.Paragraph type="secondary">
        {t('pages.nodes.speed.testDesc')}
      </Typography.Paragraph>
      <Form layout="inline" style={{ marginBottom: 12, rowGap: 8 }}>
        <Form.Item label={t('pages.nodes.speed.autoCapacity')} htmlFor="speed-auto-capacity">
          <Switch
            id="speed-auto-capacity"
            checked={testSettings.autoCapacity}
            loading={setTestSettings.isPending}
            onChange={(autoCapacity) => setTestSettings.mutate({ ...testSettings, autoCapacity })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.speed.everyHours')} htmlFor="speed-every-hours">
          <Space.Compact>
            <InputNumber
              id="speed-every-hours"
              style={{ width: SPEED_INPUT_WIDTH }}
              min={0}
              max={168}
              value={everyHoursDraft ?? testSettings.everyHours}
              onChange={(v) => setEveryHoursDraft(v ?? 0)}
            />
            {everyHoursDraft !== null && (
              <Button
                type="primary"
                loading={setTestSettings.isPending}
                onClick={() =>
                  setTestSettings.mutate(
                    { ...testSettings, everyHours: everyHoursDraft },
                    { onSuccess: () => setEveryHoursDraft(null) },
                  )
                }
              >
                {t('save')}
              </Button>
            )}
          </Space.Compact>
        </Form.Item>
      </Form>
      <Table
        rowKey="nodeId"
        size="small"
        loading={isFetching}
        dataSource={speedServerRows(servers, overview)}
        columns={serverColumns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
      <Form layout="inline" style={{ margin: '16px 0 8px', rowGap: 8 }}>
        <Form.Item label={t('pages.nodes.speed.user')} htmlFor="speed-user">
          <Select
            id="speed-user"
            showSearch
            style={{ minWidth: 200 }}
            placeholder={t('pages.nodes.speed.user')}
            value={draft.email || undefined}
            options={emails.map((e) => ({ value: e, label: e }))}
            onChange={(email: string) => setDraft({ ...draft, email })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.speed.min')} htmlFor="speed-min">
          <InputNumber
            id="speed-min"
            style={{ width: SPEED_INPUT_WIDTH }}
            min={0}
            value={draft.minMbps}
            onChange={(v) => setDraft({ ...draft, minMbps: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.speed.max')} htmlFor="speed-max">
          <InputNumber
            id="speed-max"
            style={{ width: SPEED_INPUT_WIDTH }}
            min={0}
            value={draft.maxMbps}
            onChange={(v) => setDraft({ ...draft, maxMbps: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item>
          <Button
            type="primary"
            disabled={!draft.email}
            loading={setClient.isPending}
            onClick={() =>
              setClient.mutate(draft, {
                onSuccess: () => setDraft({ email: '', minMbps: 0, maxMbps: 0 }),
              })
            }
          >
            {t('save')}
          </Button>
        </Form.Item>
      </Form>
      <Table
        rowKey="email"
        size="small"
        dataSource={overview?.clients ?? []}
        columns={clientColumns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
      <SpeedTestHistoryModal server={historyFor} onClose={() => setHistoryFor(null)} />
    </Card>
  );
}
