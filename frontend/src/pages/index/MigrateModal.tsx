import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { FormProvider, useForm, useWatch } from 'react-hook-form';
import {
  Alert,
  Button,
  Checkbox,
  Form,
  Input,
  Modal,
  Radio,
  Select,
  Space,
  Table,
  Upload,
} from 'antd';
import { UploadOutlined } from '@ant-design/icons';

import { FormField } from '@/components/form/rhf';
import {
  MigrationPreviewSchema,
  MigrationResultSchema,
  type MigrationFormValues,
  type MigrationPreview,
  type MigrationResult,
} from '@/schemas/api/migration';
import { HttpUtil, PromiseUtil } from '@/utils';

interface MigrateModalProps {
  open: boolean;
  onClose: () => void;
}

export default function MigrateModal({ open, onClose }: MigrateModalProps) {
  const { t } = useTranslation();
  const methods = useForm<MigrationFormValues>({
    defaultValues: { source: 'remnawave', url: '', token: '', applySub: true },
  });
  const source = useWatch({ control: methods.control, name: 'source' });
  const [file, setFile] = useState<File | null>(null);
  const [targets, setTargets] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<MigrationPreview | null>(null);
  const [result, setResult] = useState<MigrationResult | null>(null);
  const [loading, setLoading] = useState(false);

  function buildForm(nextTargets: Record<string, string>, applySub: boolean): FormData {
    const values = methods.getValues();
    const fd = new FormData();
    fd.append('source', values.source);
    if (file) fd.append('file', file);
    if (values.source === 'remnawave' && !file) {
      fd.append('url', values.url);
      fd.append('token', values.token);
    }
    fd.append('targets', JSON.stringify(nextTargets));
    fd.append('applySub', String(applySub));
    return fd;
  }

  async function runPreview(nextTargets: Record<string, string>) {
    setLoading(true);
    const msg = await HttpUtil.post('/panel/api/migration/preview', buildForm(nextTargets, false), {
      headers: { 'Content-Type': 'multipart/form-data' },
      silentSuccess: true,
    });
    setLoading(false);
    if (!msg?.success) return;
    const parsed = MigrationPreviewSchema.safeParse(msg.obj);
    if (!parsed.success) return;
    setPreview(parsed.data);
    const filled: Record<string, string> = { ...nextTargets };
    for (const s of parsed.data.servers) {
      if (!(s.key in filled)) filled[s.key] = s.suggested || 'skip';
    }
    setTargets(filled);
  }

  async function runApply() {
    setLoading(true);
    const msg = await HttpUtil.post(
      '/panel/api/migration/apply',
      buildForm(targets, methods.getValues().applySub),
      { headers: { 'Content-Type': 'multipart/form-data' } },
    );
    setLoading(false);
    if (!msg?.success) return;
    const parsed = MigrationResultSchema.safeParse(msg.obj);
    if (!parsed.success) return;
    setResult(parsed.data);
    if (parsed.data.panelRestart) {
      await PromiseUtil.sleep(5000);
      window.location.reload();
    }
  }

  function reset() {
    setPreview(null);
    setResult(null);
    setTargets({});
  }

  const targetOptions = [
    { value: 'skip', label: t('pages.index.migrate.targetSkip') },
    ...(preview?.targets ?? []).map((o) => ({
      value: o.value,
      label:
        o.value === 'local'
          ? t('pages.index.migrate.targetLocal')
          : `${o.name}${o.address ? ` (${o.address})` : ''}`,
    })),
  ];
  const errors = result
    ? [
        ...result.inboundErrors.map((e) => `${e.name}: ${e.error}`),
        ...result.clients.skipped.map((e) => `${e.email}: ${e.reason}`),
        ...result.hostErrors.map((e) => `${e.name}: ${e.error}`),
      ]
    : [];

  return (
    <Modal
      open={open}
      title={t('pages.index.migrate.title')}
      width={760}
      onCancel={onClose}
      footer={
        result ? (
          <Button onClick={onClose}>{t('close')}</Button>
        ) : (
          <Space>
            <Button
              loading={loading}
              onClick={() => runPreview(targets)}
              disabled={source === '3x-ui' && !file}
            >
              {t('pages.index.migrate.check')}
            </Button>
            <Button type="primary" loading={loading} disabled={!preview} onClick={runApply}>
              {t('pages.index.migrate.apply')}
            </Button>
          </Space>
        )
      }
    >
      <FormProvider {...methods}>
        <Form layout="vertical">
          <FormField name="source" label={t('pages.index.migrate.source')} onAfterChange={reset}>
            <Radio.Group
              options={[
                { value: 'remnawave', label: 'Remnawave' },
                { value: '3x-ui', label: '3x-ui' },
              ]}
            />
          </FormField>
          {source === 'remnawave' && !file && (
            <>
              <FormField name="url" label={t('pages.index.migrate.url')} onAfterChange={reset}>
                <Input placeholder="https://panel.example.com" />
              </FormField>
              <FormField
                name="token"
                label={t('pages.index.migrate.token')}
                extra={t('pages.index.migrate.tokenDesc')}
                onAfterChange={reset}
              >
                <Input.Password autoComplete="off" />
              </FormField>
            </>
          )}
          <Form.Item
            label={
              source === 'remnawave'
                ? t('pages.index.migrate.snapshotFile')
                : t('pages.index.migrate.xuiFile')
            }
          >
            <Upload
              maxCount={1}
              accept={source === 'remnawave' ? '.json' : '.db'}
              beforeUpload={(f) => {
                setFile(f);
                reset();
                return false;
              }}
              onRemove={() => {
                setFile(null);
                reset();
              }}
            >
              <Button icon={<UploadOutlined aria-hidden />}>
                {t('pages.index.migrate.chooseFile')}
              </Button>
            </Upload>
          </Form.Item>

          {preview && !result && (
            <>
              <Table
                size="small"
                pagination={false}
                rowKey="key"
                dataSource={preview.servers}
                columns={[
                  { title: t('pages.index.migrate.server'), dataIndex: 'name' },
                  {
                    title: t('pages.index.migrate.address'),
                    dataIndex: 'address',
                  },
                  {
                    title: t('pages.index.migrate.inbounds'),
                    dataIndex: 'inbounds',
                  },
                  {
                    title: t('pages.index.migrate.target'),
                    key: 'target',
                    render: (_, s) => (
                      <Select
                        aria-label={`${t('pages.index.migrate.target')}: ${s.name}`}
                        style={{ minWidth: 200 }}
                        value={targets[s.key] ?? 'skip'}
                        options={targetOptions}
                        onChange={(v) => runPreview({ ...targets, [s.key]: v })}
                      />
                    ),
                  },
                ]}
              />
              <Table
                style={{ marginTop: 12 }}
                size="small"
                pagination={false}
                rowKey="key"
                dataSource={preview.inbounds.filter((i) => targets[i.serverKey] !== 'skip')}
                columns={[
                  {
                    title: t('pages.index.migrate.inbound'),
                    dataIndex: 'remark',
                  },
                  { title: t('protocol'), dataIndex: 'protocol' },
                  { title: t('pages.index.migrate.port'), dataIndex: 'port' },
                  {
                    title: t('pages.index.migrate.clients'),
                    dataIndex: 'clients',
                  },
                  {
                    title: t('pages.index.migrate.conflict'),
                    dataIndex: 'conflict',
                    render: (v?: string) => v ?? '',
                  },
                ]}
              />
              <Alert
                style={{ marginTop: 12 }}
                type="info"
                title={t('pages.index.migrate.summary', {
                  total: preview.clients.total,
                  enabled: preview.clients.enabled,
                  hosts: preview.hosts,
                })}
                description={
                  preview.sub.exampleUrl
                    ? t('pages.index.migrate.subDesc', {
                        url: preview.sub.exampleUrl,
                      })
                    : undefined
                }
              />
              <FormField name="applySub" valueProp="checked" style={{ marginTop: 12 }}>
                <Checkbox>{t('pages.index.migrate.applySub')}</Checkbox>
              </FormField>
              {preview.warnings.length > 0 && (
                <Alert
                  type="warning"
                  title={t('pages.index.migrate.warnings')}
                  description={
                    <ul style={{ margin: 0, paddingInlineStart: 18 }}>
                      {preview.warnings.map((w) => (
                        <li key={w}>{w}</li>
                      ))}
                    </ul>
                  }
                />
              )}
            </>
          )}

          {result && (
            <Space orientation="vertical" style={{ width: '100%' }}>
              <Alert
                type="success"
                title={t('pages.index.migrate.done', {
                  inbounds: result.inboundsCreated,
                  clients: result.clients.created,
                  hosts: result.hostsCreated,
                })}
              />
              {errors.length > 0 && (
                <Alert
                  type="warning"
                  title={t('pages.index.migrate.notImported')}
                  description={
                    <ul style={{ margin: 0, paddingInlineStart: 18 }}>
                      {errors.map((e) => (
                        <li key={e}>{e}</li>
                      ))}
                    </ul>
                  }
                />
              )}
            </Space>
          )}
        </Form>
      </FormProvider>
    </Modal>
  );
}
