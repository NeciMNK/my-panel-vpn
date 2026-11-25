import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Form,
  Input,
  Modal,
  Popconfirm,
  Segmented,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';

import { selectTagRender } from '@/components/form';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { useSubBalancersQuery } from '@/api/queries/useSubBalancersQuery';
import { useSubBalancerMutations } from '@/api/queries/useSubBalancerMutations';
import type {
  SubBalancer,
  SubBalancerFormValues,
  SubBalancerStrategy,
} from '@/schemas/subBalancer';

import { foreignExitIds } from './CascadesCard';

export function isEntrylessAutoSelect(
  b: Pick<SubBalancer, 'exitForeign' | 'exitNodeIds'>,
): boolean {
  return b.exitForeign === true || (b.exitNodeIds ?? []).length > 0;
}

interface EntrylessForm {
  remark: string;
  strategy: SubBalancerStrategy;
  exitForeign: boolean;
  exitNodeIds: number[];
  enabled: boolean;
  sortOrder: number;
}

const STRATEGIES: SubBalancerStrategy[] = ['leastPing', 'leastLoad', 'random', 'roundRobin'];

function formOf(b: SubBalancer | null): EntrylessForm {
  return {
    remark: b?.remark ?? '',
    strategy: b?.strategy ?? 'leastPing',
    exitForeign: b ? b.exitForeign === true : true,
    exitNodeIds: b?.exitNodeIds ?? [],
    enabled: b?.enabled ?? true,
    sortOrder: b?.sortOrder ?? 1,
  };
}

function payloadOf(f: EntrylessForm): SubBalancerFormValues {
  return {
    remark: f.remark,
    strategy: f.strategy,
    inboundIds: [],
    sortOrder: f.sortOrder,
    enabled: f.enabled,
    exitForeign: f.exitForeign,
    exitNodeIds: f.exitForeign ? [] : f.exitNodeIds,
  };
}

interface Props {
  nodes: NodeRecord[];
}

export default function EntrylessAutoSelectList({ nodes }: Props) {
  const { t } = useTranslation();
  const { balancers, loading } = useSubBalancersQuery();
  const { create, update, remove } = useSubBalancerMutations();
  const rows = balancers.filter(isEntrylessAutoSelect);
  const [editing, setEditing] = useState<SubBalancer | null>(null);
  const [form, setForm] = useState<EntrylessForm | null>(null);
  const [saving, setSaving] = useState(false);

  const nodeName = (id: number) => nodes.find((n) => n.id === id)?.name || `#${id}`;
  const exitOptions = useMemo(
    () =>
      foreignExitIds(nodes, 0).map((id) => ({
        value: id,
        label: nodes.find((n) => n.id === id)?.name || `#${id}`,
      })),
    [nodes],
  );

  const open = (b: SubBalancer | null) => {
    setEditing(b);
    setForm(formOf(b));
  };
  const onSubmit = async () => {
    if (!form) return;
    setSaving(true);
    const msg = editing ? await update(editing.id, payloadOf(form)) : await create(payloadOf(form));
    setSaving(false);
    if (msg?.success) setForm(null);
  };

  const columns = [
    { title: t('pages.nodes.cascade.name'), dataIndex: 'remark', key: 'remark' },
    {
      title: t('pages.nodes.cascade.route'),
      key: 'route',
      render: (_: unknown, b: SubBalancer) => (
        <Space wrap size={4}>
          <Tag color="blue">{t('pages.nodes.cascade.noEntry')}</Tag>→
          {b.exitForeign ? (
            <Tag color="gold">🌍 {t('pages.nodes.cascade.exitModeForeign')}</Tag>
          ) : (
            (b.exitNodeIds ?? []).map((id) => (
              <Tag key={id} color="green">
                {nodeName(id)}
              </Tag>
            ))
          )}
          <Tag>{b.strategy}</Tag>
        </Space>
      ),
    },
    {
      title: t('pages.nodes.cascade.state'),
      key: 'state',
      render: (_: unknown, b: SubBalancer) => (
        <Switch
          size="small"
          checked={b.enabled}
          aria-label={t('pages.nodes.cascade.enable')}
          onChange={(v) => update(b.id, { ...payloadOf(formOf(b)), enabled: v })}
        />
      ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, b: SubBalancer) => (
        <Space>
          <Button
            size="small"
            icon={<EditOutlined aria-hidden />}
            aria-label={t('edit')}
            onClick={() => open(b)}
          />
          <Popconfirm title={t('pages.nodes.cascade.deleteConfirm')} onConfirm={() => remove(b.id)}>
            <Button
              size="small"
              danger
              icon={<DeleteOutlined aria-hidden />}
              aria-label={t('delete')}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      <Space
        style={{ marginTop: 16, marginBottom: 8, width: '100%', justifyContent: 'space-between' }}
      >
        <Typography.Text strong>{t('pages.nodes.cascade.noEntryTitle')}</Typography.Text>
        <Button icon={<PlusOutlined aria-hidden />} onClick={() => open(null)}>
          {t('pages.nodes.cascade.noEntryAdd')}
        </Button>
      </Space>
      <Table
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={rows}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
      <Modal
        open={form !== null}
        title={t('pages.nodes.cascade.noEntryTitle')}
        okText={t('save')}
        onOk={onSubmit}
        confirmLoading={saving}
        onCancel={() => setForm(null)}
        destroyOnHidden
      >
        {form && (
          <Form layout="vertical">
            <Typography.Paragraph type="secondary">
              {t('pages.nodes.cascade.noEntryDesc')}
            </Typography.Paragraph>
            <Form.Item label={t('pages.nodes.cascade.name')} required>
              <Input
                aria-label={t('pages.nodes.cascade.name')}
                value={form.remark}
                onChange={(e) => setForm({ ...form, remark: e.target.value })}
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.cascade.exitMode')}>
              <Segmented
                value={form.exitForeign ? 'foreign' : 'servers'}
                options={[
                  { value: 'foreign', label: t('pages.nodes.cascade.exitModeForeign') },
                  { value: 'servers', label: t('pages.nodes.cascade.exitModePickedForeign') },
                ]}
                onChange={(v) => setForm({ ...form, exitForeign: v === 'foreign' })}
              />
            </Form.Item>
            {!form.exitForeign && (
              <Form.Item label={t('pages.nodes.cascade.exits')} required>
                <Select
                  aria-label={t('pages.nodes.cascade.exits')}
                  mode="multiple"
                  tagRender={selectTagRender}
                  value={form.exitNodeIds}
                  options={exitOptions}
                  onChange={(v: number[]) => setForm({ ...form, exitNodeIds: v })}
                />
              </Form.Item>
            )}
            <Form.Item label={t('pages.nodes.cascade.strategy')}>
              <Select
                aria-label={t('pages.nodes.cascade.strategy')}
                value={form.strategy}
                options={STRATEGIES.map((s) => ({ value: s, label: s }))}
                onChange={(v: SubBalancerStrategy) => setForm({ ...form, strategy: v })}
              />
            </Form.Item>
          </Form>
        )}
      </Modal>
    </>
  );
}
