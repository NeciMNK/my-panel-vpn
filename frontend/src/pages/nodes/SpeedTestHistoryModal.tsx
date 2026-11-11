import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Modal, Table, Tag } from 'antd';

import { HttpUtil, IntlUtil } from '@/utils';
import type { SpeedTestResult } from '@/generated/types';

export function speedTestProviderKey(provider: string): string {
  return provider === 'yandex'
    ? 'pages.nodes.speed.providerYandex'
    : 'pages.nodes.speed.providerSpeedtest';
}

async function fetchHistory(nodeId: number): Promise<SpeedTestResult[]> {
  const msg = await HttpUtil.get<SpeedTestResult[]>(
    '/panel/api/speed/tests',
    { nodeId, limit: 200 },
    { silent: true },
  );
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch speed tests');
  return msg.obj ?? [];
}

export default function SpeedTestHistoryModal({
  server,
  onClose,
}: {
  server: { nodeId: number; name: string } | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { data = [], isFetching } = useQuery({
    queryKey: ['speed', 'tests', server?.nodeId],
    queryFn: () => fetchHistory(server?.nodeId ?? 0),
    enabled: server !== null,
  });
  const failed = (r: SpeedTestResult) => r.error !== '';
  const columns = [
    {
      title: t('pages.nodes.speed.when'),
      key: 'when',
      render: (_: unknown, r: SpeedTestResult) => IntlUtil.formatDate(r.createdAt),
    },
    {
      title: t('pages.nodes.speed.trigger'),
      key: 'trigger',
      render: (_: unknown, r: SpeedTestResult) =>
        t(
          r.trigger === 'schedule'
            ? 'pages.nodes.speed.triggerSchedule'
            : 'pages.nodes.speed.triggerManual',
        ),
    },
    {
      title: t('pages.nodes.speed.provider'),
      key: 'provider',
      render: (_: unknown, r: SpeedTestResult) =>
        failed(r) ? (
          <Tag color="error" title={r.error}>
            {t('pages.nodes.speed.testFailed')}
          </Tag>
        ) : (
          t(speedTestProviderKey(r.provider))
        ),
    },
    {
      title: t('pages.nodes.speed.testServer'),
      dataIndex: 'server',
      key: 'server',
    },
    {
      title: t('pages.nodes.speed.ping'),
      key: 'ping',
      render: (_: unknown, r: SpeedTestResult) => (failed(r) ? '—' : r.pingMs),
    },
    {
      title: t('pages.nodes.speed.download'),
      key: 'down',
      render: (_: unknown, r: SpeedTestResult) => (failed(r) ? '—' : r.downloadMbps),
    },
    {
      title: t('pages.nodes.speed.upload'),
      key: 'up',
      render: (_: unknown, r: SpeedTestResult) => (failed(r) ? '—' : r.uploadMbps),
    },
    {
      title: t('pages.nodes.speed.setCapacity'),
      key: 'capacity',
      render: (_: unknown, r: SpeedTestResult) => r.capacityMbps || '—',
    },
  ];
  return (
    <Modal
      open={server !== null}
      onCancel={onClose}
      footer={null}
      width={960}
      title={t('pages.nodes.speed.historyTitle', { name: server?.name ?? '' })}
    >
      <Table
        rowKey="id"
        size="small"
        loading={isFetching}
        dataSource={data}
        columns={columns}
        pagination={{ pageSize: 20, hideOnSinglePage: true }}
        scroll={{ x: 'max-content' }}
        expandable={{
          rowExpandable: failed,
          expandedRowRender: (r: SpeedTestResult) => (
            <pre style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{r.error}</pre>
          ),
        }}
      />
    </Modal>
  );
}
