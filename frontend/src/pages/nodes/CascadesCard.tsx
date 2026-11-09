import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { BranchesOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { selectTagRender } from '@/components/form';
import type { NodeRecord } from '@/api/queries/useNodesQuery';

export interface CascadeLink {
  nodeId: number;
  inboundId: number;
  address: string;
  port: number;
}

export interface CascadeRecord {
  id: number;
  name: string;
  enable: boolean;
  entryNodeId: number;
  exitNodeIds: number[];
  entryInboundTags: string[] | null;
  strategy: string;
  links: CascadeLink[] | null;
  lastError: string;
}

export interface CascadeForm {
  name: string;
  enable: boolean;
  entryNodeId: number;
  exitNodeIds: number[];
  entryInboundTags: string[];
  strategy: string;
}

const CASCADES_KEY = ['cascades', 'list'] as const;
const STRATEGIES = ['leastPing', 'leastLoad', 'random', 'roundRobin'];
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export function emptyCascadeForm(): CascadeForm {
  return {
    name: '',
    enable: true,
    entryNodeId: 0,
    exitNodeIds: [],
    entryInboundTags: [],
    strategy: 'leastPing',
  };
}

// The exit picker never offers the entry: a server cannot chain to itself.
export function exitOptions<T extends { value: number }>(servers: T[], entryNodeId: number): T[] {
  return servers.filter((s) => s.value !== entryNodeId);
}

async function fetchCascades(): Promise<CascadeRecord[]> {
  const msg = await HttpUtil.get<CascadeRecord[]>('/panel/api/cascades/list', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch cascades');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

interface CascadesCardProps {
  nodes: NodeRecord[];
}

export default function CascadesCard({ nodes }: CascadesCardProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: cascades = [], isFetching } = useQuery({
    queryKey: CASCADES_KEY,
    queryFn: fetchCascades,
  });
  const [editing, setEditing] = useState<CascadeRecord | null>(null);
  const [form, setForm] = useState<CascadeForm | null>(null);

  const servers = useMemo(
    () => [
      { value: 0, label: t('pages.xray.targetLocal') },
      ...nodes
        .filter((n) => !n.transitive)
        .map((n) => ({ value: n.id, label: n.name || `#${n.id}` })),
    ],
    [nodes, t],
  );
  const serverName = (id: number) => servers.find((s) => s.value === id)?.label ?? `#${id}`;

  const save = useMutation({
    mutationFn: (payload: { id?: number; form: CascadeForm }) =>
      HttpUtil.post<CascadeRecord>(
        payload.id ? `/panel/api/cascades/update/${payload.id}` : '/panel/api/cascades/add',
        payload.form,
        JSON_HEADERS,
      ),
    onSettled: () => queryClient.invalidateQueries({ queryKey: CASCADES_KEY }),
  });
  const remove = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/cascades/del/${id}`),
    onSettled: () => queryClient.invalidateQueries({ queryKey: CASCADES_KEY }),
  });

  const openNew = () => {
    setEditing(null);
    setForm(emptyCascadeForm());
  };
  const openEdit = (c: CascadeRecord) => {
    setEditing(c);
    setForm({
      name: c.name,
      enable: c.enable,
      entryNodeId: c.entryNodeId,
      exitNodeIds: c.exitNodeIds ?? [],
      entryInboundTags: c.entryInboundTags ?? [],
      strategy: c.strategy || 'leastPing',
    });
  };
  const onSubmit = async () => {
    if (!form) return;
    const msg = await save.mutateAsync({ id: editing?.id, form });
    if (msg?.success) setForm(null);
  };
  const toggle = (c: CascadeRecord, enable: boolean) =>
    save.mutate({
      id: c.id,
      form: {
        name: c.name,
        enable,
        entryNodeId: c.entryNodeId,
        exitNodeIds: c.exitNodeIds ?? [],
        entryInboundTags: c.entryInboundTags ?? [],
        strategy: c.strategy,
      },
    });

  const columns = [
    { title: t('pages.nodes.cascade.name'), dataIndex: 'name', key: 'name' },
    {
      title: t('pages.nodes.cascade.route'),
      key: 'route',
      render: (_: unknown, c: CascadeRecord) => (
        <Space wrap size={4}>
          <Tag color="blue">{serverName(c.entryNodeId)}</Tag>→
          {(c.exitNodeIds ?? []).map((id) => (
            <Tag key={id} color="green">
              {serverName(id)}
            </Tag>
          ))}
          {(c.exitNodeIds ?? []).length > 1 && <Tag>{c.strategy}</Tag>}
        </Space>
      ),
    },
    {
      title: t('pages.nodes.cascade.state'),
      key: 'state',
      render: (_: unknown, c: CascadeRecord) =>
        c.lastError ? (
          <Typography.Text type="danger">{c.lastError}</Typography.Text>
        ) : (
          <Switch
            size="small"
            checked={c.enable}
            aria-label={t('pages.nodes.cascade.enable')}
            onChange={(v) => toggle(c, v)}
          />
        ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, c: CascadeRecord) => (
        <Space>
          <Button
            size="small"
            icon={<EditOutlined aria-hidden />}
            aria-label={t('edit')}
            onClick={() => openEdit(c)}
          />
          <Popconfirm
            title={t('pages.nodes.cascade.deleteConfirm')}
            onConfirm={() => remove.mutate(c.id)}
          >
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

  const exits = form ? exitOptions(servers, form.entryNodeId) : [];

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <BranchesOutlined aria-hidden />
          {t('pages.nodes.cascade.title')}
        </Space>
      }
      extra={
        <Button type="primary" icon={<PlusOutlined aria-hidden />} onClick={openNew}>
          {t('pages.nodes.cascade.add')}
        </Button>
      }
    >
      <Table
        rowKey="id"
        size="small"
        loading={isFetching}
        dataSource={cascades}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />

      <Modal
        open={form !== null}
        title={editing ? t('pages.nodes.cascade.edit') : t('pages.nodes.cascade.add')}
        okText={t('save')}
        onOk={onSubmit}
        confirmLoading={save.isPending}
        onCancel={() => setForm(null)}
        destroyOnHidden
      >
        {form && (
          <Form layout="vertical">
            <Typography.Paragraph type="secondary">
              {t('pages.nodes.cascade.desc')}
            </Typography.Paragraph>
            <Form.Item label={t('pages.nodes.cascade.entry')} required>
              <Select
                aria-label={t('pages.nodes.cascade.entry')}
                value={form.entryNodeId}
                options={servers}
                onChange={(v: number) =>
                  setForm({
                    ...form,
                    entryNodeId: v,
                    exitNodeIds: form.exitNodeIds.filter((id) => id !== v),
                  })
                }
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.cascade.exits')} required>
              <Select
                aria-label={t('pages.nodes.cascade.exits')}
                mode="multiple"
                tagRender={selectTagRender}
                value={form.exitNodeIds}
                options={exits}
                onChange={(v: number[]) => setForm({ ...form, exitNodeIds: v })}
              />
            </Form.Item>
            {form.exitNodeIds.length > 1 && (
              <Form.Item label={t('pages.nodes.cascade.strategy')}>
                <Select
                  aria-label={t('pages.nodes.cascade.strategy')}
                  value={form.strategy}
                  options={STRATEGIES.map((s) => ({ value: s, label: s }))}
                  onChange={(v: string) => setForm({ ...form, strategy: v })}
                />
              </Form.Item>
            )}
            <Form.Item label={t('pages.nodes.cascade.name')}>
              <Input
                aria-label={t('pages.nodes.cascade.name')}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Form.Item>
            <Form.Item
              label={t('pages.nodes.cascade.inbounds')}
              extra={t('pages.nodes.cascade.inboundsHint')}
            >
              <Select
                aria-label={t('pages.nodes.cascade.inbounds')}
                mode="tags"
                tagRender={selectTagRender}
                value={form.entryInboundTags}
                onChange={(v: string[]) => setForm({ ...form, entryInboundTags: v })}
              />
            </Form.Item>
            {editing?.lastError && <Alert type="error" showIcon title={editing.lastError} />}
          </Form>
        )}
      </Modal>
    </Card>
  );
}
