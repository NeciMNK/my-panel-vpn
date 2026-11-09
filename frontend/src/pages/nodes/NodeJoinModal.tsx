import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Input, Modal, Space, Typography } from 'antd';
import { CopyOutlined, ReloadOutlined } from '@ant-design/icons';

import { ClipboardManager, HttpUtil } from '@/utils';
import { getMessage } from '@/utils/messageBus';

interface JoinToken {
  token: string;
  expiresAt: number;
  arch: string;
}

interface NodeJoinModalProps {
  open: boolean;
  onClose: () => void;
}

export function masterBaseUrl(): string {
  const base = window.X_UI_BASE_PATH || '/';
  return `${window.location.origin}${base.endsWith('/') ? base.slice(0, -1) : base}`;
}

export function buildJoinCommand(
  master: string,
  token: string,
  insecure: boolean,
  country = '',
): string {
  const curl = insecure ? 'curl -fsSLk' : 'curl -fsSL';
  const code = country.trim().toUpperCase();
  const flags = `${/^[A-Z]{2}$/.test(code) ? ` --country ${code}` : ''}${insecure ? ' --insecure' : ''}`;
  return `${curl} '${master}/node-join/install.sh' | bash -s -- --master '${master}' --join '${token}'${flags}`;
}

export default function NodeJoinModal({ open, onClose }: NodeJoinModalProps) {
  const { t } = useTranslation();
  const [nonce, setNonce] = useState(0);
  const [insecure, setInsecure] = useState(window.location.protocol !== 'https:');
  const [country, setCountry] = useState('');

  // Every open (and every "new command") mints a fresh one-time token.
  const query = useQuery({
    queryKey: ['nodes', 'joinToken', nonce],
    queryFn: async (): Promise<JoinToken> => {
      const msg = await HttpUtil.post<JoinToken>('/panel/api/nodes/joinToken');
      if (!msg?.success || !msg.obj) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg.obj;
    },
    enabled: open,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  });
  const join = open ? (query.data ?? null) : null;
  const error = query.error ? query.error.message : '';

  const command = useMemo(
    () => (join ? buildJoinCommand(masterBaseUrl(), join.token, insecure, country) : ''),
    [join, insecure, country],
  );

  const onCopy = async () => {
    if (await ClipboardManager.copyText(command)) getMessage().success(t('copied'));
  };

  return (
    <Modal
      open={open}
      title={t('pages.nodes.join.title')}
      onCancel={onClose}
      footer={null}
      width={720}
      destroyOnHidden
    >
      <Space direction="vertical" style={{ width: '100%' }}>
        <Typography.Paragraph>{t('pages.nodes.join.desc')}</Typography.Paragraph>
        {error && <Alert type="error" showIcon title={error} />}
        <Input.TextArea
          aria-label={t('pages.nodes.join.title')}
          value={command}
          readOnly
          autoSize={{ minRows: 3 }}
          style={{ fontFamily: 'monospace' }}
        />
        <Space wrap>
          <Button
            type="primary"
            icon={<CopyOutlined aria-hidden />}
            disabled={!command}
            onClick={onCopy}
          >
            {t('copy')}
          </Button>
          <Button
            icon={<ReloadOutlined aria-hidden />}
            loading={query.isFetching}
            onClick={() => setNonce((n) => n + 1)}
          >
            {t('pages.nodes.join.regenerate')}
          </Button>
          <Input
            aria-label={t('pages.nodes.country')}
            placeholder={t('pages.nodes.country')}
            maxLength={2}
            value={country}
            onChange={(e) => setCountry(e.target.value.toUpperCase())}
            style={{ width: 120 }}
          />
          <Checkbox checked={insecure} onChange={(e) => setInsecure(e.target.checked)}>
            {t('pages.nodes.join.insecure')}
          </Checkbox>
        </Space>
        {join && (
          <Typography.Text type="secondary">
            {t('pages.nodes.join.hint', {
              arch: join.arch,
              time: new Date(join.expiresAt * 1000).toLocaleTimeString(),
            })}
          </Typography.Text>
        )}
      </Space>
    </Modal>
  );
}
