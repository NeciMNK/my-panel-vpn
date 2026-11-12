import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  ConfigProvider,
  Layout,
  Result,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { TableColumnsType } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';

import { useTheme } from '@/hooks/useTheme';
import { usePageTitle } from '@/hooks/usePageTitle';
import { HttpUtil } from '@/utils';
import { setMessageInstance } from '@/utils/messageBus';
import AppSidebar from '@/layouts/AppSidebar';
import { GeoTokenInput } from '@/components/geodata';
import { keys } from '@/api/queryKeys';
import { parseMsg } from '@/utils/zodValidate';
import {
  AIDomainsViewSchema,
  type AIDomainSuggestion,
  type AIDomainsView,
  type AISuggestStatus,
} from '@/schemas/aiDomains';

const JSON_HEADERS = {
  headers: { 'Content-Type': 'application/json' },
} as const;
const ANSWERS: AISuggestStatus[] = ['added', 'partial', 'declined', 'present'];
const STATUS_COLORS: Record<AISuggestStatus, string> = {
  pending: 'gold',
  added: 'green',
  partial: 'blue',
  declined: 'default',
  present: 'cyan',
};

async function fetchView(): Promise<AIDomainsView> {
  const msg = await HttpUtil.get('/panel/api/aiDomains/list', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to load the AI domains');
  const validated = parseMsg(msg, AIDomainsViewSchema, 'aiDomains/list');
  if (!validated.obj) throw new Error('Empty AI domains');
  return validated.obj;
}

export default function AIDomainsPage() {
  usePageTitle();
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);
  const queryClient = useQueryClient();

  const viewQuery = useQuery({ queryKey: keys.aiDomains.list(), queryFn: fetchView });
  const view = viewQuery.data;
  const [draft, setDraft] = useState<string | undefined>();
  const domains = draft ?? view?.domains ?? '';

  const post = useMutation({
    mutationFn: ({ path, body }: { path: string; body: unknown }) =>
      HttpUtil.post(`/panel/api/aiDomains/${path}`, body, JSON_HEADERS),
    onSuccess: (msg, { path }) => {
      if (!msg?.success) return;
      if (path === 'save') setDraft(undefined);
      queryClient.invalidateQueries({ queryKey: keys.aiDomains.root() });
    },
  });

  const columns: TableColumnsType<AIDomainSuggestion> = [
    {
      title: t('pages.aiDomains.date'),
      dataIndex: 'createdAt',
      width: 150,
      render: (at: number) => dayjs.unix(at).format('YYYY-MM-DD HH:mm'),
    },
    { title: t('pages.aiDomains.client'), dataIndex: 'email', width: 160 },
    {
      title: t('pages.aiDomains.domains'),
      dataIndex: 'domains',
      render: (list: string) => (
        <Space size={[4, 4]} wrap>
          {list.split(',').map((d) => (
            <Tag key={d}>{d}</Tag>
          ))}
        </Space>
      ),
    },
    {
      title: t('pages.aiDomains.status'),
      dataIndex: 'status',
      render: (status: AISuggestStatus, row) =>
        status === 'pending' ? (
          <Space size={[4, 4]} wrap>
            {ANSWERS.map((answer) => (
              <Button
                key={answer}
                size="small"
                type={answer === 'added' ? 'primary' : 'default'}
                loading={post.isPending}
                onClick={() =>
                  post.mutate({
                    path: 'suggestions/answer',
                    body: { id: row.id, status: answer },
                  })
                }
              >
                {t(`pages.aiDomains.${answer}`)}
              </Button>
            ))}
          </Space>
        ) : (
          <Tag color={STATUS_COLORS[status]}>{t(`pages.aiDomains.${status}`)}</Tag>
        ),
    },
  ];

  const pageClass = useMemo(() => {
    const classes = ['ai-domains-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin aria-live="off" spinning={!view && !viewQuery.isError} delay={200}>
              {viewQuery.isError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={(viewQuery.error as Error).message}
                  extra={
                    <Button type="primary" onClick={() => viewQuery.refetch()}>
                      {t('refresh')}
                    </Button>
                  }
                />
              ) : (
                <Space orientation="vertical" size={16} style={{ width: '100%' }}>
                  <Card title={t('pages.aiDomains.list')}>
                    <Typography.Paragraph type="secondary">
                      {t('pages.aiDomains.listDesc')}
                    </Typography.Paragraph>
                    <Space orientation="vertical" style={{ width: '100%' }}>
                      <GeoTokenInput
                        kind="domain"
                        value={domains}
                        placeholder="domain:openai.com, geosite:openai"
                        onChange={setDraft}
                      />
                      <Button
                        type="primary"
                        disabled={draft === undefined}
                        loading={post.isPending}
                        onClick={() => post.mutate({ path: 'save', body: { domains } })}
                      >
                        {t('save')}
                      </Button>
                    </Space>
                  </Card>
                  <Card title={t('pages.aiDomains.suggestions')}>
                    <Typography.Paragraph type="secondary">
                      {t('pages.aiDomains.suggestionsDesc')}
                    </Typography.Paragraph>
                    <Table
                      rowKey="id"
                      size="small"
                      columns={columns}
                      dataSource={view?.suggestions ?? []}
                      pagination={{ pageSize: 20, hideOnSinglePage: true }}
                      scroll={{ x: 'max-content' }}
                    />
                  </Card>
                </Space>
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
