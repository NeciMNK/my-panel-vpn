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
  Segmented,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import {
  BranchesOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

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
  exitCountry: string;
  entryInboundTags: string[] | null;
  strategy: string;
  mode?: string;
  directDomains?: string[] | null;
  directIps?: string[] | null;
  relayInboundTags?: string[] | null;
  autoSelect?: boolean;
  links: CascadeLink[] | null;
  lastError: string;
}

export type CascadeMode = 'split' | 'relay' | 'both';

export interface CascadeForm {
  name: string;
  enable: boolean;
  entryNodeId: number;
  exitNodeIds: number[];
  exitCountry: string;
  entryInboundTags: string[];
  strategy: string;
  mode: CascadeMode;
  directDomains: string[];
  directIps: string[];
  relayInboundTags: string[];
  autoSelect: boolean;
}

// Matches CascadeExitForeign in Go: every node outside the entry's country is an exit.
export const EXIT_FOREIGN = 'FOREIGN';

const CASCADES_KEY = ['cascades', 'list'] as const;
const STRATEGIES = ['leastPing', 'leastLoad', 'random', 'roundRobin'];
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export function emptyCascadeForm(autoSelect = false): CascadeForm {
  return {
    name: '',
    enable: true,
    entryNodeId: 0,
    exitNodeIds: [],
    exitCountry: autoSelect ? EXIT_FOREIGN : '',
    entryInboundTags: [],
    strategy: 'leastPing',
    mode: 'split',
    directDomains: [],
    directIps: [],
    relayInboundTags: [],
    autoSelect,
  };
}

// A cascade saved before modes existed relayed everything.
export function cascadeMode(mode: string | undefined): CascadeMode {
  return mode === 'split' || mode === 'both' ? mode : 'relay';
}

// Flips one of the two switches; turning off the last one is refused.
export function toggleCascadeMode(
  mode: CascadeMode,
  which: 'split' | 'relay',
  on: boolean,
): CascadeMode {
  const split = which === 'split' ? on : mode !== 'relay';
  const relay = which === 'relay' ? on : mode !== 'split';
  if (split && relay) return 'both';
  if (split) return 'split';
  if (relay) return 'relay';
  return mode;
}

export function cascadeFormOf(c: CascadeRecord): CascadeForm {
  return {
    name: c.name,
    enable: c.enable,
    entryNodeId: c.entryNodeId,
    exitNodeIds: c.exitNodeIds ?? [],
    exitCountry: c.exitCountry ?? '',
    entryInboundTags: c.entryInboundTags ?? [],
    strategy: c.strategy || 'leastPing',
    mode: cascadeMode(c.mode),
    directDomains: c.directDomains ?? [],
    directIps: c.directIps ?? [],
    relayInboundTags: c.relayInboundTags ?? [],
    autoSelect: c.autoSelect ?? false,
  };
}

// Countries the exit picker offers: every code set on a (non-transitive) node.
export function nodeCountries(nodes: { country?: string; transitive?: boolean }[]): string[] {
  const set = new Set<string>();
  for (const n of nodes) if (!n.transitive && n.country) set.add(n.country.toUpperCase());
  return [...set].sort();
}

// "🇩🇪" from "DE": regional indicator symbols, so no flag images are shipped.
export function countryFlag(code: string): string {
  if (!/^[A-Za-z]{2}$/.test(code)) return '';
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)));
}

// The exit picker never offers the entry: a server cannot chain to itself.
export function exitOptions<T extends { value: number }>(servers: T[], entryNodeId: number): T[] {
  return servers.filter((s) => s.value !== entryNodeId);
}

// Auto-select exits: nodes with a country other than the entry's (the panel counts as RU).
export function foreignExitIds(
  nodes: { id: number; country?: string; transitive?: boolean }[],
  entryNodeId: number,
): number[] {
  const country = (n?: { country?: string }) => (n?.country ?? '').trim().toUpperCase();
  const home = country(nodes.find((n) => n.id === entryNodeId)) || 'RU';
  return nodes
    .filter(
      (n) => !n.transitive && n.id !== entryNodeId && country(n) !== '' && country(n) !== home,
    )
    .map((n) => n.id);
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
  autoSelect?: boolean;
}

export default function CascadesCard({ nodes, autoSelect = false }: CascadesCardProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: allCascades = [], isFetching } = useQuery({
    queryKey: CASCADES_KEY,
    queryFn: fetchCascades,
  });
  const cascades = allCascades.filter((c) => (c.autoSelect ?? false) === autoSelect);
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
    setForm(emptyCascadeForm(autoSelect));
  };
  const openEdit = (c: CascadeRecord) => {
    setEditing(c);
    setForm(cascadeFormOf(c));
  };
  const onSubmit = async () => {
    if (!form) return;
    const msg = await save.mutateAsync({ id: editing?.id, form });
    if (msg?.success) setForm(null);
  };
  const toggle = (c: CascadeRecord, enable: boolean) =>
    save.mutate({ id: c.id, form: { ...cascadeFormOf(c), enable } });

  const columns = [
    { title: t('pages.nodes.cascade.name'), dataIndex: 'name', key: 'name' },
    {
      title: t('pages.nodes.cascade.route'),
      key: 'route',
      render: (_: unknown, c: CascadeRecord) => (
        <Space wrap size={4}>
          <Tag color="blue">{serverName(c.entryNodeId)}</Tag>→
          {c.exitCountry && (
            <Tag color="gold">
              {c.exitCountry === EXIT_FOREIGN
                ? `🌍 ${t('pages.nodes.cascade.exitModeForeign')}`
                : `${countryFlag(c.exitCountry)} ${c.exitCountry}`}{' '}
              ({(c.exitNodeIds ?? []).length})
            </Tag>
          )}
          {(c.exitNodeIds ?? []).map((id) => (
            <Tag key={id} color="green">
              {serverName(id)}
            </Tag>
          ))}
          {(c.exitNodeIds ?? []).length > 1 && <Tag>{c.strategy}</Tag>}
          <Tag color={cascadeMode(c.mode) === 'relay' ? 'default' : 'purple'}>
            {t(`pages.nodes.cascade.modes.${cascadeMode(c.mode)}`)}
          </Tag>
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

  const foreignIds = form ? foreignExitIds(nodes, form.entryNodeId) : [];
  const exits = !form
    ? []
    : autoSelect
      ? servers.filter((s) => foreignIds.includes(s.value))
      : exitOptions(servers, form.entryNodeId);
  const countries = nodeCountries(nodes);
  const foreign = form !== null && form.exitCountry === EXIT_FOREIGN;
  const byCountry = form !== null && form.exitCountry !== '' && !foreign;

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          {autoSelect ? <ThunderboltOutlined aria-hidden /> : <BranchesOutlined aria-hidden />}
          {autoSelect ? t('pages.nodes.cascade.autoTitle') : t('pages.nodes.cascade.title')}
        </Space>
      }
      extra={
        <Button type="primary" icon={<PlusOutlined aria-hidden />} onClick={openNew}>
          {autoSelect ? t('pages.nodes.cascade.autoAdd') : t('pages.nodes.cascade.add')}
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
        title={
          autoSelect
            ? editing
              ? t('pages.nodes.cascade.autoEdit')
              : t('pages.nodes.cascade.autoAdd')
            : editing
              ? t('pages.nodes.cascade.edit')
              : t('pages.nodes.cascade.add')
        }
        okText={t('save')}
        onOk={onSubmit}
        confirmLoading={save.isPending}
        onCancel={() => setForm(null)}
        destroyOnHidden
      >
        {form && (
          <Form layout="vertical">
            <Typography.Paragraph type="secondary">
              {autoSelect ? t('pages.nodes.cascade.autoDesc') : t('pages.nodes.cascade.desc')}
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
                    exitNodeIds: form.exitNodeIds.filter(
                      (id) => id !== v && (!autoSelect || foreignExitIds(nodes, v).includes(id)),
                    ),
                  })
                }
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.cascade.exitMode')}>
              <Segmented
                value={foreign ? 'foreign' : byCountry ? 'country' : 'servers'}
                options={
                  autoSelect
                    ? [
                        { value: 'foreign', label: t('pages.nodes.cascade.exitModeForeign') },
                        { value: 'servers', label: t('pages.nodes.cascade.exitModePickedForeign') },
                      ]
                    : [
                        { value: 'servers', label: t('pages.nodes.cascade.exitModeServers') },
                        {
                          value: 'country',
                          label: t('pages.nodes.cascade.exitModeCountry'),
                          disabled: countries.length === 0,
                        },
                      ]
                }
                onChange={(v) =>
                  setForm({
                    ...form,
                    exitCountry:
                      v === 'country' ? (countries[0] ?? '') : v === 'foreign' ? EXIT_FOREIGN : '',
                  })
                }
              />
            </Form.Item>
            {foreign ? (
              <Typography.Paragraph type="secondary">
                {t('pages.nodes.cascade.exitModeForeignHint')}
              </Typography.Paragraph>
            ) : byCountry ? (
              <Form.Item
                label={t('pages.nodes.cascade.country')}
                extra={t('pages.nodes.cascade.countryHint')}
                required
              >
                <Select
                  aria-label={t('pages.nodes.cascade.country')}
                  value={form.exitCountry}
                  options={countries.map((c) => ({
                    value: c,
                    label: `${countryFlag(c)} ${c}`,
                  }))}
                  onChange={(v: string) => setForm({ ...form, exitCountry: v })}
                />
              </Form.Item>
            ) : (
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
            )}
            {(byCountry || foreign || form.exitNodeIds.length > 1) && (
              <Form.Item label={t('pages.nodes.cascade.strategy')}>
                <Select
                  aria-label={t('pages.nodes.cascade.strategy')}
                  value={form.strategy}
                  options={STRATEGIES.map((s) => ({ value: s, label: s }))}
                  onChange={(v: string) => setForm({ ...form, strategy: v })}
                />
              </Form.Item>
            )}
            <Form.Item
              label={t('pages.nodes.cascade.split')}
              extra={t('pages.nodes.cascade.splitHint')}
            >
              <Switch
                aria-label={t('pages.nodes.cascade.split')}
                checked={form.mode !== 'relay'}
                disabled={form.mode === 'split'}
                onChange={(v) =>
                  setForm({
                    ...form,
                    mode: toggleCascadeMode(form.mode, 'split', v),
                  })
                }
              />
            </Form.Item>
            <Form.Item
              label={t('pages.nodes.cascade.relay')}
              extra={t('pages.nodes.cascade.relayHint')}
            >
              <Switch
                aria-label={t('pages.nodes.cascade.relay')}
                checked={form.mode !== 'split'}
                disabled={form.mode === 'relay'}
                onChange={(v) =>
                  setForm({
                    ...form,
                    mode: toggleCascadeMode(form.mode, 'relay', v),
                  })
                }
              />
            </Form.Item>
            {form.mode === 'both' && (
              <Form.Item
                label={t('pages.nodes.cascade.relayInbounds')}
                extra={t('pages.nodes.cascade.relayInboundsHint')}
                required
              >
                <Select
                  aria-label={t('pages.nodes.cascade.relayInbounds')}
                  mode="tags"
                  tagRender={selectTagRender}
                  value={form.relayInboundTags}
                  onChange={(v: string[]) => setForm({ ...form, relayInboundTags: v })}
                />
              </Form.Item>
            )}
            {form.mode !== 'relay' && (
              <>
                <Form.Item
                  label={t('pages.nodes.cascade.directDomains')}
                  extra={t('pages.nodes.cascade.directDomainsHint')}
                >
                  <Select
                    aria-label={t('pages.nodes.cascade.directDomains')}
                    mode="tags"
                    tagRender={selectTagRender}
                    value={form.directDomains}
                    onChange={(v: string[]) => setForm({ ...form, directDomains: v })}
                  />
                </Form.Item>
                <Form.Item
                  label={t('pages.nodes.cascade.directIps')}
                  extra={t('pages.nodes.cascade.directIpsHint')}
                >
                  <Select
                    aria-label={t('pages.nodes.cascade.directIps')}
                    mode="tags"
                    tagRender={selectTagRender}
                    value={form.directIps}
                    onChange={(v: string[]) => setForm({ ...form, directIps: v })}
                  />
                </Form.Item>
              </>
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
              validateStatus={form.entryInboundTags.length === 0 ? 'warning' : undefined}
              help={
                form.entryInboundTags.length === 0
                  ? t('pages.nodes.cascade.inboundsAllWarning')
                  : undefined
              }
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
