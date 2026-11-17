import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Form, Input, InputNumber, Space, Table, Typography } from 'antd';
import { DeleteOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import type { SpeedTier } from '@/generated/types';
import { SPEED_KEY, fetchSpeedOverview } from '@/pages/nodes/SpeedCard';

const EMPTY_TIER: SpeedTier = { id: 0, name: '', minMbps: 0, monthlyPrice: 0 };
const INPUT_WIDTH = 140;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export default function SpeedTiersCard() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: overview, isFetching } = useQuery({
    queryKey: SPEED_KEY,
    queryFn: fetchSpeedOverview,
  });
  const [draft, setDraft] = useState<SpeedTier>(EMPTY_TIER);
  const invalidate = () => queryClient.invalidateQueries({ queryKey: SPEED_KEY });
  const saveTier = useMutation({
    mutationFn: (req: SpeedTier) => HttpUtil.post('/panel/api/speed/tier', req, JSON_HEADERS),
    onSettled: invalidate,
  });
  const deleteTier = useMutation({
    mutationFn: (id: number) => HttpUtil.post('/panel/api/speed/tier/delete', { id }, JSON_HEADERS),
    onSettled: invalidate,
  });

  const columns = [
    { title: t('pages.ledger.speedTiers.name'), dataIndex: 'name', key: 'name' },
    { title: t('pages.nodes.speed.min'), dataIndex: 'minMbps', key: 'min' },
    { title: t('pages.ledger.speedTiers.price'), dataIndex: 'monthlyPrice', key: 'price' },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, r: SpeedTier) => (
        <Space>
          <Button size="small" onClick={() => setDraft(r)}>
            {t('edit')}
          </Button>
          <Button
            size="small"
            danger
            aria-label={`${t('delete')} ${r.name}`}
            icon={<DeleteOutlined aria-hidden />}
            onClick={() => deleteTier.mutate(r.id)}
          />
        </Space>
      ),
    },
  ];

  return (
    <Card size="small" title={t('pages.ledger.speedTiers.title')}>
      <Typography.Paragraph type="secondary">
        {t('pages.ledger.speedTiers.desc')}
      </Typography.Paragraph>
      <Form layout="inline" style={{ marginBottom: 12, rowGap: 8 }}>
        <Form.Item label={t('pages.ledger.speedTiers.name')} htmlFor="speed-tier-name">
          <Input
            id="speed-tier-name"
            style={{ width: 160 }}
            placeholder="Plus"
            value={draft.name}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          />
        </Form.Item>
        <Form.Item label={t('pages.nodes.speed.min')} htmlFor="speed-tier-min">
          <InputNumber
            id="speed-tier-min"
            style={{ width: INPUT_WIDTH }}
            min={0}
            value={draft.minMbps}
            onChange={(v) => setDraft({ ...draft, minMbps: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item label={t('pages.ledger.speedTiers.price')} htmlFor="speed-tier-price">
          <InputNumber
            id="speed-tier-price"
            style={{ width: INPUT_WIDTH }}
            min={0}
            precision={0}
            value={draft.monthlyPrice}
            onChange={(v) => setDraft({ ...draft, monthlyPrice: v ?? 0 })}
          />
        </Form.Item>
        <Form.Item>
          <Space>
            <Button
              type="primary"
              disabled={!draft.name.trim() || draft.minMbps <= 0}
              loading={saveTier.isPending}
              onClick={() =>
                saveTier.mutate(draft, {
                  onSuccess: (msg) => {
                    if (msg?.success) setDraft(EMPTY_TIER);
                  },
                })
              }
            >
              {draft.id ? t('save') : t('add')}
            </Button>
            {draft.id !== 0 && <Button onClick={() => setDraft(EMPTY_TIER)}>{t('cancel')}</Button>}
          </Space>
        </Form.Item>
      </Form>
      <Table
        rowKey="id"
        size="small"
        loading={isFetching}
        dataSource={overview?.tiers ?? []}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
}
