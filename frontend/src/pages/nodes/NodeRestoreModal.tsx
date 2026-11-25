import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Input, InputNumber, Modal, Select, Space, Typography } from 'antd';

import {
  useBackupMutations,
  useNodeBackupsQuery,
  useNodeRestoreStatusQuery,
} from '@/api/queries/useBackups';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { IntlUtil, SizeFormatter } from '@/utils';
import { onNumber } from '@/utils/onNumber';

interface NodeRestoreModalProps {
  node: NodeRecord;
  onClose: () => void;
}

const NO_BACKUP = 'none';

export default function NodeRestoreModal({ node, onClose }: NodeRestoreModalProps) {
  const { t } = useTranslation();
  const { data: files = [], isError: listFailed, error: listError } = useNodeBackupsQuery(node.id);
  const { restore, busy } = useBackupMutations();
  const [started, setStarted] = useState(false);
  const { data: progress } = useNodeRestoreStatusQuery(started);

  const [host, setHost] = useState('');
  const [port, setPort] = useState(22);
  const [user, setUser] = useState('root');
  const [password, setPassword] = useState('');
  const [privateKey, setPrivateKey] = useState('');
  const [backup, setBackup] = useState('');

  const running = started && (progress?.running ?? true);
  const finished = started && progress && !progress.running && progress.nodeId === node.id;

  const onStart = async () => {
    const msg = await restore(node.id, {
      host,
      port,
      user,
      password,
      privateKey,
      backup,
    });
    if (msg?.success) setStarted(true);
  };

  const backupOptions = [
    { value: '', label: t('pages.nodes.restore.latest') },
    ...files.map((f) => ({
      value: f.name,
      label: `${IntlUtil.formatDate(f.modTime * 1000)} · ${SizeFormatter.sizeFormat(f.size)}`,
    })),
    { value: NO_BACKUP, label: t('pages.nodes.restore.noBackup') },
  ];

  return (
    <Modal
      open
      title={t('pages.nodes.restore.title', { name: node.name ?? '' })}
      okText={t('pages.nodes.restore.start')}
      okButtonProps={{
        disabled: started || !host.trim() || (!password && !privateKey.trim()),
        loading: busy || running,
      }}
      cancelText={finished ? t('close') : t('cancel')}
      onOk={onStart}
      onCancel={onClose}
      destroyOnHidden
    >
      {!started ? (
        <Space orientation="vertical" style={{ width: '100%' }}>
          <Alert type="info" showIcon title={t('pages.nodes.restore.desc')} />
          {listFailed && <Alert type="warning" showIcon title={(listError as Error).message} />}
          <Typography.Text strong>{t('pages.nodes.restore.server')}</Typography.Text>
          <Space.Compact style={{ width: '100%' }}>
            <Input
              aria-label={t('pages.nodes.restore.server')}
              value={host}
              placeholder="203.0.113.20"
              onChange={(e) => setHost(e.target.value)}
            />
            <InputNumber
              aria-label={t('pages.settings.backup.port')}
              min={1}
              max={65535}
              value={port}
              onChange={onNumber(setPort)}
            />
          </Space.Compact>
          <Input
            aria-label={t('pages.settings.backup.user')}
            placeholder={t('pages.settings.backup.user')}
            value={user}
            onChange={(e) => setUser(e.target.value)}
          />
          <Input.Password
            aria-label={t('pages.settings.backup.password')}
            placeholder={t('pages.settings.backup.password')}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <Input.TextArea
            aria-label={t('pages.settings.backup.privateKey')}
            rows={3}
            placeholder={t('pages.nodes.restore.keyPlaceholder')}
            value={privateKey}
            onChange={(e) => setPrivateKey(e.target.value)}
          />
          <Typography.Text strong>{t('pages.nodes.restore.backup')}</Typography.Text>
          <Select
            aria-label={t('pages.nodes.restore.backup')}
            style={{ width: '100%' }}
            value={backup}
            options={backupOptions}
            onChange={setBackup}
          />
        </Space>
      ) : (
        <Space orientation="vertical" style={{ width: '100%' }}>
          {finished && !progress?.error && (
            <Alert type="success" showIcon title={t('pages.nodes.restore.done')} />
          )}
          {finished && progress?.error && <Alert type="error" showIcon title={progress.error} />}
          <pre
            style={{
              maxHeight: 320,
              overflow: 'auto',
              fontSize: 12,
              whiteSpace: 'pre-wrap',
            }}
          >
            {(progress?.log ?? []).join('\n')}
          </pre>
        </Space>
      )}
    </Modal>
  );
}
