import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Form, Input, Modal, Space, Table, Tag, Typography } from 'antd';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  ClusterOutlined,
  CrownOutlined,
  PlusOutlined,
} from '@ant-design/icons';

import type { MasterSetup, MasterStatus } from '@/generated/types';
import { ClipboardManager, HttpUtil } from '@/utils';
import { getMessage } from '@/utils/messageBus';

export type MasterStatusRecord = MasterStatus;

export type MasterState = 'leader' | 'follower' | 'down' | 'pending';

export function masterState(m: MasterStatusRecord): MasterState {
  if (m.leader) return 'leader';
  if (m.alive) return 'follower';
  return m.pending ? 'pending' : 'down';
}

export type MastersLock = {
  reason: 'offline' | 'notPrimary';
  name: string;
} | null;

// Mirrors the server rule: change only on the leading primary while every
// joined master but spare is online; pending masters never block.
export function mastersLock(masters: MasterStatusRecord[], spare = ''): MastersLock {
  const joined = masters.filter((m) => !m.pending);
  const down = joined.find((m) => !m.alive && m.name !== spare);
  if (down) return { reason: 'offline', name: down.name };
  const primary = joined.find((m) => m.alive);
  if (primary && !(primary.self && primary.leader)) {
    return { reason: 'notPrimary', name: primary.name };
  }
  return null;
}

export function moveMaster(names: string[], from: number, to: number): string[] {
  const next = [...names];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
}

interface AddMasterBody {
  name: string;
  url?: string;
  dbHost?: string;
}

const MASTERS_KEY = ['masters', 'list'] as const;
const STATE_COLOR: Record<MasterState, string> = {
  leader: 'gold',
  follower: 'green',
  down: 'red',
  pending: 'blue',
};
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

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
  const [adding, setAdding] = useState(false);
  const [setup, setSetup] = useState<MasterSetup | null>(null);
  const [form] = Form.useForm<AddMasterBody>();
  const { data: masters = [] } = useQuery({
    queryKey: MASTERS_KEY,
    queryFn: fetchMasters,
    refetchInterval: 10_000,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: MASTERS_KEY });
  const forget = useMutation({
    mutationFn: (name: string) =>
      HttpUtil.post('/panel/api/masters/forget', { name }, JSON_HEADERS),
    onSettled: refresh,
  });
  const reorder = useMutation({
    mutationFn: (names: string[]) =>
      HttpUtil.post('/panel/api/masters/reorder', { names }, JSON_HEADERS),
    onSettled: refresh,
  });
  const add = useMutation({
    mutationFn: (body: AddMasterBody) =>
      HttpUtil.post<MasterSetup>('/panel/api/masters/add', body, JSON_HEADERS),
    onSuccess: (msg) => {
      if (msg?.success && msg.obj) setSetup(msg.obj);
    },
    onSettled: refresh,
  });
  if (masters.length === 0) return null;

  const lock = mastersLock(masters);
  const names = masters.map((m) => m.name);
  const busy = reorder.isPending || forget.isPending || add.isPending;
  const closeAdd = () => {
    setAdding(false);
    setSetup(null);
    form.resetFields();
  };

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
    {
      title: t('pages.nodes.masters.priority'),
      dataIndex: 'priority',
      key: 'priority',
    },
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
      render: (_: unknown, m: MasterStatusRecord) =>
        m.pending ? '—' : new Date(m.lastSeen * 1000).toLocaleString(),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, m: MasterStatusRecord, index: number) => {
        const forgetButton = (
          <Button
            size="small"
            danger
            disabled={busy || mastersLock(masters, m.name) !== null}
            onClick={() => forget.mutate(m.name)}
          >
            {t('pages.nodes.masters.forget')}
          </Button>
        );
        if (!m.alive && !m.pending) return forgetButton;
        const locked = busy || lock !== null;
        return (
          <Space size={4}>
            {m.pending ? (
              forgetButton
            ) : (
              <Button
                size="small"
                icon={<CrownOutlined aria-hidden />}
                disabled={locked || index === 0}
                onClick={() => reorder.mutate(moveMaster(names, index, 0))}
              >
                {t('pages.nodes.masters.makePrimary')}
              </Button>
            )}
            <Button
              size="small"
              icon={<ArrowUpOutlined aria-hidden />}
              aria-label={t('pages.nodes.masters.moveUp')}
              disabled={locked || index === 0}
              onClick={() => reorder.mutate(moveMaster(names, index, index - 1))}
            />
            <Button
              size="small"
              icon={<ArrowDownOutlined aria-hidden />}
              aria-label={t('pages.nodes.masters.moveDown')}
              disabled={locked || index === masters.length - 1}
              onClick={() => reorder.mutate(moveMaster(names, index, index + 1))}
            />
          </Space>
        );
      },
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
      extra={
        <Button
          size="small"
          icon={<PlusOutlined aria-hidden />}
          disabled={busy || lock !== null}
          onClick={() => setAdding(true)}
        >
          {t('pages.nodes.masters.add')}
        </Button>
      }
    >
      <Typography.Paragraph type="secondary">{t('pages.nodes.masters.desc')}</Typography.Paragraph>
      {lock && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          title={t(
            lock.reason === 'offline'
              ? 'pages.nodes.masters.lockedOffline'
              : 'pages.nodes.masters.lockedNotPrimary',
            { name: lock.name },
          )}
        />
      )}
      <Table
        rowKey="name"
        size="small"
        dataSource={masters}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
      <Modal
        open={adding}
        title={t('pages.nodes.masters.addTitle')}
        onCancel={closeAdd}
        footer={null}
        width={640}
        destroyOnHidden
      >
        {setup ? (
          <Space direction="vertical" style={{ width: '100%' }}>
            <Typography.Paragraph>{t('pages.nodes.masters.setupHint')}</Typography.Paragraph>
            {setup.dbLocal && (
              <Alert type="warning" showIcon title={t('pages.nodes.masters.setupDbLocal')} />
            )}
            {setup.keyFile && (
              <Alert
                type="info"
                showIcon
                title={t('pages.nodes.masters.setupKeyFile', {
                  file: setup.keyFile,
                })}
              />
            )}
            <Input.TextArea
              aria-label="/etc/default/x-ui"
              value={setup.env}
              readOnly
              autoSize
              style={{ fontFamily: 'monospace' }}
            />
            <Button
              type="primary"
              onClick={async () => {
                if (await ClipboardManager.copyText(setup.env)) getMessage().success(t('copied'));
              }}
            >
              {t('pages.nodes.masters.copyEnv')}
            </Button>
          </Space>
        ) : (
          <Form form={form} layout="vertical" onFinish={(values) => add.mutate(values)}>
            <Form.Item
              name="name"
              label={t('pages.nodes.masters.addName')}
              rules={[{ required: true, whitespace: true, max: 64 }]}
            >
              <Input placeholder="m3" />
            </Form.Item>
            <Form.Item
              name="url"
              label={t('pages.nodes.masters.addUrl')}
              rules={[{ type: 'url', max: 255 }]}
            >
              <Input placeholder="https://10.0.0.3:2053" />
            </Form.Item>
            <Form.Item
              name="dbHost"
              label={t('pages.nodes.masters.addDbHost')}
              extra={t('pages.nodes.masters.addDbHostHint')}
              rules={[{ max: 255, whitespace: true }]}
            >
              <Input placeholder="10.0.0.1" />
            </Form.Item>
            <Button type="primary" htmlType="submit" loading={add.isPending}>
              {t('pages.nodes.masters.addSubmit')}
            </Button>
          </Form>
        )}
      </Modal>
    </Card>
  );
}
