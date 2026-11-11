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
import { DeleteOutlined, EditOutlined, HomeOutlined, PlusOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { selectTagRender } from '@/components/form';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { useClientGroupOptions, useClientOptions } from '@/api/queries/useClientOptions';

export type HomeKind = 'wireguard' | 'openvpn' | 'l2tp' | 'ikev2';

export interface HomeAccessRule {
  emails: string[];
  groups: string[];
  targets: string[];
}

export interface HomeNetworkForm {
  name: string;
  enable: boolean;
  kind: HomeKind;
  gatewayNodeId: number;
  config: string;
  server: string;
  username: string;
  password: string;
  psk: string;
  subnets: string[];
  hosts: string[];
  rules: HomeAccessRule[];
}

export interface HomeNetworkRecord extends HomeNetworkForm {
  id: number;
  tunnelState: string;
  errors: Record<string, string> | null;
}

const HOME_KEY = ['homeNetworks', 'list'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export function emptyHomeForm(): HomeNetworkForm {
  return {
    name: '',
    enable: true,
    kind: 'wireguard',
    gatewayNodeId: 0,
    config: '',
    server: '',
    username: '',
    password: '',
    psk: '',
    subnets: [],
    hosts: [],
    rules: [{ emails: [], groups: [], targets: [] }],
  };
}

export function homeFormOf(h: HomeNetworkRecord): HomeNetworkForm {
  return {
    name: h.name,
    enable: h.enable,
    kind: h.kind,
    gatewayNodeId: h.gatewayNodeId,
    config: h.config ?? '',
    server: h.server ?? '',
    username: h.username ?? '',
    password: h.password ?? '',
    psk: h.psk ?? '',
    subnets: h.subnets ?? [],
    hosts: h.hosts ?? [],
    rules: (h.rules ?? []).map((r) => ({
      emails: r.emails ?? [],
      groups: r.groups ?? [],
      targets: r.targets ?? [],
    })),
  };
}

// A rule may only narrow to addresses the network still has; dropping a subnet
// from the network drops it from every rule too.
export function pruneRuleTargets(form: HomeNetworkForm): HomeNetworkForm {
  const known = new Set([...form.subnets, ...form.hosts]);
  return {
    ...form,
    rules: form.rules.map((r) => ({ ...r, targets: r.targets.filter((t) => known.has(t)) })),
  };
}

async function fetchHomeNetworks(): Promise<HomeNetworkRecord[]> {
  const msg = await HttpUtil.get<HomeNetworkRecord[]>('/panel/api/homeNetworks/list', undefined, {
    silent: true,
  });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch home networks');
  return Array.isArray(msg.obj) ? msg.obj : [];
}

interface HomeNetworksCardProps {
  nodes: NodeRecord[];
}

export default function HomeNetworksCard({ nodes }: HomeNetworksCardProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: homes = [], isFetching } = useQuery({
    queryKey: HOME_KEY,
    queryFn: fetchHomeNetworks,
  });
  const [editing, setEditing] = useState<HomeNetworkRecord | null>(null);
  const [form, setForm] = useState<HomeNetworkForm | null>(null);
  const { data: emails = [] } = useClientOptions(form !== null);
  const { data: groups = [] } = useClientGroupOptions(form !== null);

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
    mutationFn: (payload: { id?: number; form: HomeNetworkForm }) =>
      HttpUtil.post<HomeNetworkRecord>(
        payload.id ? `/panel/api/homeNetworks/update/${payload.id}` : '/panel/api/homeNetworks/add',
        payload.form,
        JSON_HEADERS,
      ),
    onSettled: () => queryClient.invalidateQueries({ queryKey: HOME_KEY }),
  });
  const remove = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/homeNetworks/del/${id}`),
    onSettled: () => queryClient.invalidateQueries({ queryKey: HOME_KEY }),
  });

  const onSubmit = async () => {
    if (!form) return;
    const msg = await save.mutateAsync({ id: editing?.id, form });
    if (msg?.success) setForm(null);
  };
  const setRule = (i: number, patch: Partial<HomeAccessRule>) => {
    if (!form) return;
    setForm({
      ...form,
      rules: form.rules.map((r, j) => (j === i ? { ...r, ...patch } : r)),
    });
  };

  const columns = [
    { title: t('pages.nodes.home.name'), dataIndex: 'name', key: 'name' },
    {
      title: t('pages.nodes.home.network'),
      key: 'network',
      render: (_: unknown, h: HomeNetworkRecord) => (
        <Space wrap size={4}>
          <Tag color="purple">{t(`pages.nodes.home.kinds.${h.kind}`)}</Tag>
          <Tag color="blue">{serverName(h.gatewayNodeId)}</Tag>
          {[...(h.subnets ?? []), ...(h.hosts ?? [])].map((a) => (
            <Tag key={a}>{a}</Tag>
          ))}
        </Space>
      ),
    },
    {
      title: t('pages.nodes.home.rules'),
      key: 'rules',
      render: (_: unknown, h: HomeNetworkRecord) => (h.rules ?? []).length,
    },
    {
      title: t('pages.nodes.home.state'),
      key: 'state',
      render: (_: unknown, h: HomeNetworkRecord) => {
        const errors = Object.entries(h.errors ?? {});
        return (
          <Space direction="vertical" size={0}>
            <Switch
              size="small"
              checked={h.enable}
              aria-label={t('pages.nodes.home.enable')}
              onChange={(v) => save.mutate({ id: h.id, form: { ...homeFormOf(h), enable: v } })}
            />
            {h.tunnelState && <Typography.Text type="secondary">{h.tunnelState}</Typography.Text>}
            {errors.map(([where, msg]) => (
              <Typography.Text key={where} type="danger">
                {where}: {msg}
              </Typography.Text>
            ))}
          </Space>
        );
      },
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, h: HomeNetworkRecord) => (
        <Space>
          <Button
            size="small"
            icon={<EditOutlined aria-hidden />}
            aria-label={t('edit')}
            onClick={() => {
              setEditing(h);
              setForm(homeFormOf(h));
            }}
          />
          <Popconfirm
            title={t('pages.nodes.home.deleteConfirm')}
            onConfirm={() => remove.mutate(h.id)}
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

  const targets = form ? [...form.subnets, ...form.hosts] : [];
  const ikev2 = form?.kind === 'ikev2';
  const serverLabel = ikev2 ? 'pages.nodes.home.serverIkev2' : 'pages.nodes.home.server';
  const pskLabel = ikev2 ? 'pages.nodes.home.pskIkev2' : 'pages.nodes.home.psk';

  return (
    <Card
      size="small"
      hoverable
      title={
        <Space>
          <HomeOutlined aria-hidden />
          {t('pages.nodes.home.title')}
        </Space>
      }
      extra={
        <Button
          type="primary"
          icon={<PlusOutlined aria-hidden />}
          onClick={() => {
            setEditing(null);
            setForm(emptyHomeForm());
          }}
        >
          {t('pages.nodes.home.add')}
        </Button>
      }
    >
      <Table
        rowKey="id"
        size="small"
        loading={isFetching}
        dataSource={homes}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />

      <Modal
        open={form !== null}
        title={editing ? t('pages.nodes.home.edit') : t('pages.nodes.home.add')}
        okText={t('save')}
        onOk={onSubmit}
        confirmLoading={save.isPending}
        onCancel={() => setForm(null)}
        width={720}
        destroyOnHidden
      >
        {form && (
          <Form layout="vertical">
            <Typography.Paragraph type="secondary">
              {t('pages.nodes.home.desc')}
            </Typography.Paragraph>
            <Form.Item label={t('pages.nodes.home.name')}>
              <Input
                aria-label={t('pages.nodes.home.name')}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.home.kind')}>
              <Segmented
                value={form.kind}
                options={(['wireguard', 'openvpn', 'l2tp', 'ikev2'] as const).map((k) => ({
                  value: k,
                  label: t(`pages.nodes.home.kinds.${k}`),
                }))}
                onChange={(v) => setForm({ ...form, kind: v as HomeKind })}
              />
            </Form.Item>
            <Form.Item
              label={t('pages.nodes.home.gateway')}
              extra={t('pages.nodes.home.gatewayHint')}
              required
            >
              <Select
                aria-label={t('pages.nodes.home.gateway')}
                value={form.gatewayNodeId}
                options={servers}
                onChange={(v: number) => setForm({ ...form, gatewayNodeId: v })}
              />
            </Form.Item>
            {(form.kind === 'wireguard' || form.kind === 'openvpn') && (
              <Form.Item
                label={t('pages.nodes.home.config')}
                extra={t(
                  form.kind === 'wireguard'
                    ? 'pages.nodes.home.configHintWg'
                    : 'pages.nodes.home.configHintOvpn',
                )}
                required
              >
                <Input.TextArea
                  aria-label={t('pages.nodes.home.config')}
                  rows={7}
                  spellCheck={false}
                  value={form.config}
                  onChange={(e) => setForm({ ...form, config: e.target.value })}
                />
              </Form.Item>
            )}
            {(form.kind === 'l2tp' || form.kind === 'ikev2') && (
              <Form.Item label={t(serverLabel)} required>
                <Input
                  aria-label={t(serverLabel)}
                  value={form.server}
                  onChange={(e) => setForm({ ...form, server: e.target.value })}
                />
              </Form.Item>
            )}
            {form.kind !== 'wireguard' && (
              <Space wrap>
                <Form.Item label={t('pages.nodes.home.username')}>
                  <Input
                    aria-label={t('pages.nodes.home.username')}
                    value={form.username}
                    onChange={(e) => setForm({ ...form, username: e.target.value })}
                  />
                </Form.Item>
                <Form.Item label={t('pages.nodes.home.password')}>
                  <Input.Password
                    aria-label={t('pages.nodes.home.password')}
                    value={form.password}
                    onChange={(e) => setForm({ ...form, password: e.target.value })}
                  />
                </Form.Item>
                {(form.kind === 'l2tp' || form.kind === 'ikev2') && (
                  <Form.Item label={t(pskLabel)}>
                    <Input.Password
                      aria-label={t(pskLabel)}
                      value={form.psk}
                      onChange={(e) => setForm({ ...form, psk: e.target.value })}
                    />
                  </Form.Item>
                )}
              </Space>
            )}
            {form.kind === 'ikev2' && (
              <Form.Item
                label={t('pages.nodes.home.ca')}
                extra={t('pages.nodes.home.caHint')}
                required={form.psk === ''}
              >
                <Input.TextArea
                  aria-label={t('pages.nodes.home.ca')}
                  rows={5}
                  spellCheck={false}
                  placeholder="-----BEGIN CERTIFICATE-----"
                  value={form.config}
                  onChange={(e) => setForm({ ...form, config: e.target.value })}
                />
              </Form.Item>
            )}
            <Form.Item
              label={t('pages.nodes.home.subnets')}
              extra={t('pages.nodes.home.subnetsHint')}
            >
              <Select
                aria-label={t('pages.nodes.home.subnets')}
                mode="tags"
                tagRender={selectTagRender}
                value={form.subnets}
                tokenSeparators={[',', ' ']}
                onChange={(v: string[]) => setForm(pruneRuleTargets({ ...form, subnets: v }))}
              />
            </Form.Item>
            <Form.Item label={t('pages.nodes.home.hosts')} extra={t('pages.nodes.home.hostsHint')}>
              <Select
                aria-label={t('pages.nodes.home.hosts')}
                mode="tags"
                tagRender={selectTagRender}
                value={form.hosts}
                tokenSeparators={[',', ' ']}
                onChange={(v: string[]) => setForm(pruneRuleTargets({ ...form, hosts: v }))}
              />
            </Form.Item>
            <Typography.Title level={5}>{t('pages.nodes.home.rules')}</Typography.Title>
            <Typography.Paragraph type="secondary">
              {t('pages.nodes.home.rulesHint')}
            </Typography.Paragraph>
            {form.rules.map((rule, i) => (
              <Card
                key={i}
                size="small"
                style={{ marginBottom: 8 }}
                extra={
                  <Button
                    size="small"
                    danger
                    icon={<DeleteOutlined aria-hidden />}
                    aria-label={t('delete')}
                    onClick={() =>
                      setForm({ ...form, rules: form.rules.filter((_, j) => j !== i) })
                    }
                  />
                }
              >
                <Form.Item label={t('pages.nodes.home.ruleUsers')}>
                  <Select
                    aria-label={t('pages.nodes.home.ruleUsers')}
                    mode="tags"
                    tagRender={selectTagRender}
                    value={rule.emails}
                    options={emails.map((e) => ({ value: e, label: e }))}
                    onChange={(v: string[]) => setRule(i, { emails: v })}
                  />
                </Form.Item>
                <Form.Item label={t('pages.nodes.home.ruleGroups')}>
                  <Select
                    aria-label={t('pages.nodes.home.ruleGroups')}
                    mode="tags"
                    tagRender={selectTagRender}
                    value={rule.groups}
                    options={groups.map((g) => ({ value: g, label: g }))}
                    onChange={(v: string[]) => setRule(i, { groups: v })}
                  />
                </Form.Item>
                <Form.Item
                  label={t('pages.nodes.home.ruleTargets')}
                  extra={t('pages.nodes.home.ruleTargetsAll')}
                >
                  <Select
                    aria-label={t('pages.nodes.home.ruleTargets')}
                    mode="multiple"
                    tagRender={selectTagRender}
                    value={rule.targets}
                    options={targets.map((a) => ({ value: a, label: a }))}
                    onChange={(v: string[]) => setRule(i, { targets: v })}
                  />
                </Form.Item>
              </Card>
            ))}
            <Button
              icon={<PlusOutlined aria-hidden />}
              onClick={() =>
                setForm({
                  ...form,
                  rules: [...form.rules, { emails: [], groups: [], targets: [] }],
                })
              }
            >
              {t('pages.nodes.home.addRule')}
            </Button>
            {editing && Object.keys(editing.errors ?? {}).length > 0 && (
              <Alert
                style={{ marginTop: 12 }}
                type="error"
                showIcon
                title={Object.entries(editing.errors ?? {})
                  .map(([k, v]) => `${k}: ${v}`)
                  .join('; ')}
              />
            )}
          </Form>
        )}
      </Modal>
    </Card>
  );
}
