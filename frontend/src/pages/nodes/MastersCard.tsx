import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Space, Table, Tag, Typography } from 'antd';
import { ClusterOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';

export interface MasterStatusRecord {
  name: string;
  priority: number;
  url: string;
  alive: boolean;
  leader: boolean;
  self: boolean;
  lastSeen: number;
}

export type MasterState = 'leader' | 'follower' | 'down';

export function masterState(m: MasterStatusRecord): MasterState {
  if (m.leader) return 'leader';
  return m.alive ? 'follower' : 'down';
}

const MASTERS_KEY = ['masters', 'list'] as const;
const STATE_COLOR: Record<MasterState, string> = {
  leader: 'gold',
  follower: 'green',
  down: 'red',
};

async function fetchMasters(): Promise<MasterStatusRecord[]> {
  const msg = await HttpUtil.get<MasterStatusRecord[]>('/panel/api/masters/list', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch masters');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

// Shown only once XUI_MASTER_PRIORITY made this panel one of several masters.
export default function MastersCard() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: masters = [] } = useQuery({
    queryKey: MASTERS_KEY,
    queryFn: fetchMasters,
    refetchInterval: 10_000,
  });
  const forget = useMutation({
    mutationFn: (name: string) =>
      HttpUtil.post(
        '/panel/api/masters/forget',
        { name },
        {
          headers: { 'Content-Type': 'application/json' },
        },
      ),
    onSettled: () => queryClient.invalidateQueries({ queryKey: MASTERS_KEY }),
  });
  if (masters.length === 0) return null;

  const columns = [
    {
      title: t('pages.nodes.masters.name'),
      key: 'name',
      render: (_: unknown, m: MasterStatusRecord) => (
        <Space>
          {m.name}
          {m.self && (
            <Typography.Text type="secondary">({t('pages.nodes.masters.self')})</Typography.Text>
          )}
        </Space>
      ),
    },
    { title: t('pages.nodes.masters.priority'), dataIndex: 'priority', key: 'priority' },
    { title: t('pages.nodes.masters.address'), dataIndex: 'url', key: 'url' },
    {
      title: t('pages.nodes.masters.state'),
      key: 'state',
      render: (_: unknown, m: MasterStatusRecord) => {
        const state = masterState(m);
        return <Tag color={STATE_COLOR[state]}>{t(`pages.nodes.masters.states.${state}`)}</Tag>;
      },
    },
    {
      title: t('pages.nodes.masters.lastSeen'),
      key: 'lastSeen',
      render: (_: unknown, m: MasterStatusRecord) => new Date(m.lastSeen * 1000).toLocaleString(),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, m: MasterStatusRecord) =>
        masterState(m) === 'down' && (
          <Button
            size="small"
            danger
            loading={forget.isPending}
            onClick={() => forget.mutate(m.name)}
          >
            {t('pages.nodes.masters.forget')}
          </Button>
        ),
    },
  ];

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <ClusterOutlined aria-hidden />
          {t('pages.nodes.masters.title')}
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">{t('pages.nodes.masters.desc')}</Typography.Paragraph>
      <Table
        rowKey="name"
        size="small"
        dataSource={masters}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
}
