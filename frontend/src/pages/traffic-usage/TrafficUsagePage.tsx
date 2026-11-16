import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  ConfigProvider,
  Input,
  Layout,
  Result,
  Space,
  Spin,
  Table,
  Tooltip,
  Typography,
} from 'antd';
import type { TableColumnsType } from 'antd';
import { useQuery } from '@tanstack/react-query';

import { useTheme } from '@/hooks/useTheme';
import { usePageTitle } from '@/hooks/usePageTitle';
import { HttpUtil, SizeFormatter } from '@/utils';
import AppSidebar from '@/layouts/AppSidebar';
import { keys } from '@/api/queryKeys';
import { parseMsg } from '@/utils/zodValidate';
import {
  TrafficUsageReportSchema,
  type TrafficUsageReport,
  type TrafficUsageRow,
} from '@/generated/zod';

async function fetchReport(): Promise<TrafficUsageReport> {
  const msg = await HttpUtil.get('/panel/api/trafficUsage/report', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to load the traffic report');
  const validated = parseMsg(msg, TrafficUsageReportSchema, 'trafficUsage/report');
  if (!validated.obj) throw new Error('Empty traffic report');
  return validated.obj;
}

function total(row: TrafficUsageRow, i: number): number {
  const u = row.usage[i];
  return u ? u.up + u.down : 0;
}

export default function TrafficUsagePage() {
  usePageTitle();
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [search, setSearch] = useState('');

  const query = useQuery({
    queryKey: keys.trafficUsage.report(),
    queryFn: fetchReport,
    refetchInterval: 60_000,
  });
  const report = query.data;

  const columns = useMemo<TableColumnsType<TrafficUsageRow>>(() => {
    const periods = report?.periods ?? [];
    return [
      {
        title: t('pages.trafficUsage.name'),
        dataIndex: 'name',
        fixed: 'left',
        render: (name: string) => name || t('pages.trafficUsage.thisPanel'),
      },
      ...periods.map((p, i) => ({
        title: t(`pages.trafficUsage.periods.${p}`),
        key: p,
        align: 'right' as const,
        sorter: (a: TrafficUsageRow, b: TrafficUsageRow) => total(a, i) - total(b, i),
        render: (_: unknown, row: TrafficUsageRow) => (
          <Tooltip
            title={`↑ ${SizeFormatter.sizeFormat(row.usage[i]?.up)} ↓ ${SizeFormatter.sizeFormat(row.usage[i]?.down)}`}
          >
            {SizeFormatter.sizeFormat(total(row, i))}
          </Tooltip>
        ),
      })),
    ];
  }, [report?.periods, t]);

  const clients = useMemo(() => {
    const needle = search.trim().toLowerCase();
    const rows = report?.clients ?? [];
    return needle ? rows.filter((r) => r.subject.toLowerCase().includes(needle)) : rows;
  }, [report?.clients, search]);

  const pageClass = useMemo(() => {
    const classes = ['traffic-usage-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin aria-live="off" spinning={!report && !query.isError} delay={200}>
              {query.isError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={(query.error as Error).message}
                  extra={
                    <Button type="primary" onClick={() => query.refetch()}>
                      {t('refresh')}
                    </Button>
                  }
                />
              ) : (
                <Space orientation="vertical" size={16} style={{ width: '100%' }}>
                  <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                    {t('pages.trafficUsage.desc')}
                  </Typography.Paragraph>
                  <Card title={t('pages.trafficUsage.servers')}>
                    <Table
                      rowKey="subject"
                      size="small"
                      columns={columns}
                      dataSource={report?.servers ?? []}
                      pagination={false}
                      scroll={{ x: 'max-content' }}
                    />
                  </Card>
                  <Card
                    title={t('pages.trafficUsage.clients')}
                    extra={
                      <Input.Search
                        allowClear
                        aria-label={t('pages.trafficUsage.search')}
                        placeholder={t('pages.trafficUsage.search')}
                        onChange={(e) => setSearch(e.target.value)}
                      />
                    }
                  >
                    <Table
                      rowKey="subject"
                      size="small"
                      columns={columns}
                      dataSource={clients}
                      pagination={{ pageSize: 50, hideOnSinglePage: true }}
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
