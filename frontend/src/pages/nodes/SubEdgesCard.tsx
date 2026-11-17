import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Form,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { CloudServerOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { useTlsCertsQuery } from '@/api/queries/useTlsCerts';
import type { TlsCertificate } from '@/schemas/tlsCert';
import type { SubEdge, SubEdgeRequest } from '@/generated/types';

const SUB_EDGES_KEY = ['subEdges', 'list'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export function emptySubEdgeForm(): SubEdgeRequest {
  return { nodeId: 0, enable: true, port: 443, certId: 0 };
}

export function subEdgeFormOf(e: SubEdge): SubEdgeRequest {
  return { nodeId: e.nodeId, enable: e.enable, port: e.port, certId: e.certId };
}

// The panel pushes every certificate to every node; until it lands the edge cannot serve it.
export function certMissingOnNode(cert: TlsCertificate | undefined, nodeId: number): boolean {
  if (!cert || nodeId === 0) return false;
  return !cert.nodeInstalls?.[nodeId]?.certFile;
}

async function fetchSubEdges(): Promise<SubEdge[]> {
  const msg = await HttpUtil.get<SubEdge[]>('/panel/api/subEdges/list', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch subscription edges');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

interface SubEdgesCardProps {
  nodes: NodeRecord[];
}

export default function SubEdgesCard({ nodes }: SubEdgesCardProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: edges = [], isFetching } = useQuery({
    queryKey: SUB_EDGES_KEY,
    queryFn: fetchSubEdges,
    refetchInterval: 15000,
  });
  const { data: certs = [] } = useTlsCertsQuery();
  const [editing, setEditing] = useState<SubEdge | null>(null);
  const [form, setForm] = useState<SubEdgeRequest | null>(null);

  const usedNodes = new Set(edges.filter((e) => e.id !== editing?.id).map((e) => e.nodeId));
  const nodeOptions = nodes
    .filter((n) => !n.transitive && !usedNodes.has(n.id))
    .map((n) => ({
      value: n.id,
      label: `${n.name || `#${n.id}`}${n.country ? ` (${n.country})` : ''}`,
    }));
  const nodeName = (id: number) => nodes.find((n) => n.id === id)?.name || `#${id}`;
  const certOptions = [
    { value: 0, label: t('pages.nodes.subEdge.certNone') },
    ...certs.map((c) => ({ value: c.id, label: c.domains.join(', ') })),
  ];
  const certName = (id: number) =>
    id === 0
      ? t('pages.nodes.subEdge.certNone')
      : (certs.find((c) => c.id === id)?.domains.join(', ') ?? `#${id}`);

  const save = useMutation({
    mutationFn: (payload: { id?: number; form: SubEdgeRequest }) =>
      HttpUtil.post<SubEdge>(
        payload.id ? `/panel/api/subEdges/update/${payload.id}` : '/panel/api/subEdges/add',
        payload.form,
        JSON_HEADERS,
      ),
    onSettled: () => queryClient.invalidateQueries({ queryKey: SUB_EDGES_KEY }),
  });
  const remove = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/subEdges/del/${id}`),
    onSettled: () => queryClient.invalidateQueries({ queryKey: SUB_EDGES_KEY }),
  });

  const openNew = () => {
    setEditing(null);
    setForm(emptySubEdgeForm());
  };
  const openEdit = (e: SubEdge) => {
    setEditing(e);
    setForm(subEdgeFormOf(e));
  };
  const onSubmit = async () => {
    if (!form) return;
    const msg = await save.mutateAsync({ id: editing?.id, form });
    if (msg?.success) setForm(null);
  };

  const columns = [
    {
      title: t('pages.nodes.subEdge.node'),
      key: 'node',
      render: (_: unknown, e: SubEdge) => nodeName(e.nodeId),
    },
    { title: t('pages.nodes.subEdge.port'), dataIndex: 'port', key: 'port' },
    {
      title: t('pages.nodes.subEdge.cert'),
      key: 'cert',
      render: (_: unknown, e: SubEdge) => certName(e.certId),
    },
    {
      title: t('pages.nodes.subEdge.tunnel'),
      key: 'tunnel',
      render: (_: unknown, e: SubEdge) =>
        !e.enable ? (
          <Tag>{t('disabled')}</Tag>
        ) : e.status === 'online' ? (
          <Tag color="green">{t('pages.nodes.subEdge.online')}</Tag>
        ) : (
          <Tooltip title={e.lastError}>
            <Tag color="red">{t('pages.nodes.subEdge.offline')}</Tag>
          </Tooltip>
        ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, e: SubEdge) => (
        <Space>
          <Switch
            size="small"
            checked={e.enable}
            aria-label={t('pages.nodes.subEdge.enable')}
            onChange={(enable) => save.mutate({ id: e.id, form: { ...subEdgeFormOf(e), enable } })}
          />
          <Button
            size="small"
            icon={<EditOutlined aria-hidden />}
            aria-label={t('edit')}
            onClick={() => openEdit(e)}
          />
          <Popconfirm
            title={t('pages.nodes.subEdge.deleteConfirm')}
            onConfirm={() => remove.mutate(e.id)}
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

  const pickedCert = form ? certs.find((c) => c.id === form.certId) : undefined;

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <CloudServerOutlined aria-hidden />
          {t('pages.nodes.subEdge.title')}
        </Space>
      }
      extra={
        <Button type="primary" icon={<PlusOutlined aria-hidden />} onClick={openNew}>
          {t('pages.nodes.subEdge.add')}
        </Button>
      }
    >
      <Table
        rowKey="id"
        size="small"
        loading={isFetching && edges.length === 0}
        dataSource={edges}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />

      <Modal
        open={form !== null}
        title={editing ? t('pages.nodes.subEdge.edit') : t('pages.nodes.subEdge.add')}
        okText={t('save')}
        onOk={onSubmit}
        okButtonProps={{ disabled: !form || form.nodeId === 0 }}
        confirmLoading={save.isPending}
        onCancel={() => setForm(null)}
        destroyOnHidden
      >
        {form && (
          <Form layout="vertical">
            <Typography.Paragraph type="secondary">
              {t('pages.nodes.subEdge.desc')}
            </Typography.Paragraph>
            <Form.Item label={t('pages.nodes.subEdge.node')} required>
              <Select
                aria-label={t('pages.nodes.subEdge.node')}
                value={form.nodeId || undefined}
                options={nodeOptions}
                onChange={(nodeId: number) => setForm({ ...form, nodeId })}
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.subEdge.port')} required>
              <InputNumber
                aria-label={t('pages.nodes.subEdge.port')}
                min={1}
                max={65535}
                value={form.port}
                onChange={(port) => setForm({ ...form, port: port ?? 443 })}
              />
            </Form.Item>
            <Form.Item
              label={t('pages.nodes.subEdge.cert')}
              validateStatus={certMissingOnNode(pickedCert, form.nodeId) ? 'warning' : undefined}
              help={
                certMissingOnNode(pickedCert, form.nodeId)
                  ? t('pages.nodes.subEdge.certNotOnNode')
                  : undefined
              }
            >
              <Select
                aria-label={t('pages.nodes.subEdge.cert')}
                value={form.certId}
                options={certOptions}
                onChange={(certId: number) => setForm({ ...form, certId })}
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.subEdge.enable')}>
              <Switch
                checked={form.enable}
                aria-label={t('pages.nodes.subEdge.enable')}
                onChange={(enable) => setForm({ ...form, enable })}
              />
            </Form.Item>
            <Alert type="info" showIcon message={t('pages.nodes.subEdge.dnsHint')} />
          </Form>
        )}
      </Modal>
    </Card>
  );
}
