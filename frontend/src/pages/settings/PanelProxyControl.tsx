import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Modal, Segmented, Select, Space, Tag, Typography } from 'antd';
import { CheckCircleOutlined, GlobalOutlined } from '@ant-design/icons';
import { z } from 'zod';

import {
  PanelProxyCheckSchema,
  PanelProxyStatusSchema,
  type PanelProxyCheck,
  type PanelProxyStatus,
} from '@/generated/zod';
import { HttpUtil } from '@/utils';

type Mode = 'client' | 'link' | 'subscription';

const ClientListSchema = z.array(z.string());

async function postParsed<T extends z.ZodType>(
  path: string,
  schema: T,
  body?: Record<string, string>,
): Promise<z.infer<T> | null> {
  const msg = await HttpUtil.post(path, body, { silent: body === undefined });
  if (!msg?.success) return null;
  const parsed = schema.safeParse(msg.obj);
  return parsed.success ? parsed.data : null;
}

interface PanelProxyControlProps {
  onStatus: (status: PanelProxyStatus | null) => void;
}

// The "two clicks" entry to the panel egress: pick a source, press Enable.
export default function PanelProxyControl({ onStatus }: PanelProxyControlProps) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<PanelProxyStatus | null>(null);
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>('client');
  const [source, setSource] = useState('');
  const [clients, setClients] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [check, setCheck] = useState<PanelProxyCheck | null>(null);

  const applyStatus = useCallback(
    (next: PanelProxyStatus | null) => {
      setStatus(next);
      onStatus(next);
    },
    [onStatus],
  );

  useEffect(() => {
    let cancelled = false;
    void postParsed('/panel/api/setting/panelProxy', PanelProxyStatusSchema).then((next) => {
      if (!cancelled) applyStatus(next);
    });
    return () => {
      cancelled = true;
    };
  }, [applyStatus]);

  const openModal = async () => {
    setOpen(true);
    if (status?.mode) {
      setMode(status.mode as Mode);
      setSource(status.source);
    }
    const list = await postParsed('/panel/api/setting/panelProxy/clients', ClientListSchema);
    const emails = list ?? [];
    setClients(emails);
    if (!status?.mode && emails.length > 0) setSource(emails[0]);
  };

  const save = async (nextMode: Mode | '', nextSource: string) => {
    setBusy(true);
    setCheck(null);
    try {
      const next = await postParsed('/panel/api/setting/panelProxy/set', PanelProxyStatusSchema, {
        mode: nextMode,
        source: nextSource,
      });
      if (next) {
        applyStatus(next);
        setOpen(false);
      }
    } finally {
      setBusy(false);
    }
  };

  const runCheck = async () => {
    setBusy(true);
    try {
      setCheck(await postParsed('/panel/api/setting/panelProxy/check', PanelProxyCheckSchema, {}));
    } finally {
      setBusy(false);
    }
  };

  const changeMode = (next: Mode) => {
    setMode(next);
    setSource(next === 'client' ? (clients[0] ?? '') : '');
  };

  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Space wrap>
        <Button icon={<GlobalOutlined aria-hidden />} onClick={openModal}>
          {t('pages.settings.panelProxy.setup')}
        </Button>
        {status?.mode && (
          <>
            {status.active ? (
              <Tag color="green">
                {t('pages.settings.panelProxy.active', {
                  count: status.outbounds.length,
                })}
              </Tag>
            ) : (
              <Tag color="red">
                {t('pages.settings.panelProxy.broken', { error: status.error })}
              </Tag>
            )}
            <Button icon={<CheckCircleOutlined aria-hidden />} loading={busy} onClick={runCheck}>
              {t('pages.settings.panelProxy.check')}
            </Button>
            <Button danger disabled={busy} onClick={() => save('', '')}>
              {t('pages.settings.panelProxy.disable')}
            </Button>
          </>
        )}
      </Space>
      {check && <CheckResult check={check} />}

      <Modal
        open={open}
        title={t('pages.settings.panelProxy.title')}
        okText={t('pages.settings.panelProxy.enable')}
        okButtonProps={{ disabled: !source.trim(), loading: busy }}
        onOk={() => save(mode, source)}
        onCancel={() => setOpen(false)}
        destroyOnHidden
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Typography.Paragraph type="secondary">
            {t('pages.settings.panelProxy.desc')}
          </Typography.Paragraph>
          <Segmented<Mode>
            aria-label={t('pages.settings.panelProxy.title')}
            block
            value={mode}
            onChange={changeMode}
            options={[
              {
                value: 'client',
                label: t('pages.settings.panelProxy.modeClient'),
              },
              { value: 'link', label: t('pages.settings.panelProxy.modeLink') },
              {
                value: 'subscription',
                label: t('pages.settings.panelProxy.modeSubscription'),
              },
            ]}
          />
          {mode === 'client' &&
            (clients.length > 0 ? (
              <Select
                style={{ width: '100%' }}
                showSearch
                value={source || undefined}
                aria-label={t('pages.settings.panelProxy.modeClient')}
                placeholder={t('pages.settings.panelProxy.clientPh')}
                options={clients.map((email) => ({
                  label: email,
                  value: email,
                }))}
                onChange={(v: string) => setSource(v)}
              />
            ) : (
              <Alert type="info" showIcon message={t('pages.settings.panelProxy.noClients')} />
            ))}
          {mode === 'link' && (
            <Input.TextArea
              rows={3}
              value={source}
              aria-label={t('pages.settings.panelProxy.modeLink')}
              placeholder={t('pages.settings.panelProxy.linkPh')}
              onChange={(e) => setSource(e.target.value)}
            />
          )}
          {mode === 'subscription' && (
            <Input
              value={source}
              aria-label={t('pages.settings.panelProxy.modeSubscription')}
              placeholder={t('pages.settings.panelProxy.subscriptionPh')}
              onChange={(e) => setSource(e.target.value)}
            />
          )}
        </Space>
      </Modal>
    </Space>
  );
}

function CheckResult({ check }: { check: PanelProxyCheck }) {
  const { t } = useTranslation();
  if (!check.viaProxy) {
    return <Alert type="warning" showIcon message={t('pages.settings.panelProxy.checkDirect')} />;
  }
  if (!check.telegram) {
    return (
      <Alert
        type="error"
        showIcon
        message={t('pages.settings.panelProxy.checkFail', {
          error: check.error,
        })}
      />
    );
  }
  return (
    <Alert
      type="success"
      showIcon
      message={t('pages.settings.panelProxy.checkOk', {
        ms: check.telegramMs,
        ip: check.exitIp || '?',
      })}
    />
  );
}
