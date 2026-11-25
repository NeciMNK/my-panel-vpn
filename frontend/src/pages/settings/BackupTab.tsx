import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, InputNumber, Space, Switch, Table, Tag, Typography } from 'antd';
import { CloudUploadOutlined, LinkOutlined, SaveOutlined } from '@ant-design/icons';

import {
  useBackupMutations,
  useBackupSettingsQuery,
  useBackupStatusQuery,
} from '@/api/queries/useBackups';
import { SettingListItem } from '@/components/ui';
import type { BackupNodeRun, BackupSettings } from '@/generated/zod';
import { IntlUtil } from '@/utils';
import { onNumber } from '@/utils/onNumber';

import SecretInput from './SecretInput';

type Draft = Omit<BackupSettings, 'hasPassword' | 'hasPrivateKey' | 'hostKey'>;

function toDraft(s: BackupSettings): Draft {
  return {
    host: s.host,
    port: s.port,
    user: s.user,
    dir: s.dir,
    dbEnable: s.dbEnable,
    dbEveryHours: s.dbEveryHours,
    dbOnChange: s.dbOnChange,
    nodesEnable: s.nodesEnable,
    nodesEveryHours: s.nodesEveryHours,
    keep: s.keep,
  };
}

function RunResult({ at, file, error }: { at: number; file: string; error: string }) {
  const { t } = useTranslation();
  if (!at)
    return <Typography.Text type="secondary">{t('pages.settings.backup.never')}</Typography.Text>;
  return (
    <Space orientation="vertical" size={0}>
      <Space size={4}>
        <Tag color={error ? 'error' : 'success'}>
          {error ? t('pages.settings.backup.failed') : t('pages.settings.backup.ok')}
        </Tag>
        <Typography.Text type="secondary">{IntlUtil.formatDate(at * 1000)}</Typography.Text>
      </Space>
      {error ? (
        <Typography.Text type="danger" style={{ fontSize: 12 }}>
          {error}
        </Typography.Text>
      ) : (
        <Typography.Text style={{ fontSize: 12 }} copyable>
          {file}
        </Typography.Text>
      )}
    </Space>
  );
}

export default function BackupTab() {
  const { data: settings } = useBackupSettingsQuery();
  if (!settings) return null;
  // Remounting on new saved settings resets the draft to what the server holds.
  return <BackupForm key={JSON.stringify(settings)} settings={settings} />;
}

function BackupForm({ settings }: { settings: BackupSettings }) {
  const { t } = useTranslation();
  const { data: status } = useBackupStatusQuery();
  const { save, test, run, busy } = useBackupMutations();

  const [draft, setDraft] = useState<Draft>(() => toDraft(settings));
  const [password, setPassword] = useState('');
  const [clearPassword, setClearPassword] = useState(false);
  const [privateKey, setPrivateKey] = useState('');
  const [clearKey, setClearKey] = useState(false);

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) =>
    setDraft({ ...draft, [key]: value });

  const onSave = async () => {
    const msg = await save({
      ...draft,
      password: password || clearPassword ? password : undefined,
      privateKey: privateKey.trim() || clearKey ? privateKey : undefined,
    });
    if (msg?.success) {
      setPassword('');
      setPrivateKey('');
      setClearPassword(false);
      setClearKey(false);
    }
  };

  const configured = !!settings.host;
  const nodeColumns = [
    { title: t('pages.settings.backup.node'), dataIndex: 'name', key: 'name' },
    {
      title: t('pages.settings.backup.lastBackup'),
      key: 'last',
      render: (_: unknown, r: BackupNodeRun) => (
        <RunResult at={r.at} file={r.file} error={r.error} />
      ),
    },
  ];

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        title={t('pages.settings.backup.desc')}
      />

      <Typography.Title level={5}>{t('pages.settings.backup.server')}</Typography.Title>
      <SettingListItem
        title={t('pages.settings.backup.host')}
        description={t('pages.settings.backup.hostDesc')}
      >
        <Space.Compact style={{ width: '100%' }}>
          <Input
            aria-label={t('pages.settings.backup.host')}
            value={draft.host}
            placeholder="backup.example.com"
            onChange={(e) => set('host', e.target.value)}
          />
          <InputNumber
            aria-label={t('pages.settings.backup.port')}
            min={1}
            max={65535}
            value={draft.port}
            onChange={onNumber((v) => set('port', v))}
          />
        </Space.Compact>
      </SettingListItem>
      <SettingListItem title={t('pages.settings.backup.user')}>
        <Input
          aria-label={t('pages.settings.backup.user')}
          value={draft.user}
          onChange={(e) => set('user', e.target.value)}
        />
      </SettingListItem>
      <SettingListItem title={t('pages.settings.backup.password')}>
        <SecretInput
          value={password}
          configured={settings.hasPassword}
          clearArmed={clearPassword}
          placeholder={t('pages.settings.backup.secretSet')}
          onChange={setPassword}
          onClearArmedChange={setClearPassword}
        />
      </SettingListItem>
      <SettingListItem
        title={t('pages.settings.backup.privateKey')}
        description={t('pages.settings.backup.privateKeyDesc')}
      >
        <Space orientation="vertical" style={{ width: '100%' }}>
          <Input.TextArea
            aria-label={t('pages.settings.backup.privateKey')}
            rows={3}
            value={privateKey}
            placeholder={
              settings.hasPrivateKey && !clearKey
                ? t('pages.settings.backup.secretSet')
                : '-----BEGIN OPENSSH PRIVATE KEY-----'
            }
            onChange={(e) => {
              setPrivateKey(e.target.value);
              setClearKey(false);
            }}
          />
          {settings.hasPrivateKey && (
            <Button size="small" danger={clearKey} onClick={() => setClearKey(!clearKey)}>
              {clearKey ? t('pages.settings.secretClearUndo') : t('pages.settings.secretClear')}
            </Button>
          )}
        </Space>
      </SettingListItem>
      <SettingListItem title={t('pages.settings.backup.dir')}>
        <Input
          aria-label={t('pages.settings.backup.dir')}
          value={draft.dir}
          onChange={(e) => set('dir', e.target.value)}
        />
      </SettingListItem>
      {settings.hostKey && (
        <SettingListItem
          title={t('pages.settings.backup.hostKey')}
          description={t('pages.settings.backup.hostKeyDesc')}
        >
          <Typography.Text code copyable>
            {settings.hostKey}
          </Typography.Text>
        </SettingListItem>
      )}

      <Typography.Title level={5} style={{ marginTop: 16 }}>
        {t('pages.settings.backup.schedule')}
      </Typography.Title>
      <SettingListItem
        title={t('pages.settings.backup.dbEnable')}
        description={t('pages.settings.backup.everyHours')}
        control={
          <Space>
            <InputNumber
              aria-label={t('pages.settings.backup.everyHours')}
              min={1}
              value={draft.dbEveryHours}
              onChange={onNumber((v) => set('dbEveryHours', v))}
            />
            <Switch
              aria-label={t('pages.settings.backup.dbEnable')}
              checked={draft.dbEnable}
              onChange={(v) => set('dbEnable', v)}
            />
          </Space>
        }
      />
      <SettingListItem
        title={t('pages.settings.backup.dbOnChange')}
        description={t('pages.settings.backup.dbOnChangeDesc')}
        control={
          <Switch
            aria-label={t('pages.settings.backup.dbOnChange')}
            checked={draft.dbOnChange}
            onChange={(v) => set('dbOnChange', v)}
          />
        }
      />
      <SettingListItem
        title={t('pages.settings.backup.nodesEnable')}
        description={t('pages.settings.backup.nodesEnableDesc')}
        control={
          <Space>
            <InputNumber
              aria-label={t('pages.settings.backup.everyHours')}
              min={1}
              value={draft.nodesEveryHours}
              onChange={onNumber((v) => set('nodesEveryHours', v))}
            />
            <Switch
              aria-label={t('pages.settings.backup.nodesEnable')}
              checked={draft.nodesEnable}
              onChange={(v) => set('nodesEnable', v)}
            />
          </Space>
        }
      />
      <SettingListItem
        title={t('pages.settings.backup.keep')}
        control={
          <InputNumber
            aria-label={t('pages.settings.backup.keep')}
            min={1}
            max={1000}
            value={draft.keep}
            onChange={onNumber((v) => set('keep', v))}
          />
        }
      />

      <Space wrap style={{ margin: '16px 0' }}>
        <Button type="primary" icon={<SaveOutlined aria-hidden />} loading={busy} onClick={onSave}>
          {t('pages.settings.save')}
        </Button>
        <Button
          icon={<LinkOutlined aria-hidden />}
          disabled={!configured}
          loading={busy}
          onClick={() => test()}
        >
          {t('pages.settings.backup.test')}
        </Button>
        <Button
          icon={<CloudUploadOutlined aria-hidden />}
          disabled={!configured || !!status?.running}
          onClick={() => run('db')}
        >
          {t('pages.settings.backup.runDb')}
        </Button>
        <Button
          icon={<CloudUploadOutlined aria-hidden />}
          disabled={!configured || !!status?.running}
          onClick={() => run('nodes')}
        >
          {t('pages.settings.backup.runNodes')}
        </Button>
      </Space>

      {status?.running && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          title={t('pages.settings.backup.running')}
        />
      )}
      <SettingListItem title={t('pages.settings.backup.lastDb')}>
        <RunResult
          at={status?.db.at ?? 0}
          file={status?.db.file ?? ''}
          error={status?.db.error ?? ''}
        />
      </SettingListItem>
      <Table
        size="small"
        style={{ marginTop: 12 }}
        dataSource={status?.nodes ?? []}
        rowKey={(r) => r.nodeId}
        pagination={false}
        scroll={{ x: true }}
        locale={{ emptyText: t('pages.settings.backup.noNodeBackups') }}
        columns={nodeColumns}
      />
    </div>
  );
}
