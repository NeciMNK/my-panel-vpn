import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Form, InputNumber, Select, Space, Table, Typography } from 'antd';
import { DeleteOutlined, TeamOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import { parseMsg } from '@/utils/zodValidate';
import { GroupSummaryListSchema } from '@/schemas/client';
import type { GroupSpeed } from '@/generated/types';

import { SPEED_KEY, fetchSpeedOverview } from './SpeedCard';

type GroupSpeedDraft = Omit<GroupSpeed, 'id'>;

const EMPTY_DRAFT: GroupSpeedDraft = {
  group: '',
  minMbps: 0,
  maxMbps: 0,
  floorMbps: 0,
};
const INPUT_WIDTH = 140;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

// A floating cap reads "50–100"; a fixed one is just its number.
function groupCapLabel(g: GroupSpeedDraft): string {
  if (g.maxMbps === 0) return '—';
  return g.floorMbps > 0 ? `${g.floorMbps}–${g.maxMbps}` : String(g.maxMbps);
}

export default function GroupSpeedCard() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: overview, isFetching } = useQuery({
    queryKey: SPEED_KEY,
    queryFn: fetchSpeedOverview,
  });
  const { data: groups = [] } = useQuery({
    queryKey: keys.clients.groups(),
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/clients/groups', undefined, {
        silent: true,
      });
      return parseMsg(msg, GroupSummaryListSchema, 'clients/groups').obj ?? [];
    },
  });
  const [draft, setDraft] = useState<GroupSpeedDraft>(EMPTY_DRAFT);
  const setGroup = useMutation({
    mutationFn: (req: GroupSpeedDraft) =>
      HttpUtil.post('/panel/api/speed/group', req, JSON_HEADERS),
    onSettled: () => queryClient.invalidateQueries({ queryKey: SPEED_KEY }),
  });

  const columns = [
    {
      title: t('pages.nodes.groupSpeed.group'),
      dataIndex: 'group',
      key: 'group',
    },
    { title: t('pages.nodes.speed.min'), dataIndex: 'minMbps', key: 'min' },
    {
      title: t('pages.nodes.speed.max'),
      key: 'max',
      render: (_: unknown, r: GroupSpeed) => groupCapLabel(r),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, r: GroupSpeed) => (
        <Space>
          <Button size="small" onClick={() => setDraft(r)}>
            {t('edit')}
          </Button>
          <Button
            size="small"
            danger
            aria-label={`${t('delete')} ${r.group}`}
            icon={<DeleteOutlined aria-hidden />}
            onClick={() => setGroup.mutate({ ...EMPTY_DRAFT, group: r.group })}
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
          <TeamOutlined aria-hidden />
          {t('pages.nodes.groupSpeed.title')}
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">
        {t('pages.nodes.groupSpeed.desc')}
      </Typography.Paragraph>
      <Form layout="inline" style={{ marginBottom: 12, rowGap: 8 }}>
        <Form.Item label={t('pages.nodes.groupSpeed.group')} htmlFor="group-speed-group">
          <Select
            id="group-speed-group"
            showSearch
            style={{ minWidth: 180 }}
            placeholder={t('pages.nodes.groupSpeed.group')}
            value={draft.group || undefined}
            options={groups.map((g) => ({ value: g.name, label: g.name }))}
            onChange={(group: string) => setDraft({ ...draft, group })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.speed.min')} htmlFor="group-speed-min">
          <InputNumber
            id="group-speed-min"
            style={{ width: INPUT_WIDTH }}
            min={0}
            value={draft.minMbps}
            onChange={(v) => setDraft({ ...draft, minMbps: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.groupSpeed.floor')} htmlFor="group-speed-floor">
          <InputNumber
            id="group-speed-floor"
            style={{ width: INPUT_WIDTH }}
            min={0}
            value={draft.floorMbps}
            onChange={(v) => setDraft({ ...draft, floorMbps: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.speed.max')} htmlFor="group-speed-max">
          <InputNumber
            id="group-speed-max"
            style={{ width: INPUT_WIDTH }}
            min={0}
            value={draft.maxMbps}
            onChange={(v) => setDraft({ ...draft, maxMbps: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item>
          <Button
            type="primary"
            disabled={!draft.group}
            loading={setGroup.isPending}
            onClick={() =>
              setGroup.mutate(draft, {
                onSuccess: (msg) => {
                  if (msg?.success) setDraft(EMPTY_DRAFT);
                },
              })
            }
          >
            {t('save')}
          </Button>
        </Form.Item>
      </Form>
      <Table
        rowKey="group"
        size="small"
        loading={isFetching}
        dataSource={overview?.groups ?? []}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
}
