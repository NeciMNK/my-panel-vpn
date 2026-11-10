import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Checkbox,
  Input,
  Modal,
  Popconfirm,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { DeleteOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';

import {
  useTlsCertMutations,
  useTlsCertSettingsQuery,
  useTlsCertsQuery,
} from '@/api/queries/useTlsCerts';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import { SettingListItem } from '@/components/ui';
import type { TlsCertificate } from '@/schemas/tlsCert';
import { IntlUtil } from '@/utils';

const STATUS_COLORS: Record<TlsCertificate['status'], string> = {
  pending: 'processing',
  valid: 'success',
  error: 'error',
};

export function splitDomains(raw: string): string[] {
  return raw
    .split(/[\s,;]+/)
    .map((d) => d.trim())
    .filter(Boolean);
}

export default function TlsCertsTab() {
  const { t } = useTranslation();
  const { data: certs = [], isFetching, isError, error, refetch } = useTlsCertsQuery();
  const { data: settings } = useTlsCertSettingsQuery();
  const { nodes } = useNodesQuery();
  const { saveToken, issue, renew, setAutoRenew, remove, busy } = useTlsCertMutations();

  const [token, setToken] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [domainsRaw, setDomainsRaw] = useState('');
  const [autoRenew, setAutoRenewDraft] = useState(true);
  const [applyToPanel, setApplyToPanel] = useState(false);

  const tokenSet = settings?.cloudflareTokenSet ?? false;
  const nodeName = (id: string) => nodes.find((n) => String(n.id) === id)?.name ?? `#${id}`;

  const onSaveToken = async () => {
    const msg = await saveToken(token);
    if (msg?.success) setToken('');
  };

  const onIssue = async () => {
    const msg = await issue({
      domains: splitDomains(domainsRaw),
      autoRenew,
      applyToPanel,
    });
    if (msg?.success) {
      setModalOpen(false);
      setDomainsRaw('');
    }
  };

  const statusLabel: Record<TlsCertificate['status'], string> = {
    pending: t('pages.settings.tlsCerts.statusPending'),
    valid: t('pages.settings.tlsCerts.statusValid'),
    error: t('pages.settings.tlsCerts.statusError'),
  };

  const columns = [
    {
      title: t('pages.settings.tlsCerts.domains'),
      key: 'domains',
      render: (_: unknown, r: TlsCertificate) => (
        <Space orientation="vertical" size={0}>
          {r.domains.map((d) => (
            <Typography.Text key={d} code>
              {d}
            </Typography.Text>
          ))}
        </Space>
      ),
    },
    {
      title: t('pages.settings.tlsCerts.status'),
      key: 'status',
      render: (_: unknown, r: TlsCertificate) => (
        <Space orientation="vertical" size={2}>
          <Tag color={STATUS_COLORS[r.status]}>{statusLabel[r.status]}</Tag>
          {r.lastError && (
            <Typography.Text type="danger" style={{ fontSize: 12 }}>
              {r.lastError}
            </Typography.Text>
          )}
        </Space>
      ),
    },
    {
      title: t('pages.settings.tlsCerts.expires'),
      key: 'notAfter',
      render: (_: unknown, r: TlsCertificate) =>
        r.notAfter ? IntlUtil.formatDate(r.notAfter) : '—',
    },
    {
      title: t('pages.settings.tlsCerts.files'),
      key: 'files',
      render: (_: unknown, r: TlsCertificate) =>
        r.certFile ? (
          <Space orientation="vertical" size={0}>
            <Typography.Text copyable style={{ fontSize: 12 }}>
              {r.certFile}
            </Typography.Text>
            <Typography.Text copyable style={{ fontSize: 12 }}>
              {r.keyFile}
            </Typography.Text>
          </Space>
        ) : (
          '—'
        ),
    },
    {
      title: t('pages.settings.tlsCerts.nodes'),
      key: 'nodes',
      render: (_: unknown, r: TlsCertificate) => (
        <Space wrap size={4}>
          {Object.entries(r.nodeInstalls ?? {}).map(([id, inst]) => {
            const current = !inst.error && inst.notAfter === r.notAfter;
            return (
              <Tooltip
                key={id}
                title={
                  inst.error ||
                  (current
                    ? t('pages.settings.tlsCerts.nodeCurrent')
                    : t('pages.settings.tlsCerts.nodeOutdated'))
                }
              >
                <Tag color={inst.error ? 'error' : current ? 'success' : 'warning'}>
                  {nodeName(id)}
                </Tag>
              </Tooltip>
            );
          })}
        </Space>
      ),
    },
    {
      title: t('pages.settings.tlsCerts.autoRenew'),
      key: 'autoRenew',
      align: 'center' as const,
      render: (_: unknown, r: TlsCertificate) => (
        <Switch
          size="small"
          aria-label={t('pages.settings.tlsCerts.autoRenew')}
          checked={r.autoRenew}
          onChange={(v) => setAutoRenew(r.id, v)}
        />
      ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, r: TlsCertificate) => (
        <Space>
          <Tooltip title={t('pages.settings.tlsCerts.renew')}>
            <Button
              aria-label={t('pages.settings.tlsCerts.renew')}
              size="small"
              icon={<ReloadOutlined aria-hidden />}
              disabled={r.status === 'pending'}
              onClick={() => renew(r.id)}
            />
          </Tooltip>
          <Popconfirm
            title={t('pages.settings.tlsCerts.deleteConfirm')}
            okText={t('delete')}
            cancelText={t('cancel')}
            onConfirm={() => remove(r.id)}
          >
            <Button
              aria-label={t('delete')}
              size="small"
              danger
              disabled={r.status === 'pending'}
              icon={<DeleteOutlined aria-hidden />}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        title={t('pages.settings.tlsCerts.desc')}
      />
      <SettingListItem
        title={t('pages.settings.tlsCerts.cfToken')}
        description={t('pages.settings.tlsCerts.cfTokenDesc')}
      >
        <Space.Compact style={{ width: '100%' }}>
          <Input.Password
            aria-label={t('pages.settings.tlsCerts.cfToken')}
            value={token}
            placeholder={
              tokenSet
                ? t('pages.settings.tlsCerts.cfTokenSet')
                : t('pages.settings.tlsCerts.cfTokenNotSet')
            }
            onChange={(e) => setToken(e.target.value)}
          />
          <Button type="primary" disabled={!token.trim()} loading={busy} onClick={onSaveToken}>
            {t('pages.settings.tlsCerts.saveToken')}
          </Button>
        </Space.Compact>
      </SettingListItem>

      {isError && (
        <Alert
          type="error"
          showIcon
          style={{ margin: '16px 0' }}
          title={(error as Error).message}
          action={
            <Button size="small" onClick={() => refetch()}>
              {t('refresh')}
            </Button>
          }
        />
      )}
      <div style={{ margin: '16px 0 12px' }}>
        <Button
          type="primary"
          icon={<PlusOutlined aria-hidden />}
          disabled={!tokenSet}
          onClick={() => setModalOpen(true)}
        >
          {t('pages.settings.tlsCerts.issue')}
        </Button>
      </div>
      {certs.some((c) => c.status === 'pending') && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          title={t('pages.settings.tlsCerts.pendingHint')}
        />
      )}
      <Table
        size="small"
        dataSource={certs}
        rowKey={(r) => r.id}
        pagination={false}
        loading={isFetching && certs.length === 0}
        scroll={{ x: true }}
        locale={{ emptyText: t('pages.settings.tlsCerts.empty') }}
        columns={columns}
      />

      <Modal
        open={modalOpen}
        title={t('pages.settings.tlsCerts.issue')}
        okText={t('pages.settings.tlsCerts.issue')}
        cancelText={t('cancel')}
        okButtonProps={{
          disabled: splitDomains(domainsRaw).length === 0,
          loading: busy,
        }}
        onOk={onIssue}
        onCancel={() => setModalOpen(false)}
      >
        <Space orientation="vertical" style={{ width: '100%' }}>
          <Typography.Text strong>{t('pages.settings.tlsCerts.domains')}</Typography.Text>
          <Input.TextArea
            aria-label={t('pages.settings.tlsCerts.domains')}
            autoSize={{ minRows: 2, maxRows: 6 }}
            placeholder={'example.com\n*.example.com'}
            value={domainsRaw}
            onChange={(e) => setDomainsRaw(e.target.value)}
          />
          <Typography.Text type="secondary">
            {t('pages.settings.tlsCerts.domainsDesc')}
          </Typography.Text>
          <Checkbox checked={autoRenew} onChange={(e) => setAutoRenewDraft(e.target.checked)}>
            {t('pages.settings.tlsCerts.autoRenew')}
          </Checkbox>
          <Checkbox checked={applyToPanel} onChange={(e) => setApplyToPanel(e.target.checked)}>
            {t('pages.settings.tlsCerts.applyToPanel')}
          </Checkbox>
          {applyToPanel && (
            <Typography.Text type="secondary">
              {t('pages.settings.tlsCerts.applyToPanelDesc')}
            </Typography.Text>
          )}
        </Space>
      </Modal>
    </div>
  );
}
