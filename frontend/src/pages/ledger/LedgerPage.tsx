import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  Col,
  ConfigProvider,
  DatePicker,
  Dropdown,
  Form,
  Input,
  InputNumber,
  Layout,
  Modal,
  Popconfirm,
  Result,
  Row,
  Select,
  Space,
  Spin,
  Statistic,
  Table,
  Tag,
  message,
} from 'antd';
import type { TableColumnsType } from 'antd';
import {
  CalendarOutlined,
  DeleteOutlined,
  EditOutlined,
  LeftOutlined,
  PlusOutlined,
  RightOutlined,
} from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FormProvider, useForm } from 'react-hook-form';
import dayjs from 'dayjs';

import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { usePageTitle } from '@/hooks/usePageTitle';
import { HttpUtil } from '@/utils';
import { setMessageInstance } from '@/utils/messageBus';
import AppSidebar from '@/layouts/AppSidebar';
import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { selectTagRender } from '@/components/form/SelectTag';
import { keys } from '@/api/queryKeys';
import { parseMsg } from '@/utils/zodValidate';
import { GroupSummaryListSchema, ClientRecordSchema } from '@/schemas/client';
import {
  LedgerEntryFormSchema,
  LedgerPaymentSchema,
  LedgerReportSchema,
  LedgerServerFormSchema,
  type LedgerEntry,
  type LedgerEntryFormValues,
  type LedgerNonPaying,
  type LedgerPayment,
  type LedgerReport,
  type LedgerServer,
  type LedgerServerFormValues,
} from '@/schemas/ledger';
import { z } from 'zod';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;
const MONTH_FORMAT = 'YYYY-MM';
const EXTEND_MONTHS = [1, 2, 3, 6, 12] as const;
const ClientEmailsSchema = z
  .array(ClientRecordSchema.pick({ email: true }))
  .nullable()
  .transform((v) => (v ?? []).map((c) => c.email));

async function fetchReport(month: string): Promise<LedgerReport> {
  const msg = await HttpUtil.get('/panel/api/ledger/report', { month }, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to load the ledger');
  const validated = parseMsg(msg, LedgerReportSchema, 'ledger/report');
  if (!validated.obj) throw new Error('Empty ledger report');
  return validated.obj;
}

function monthRange(server: LedgerServer, ongoing: string): string {
  return `${server.startMonth} — ${server.endMonth || ongoing}`;
}

function chargedIn(server: LedgerServer, month: string): boolean {
  return server.startMonth <= month && (server.endMonth === '' || month <= server.endMonth);
}

export default function LedgerPage() {
  usePageTitle();
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);
  const queryClient = useQueryClient();
  const [month, setMonth] = useState(() => dayjs().format(MONTH_FORMAT));

  const reportQuery = useQuery({
    queryKey: keys.ledger.report(month),
    queryFn: () => fetchReport(month),
  });
  const report = reportQuery.data;

  const groupsQuery = useQuery({
    queryKey: keys.clients.groups(),
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/clients/groups', undefined, { silent: true });
      return parseMsg(msg, GroupSummaryListSchema, 'clients/groups').obj ?? [];
    },
  });
  const emailsQuery = useQuery({
    queryKey: [...keys.ledger.root(), 'emails'],
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/clients/list', undefined, { silent: true });
      return parseMsg(msg, ClientEmailsSchema, 'clients/list').obj ?? [];
    },
    staleTime: 30_000,
  });

  const post = useMutation({
    mutationFn: ({ path, body }: { path: string; body: unknown }) =>
      HttpUtil.post(`/panel/api/ledger/${path}`, body, JSON_HEADERS),
    onSuccess: (msg) => {
      if (msg?.success) queryClient.invalidateQueries({ queryKey: keys.ledger.root() });
    },
  });

  const [serverOpen, setServerOpen] = useState(false);
  const serverForm = useForm<LedgerServerFormValues>();
  const [entryOpen, setEntryOpen] = useState(false);
  const entryForm = useForm<LedgerEntryFormValues>();
  const [markEmail, setMarkEmail] = useState<string | undefined>();
  const paymentForm = useForm<LedgerPayment>();
  const savedPayment = report?.payment;
  useEffect(() => {
    if (savedPayment) paymentForm.reset(savedPayment);
  }, [savedPayment, paymentForm]);

  function submitPayment(values: LedgerPayment) {
    post.mutate({ path: 'payment', body: values });
  }

  function openServer(server?: LedgerServer) {
    serverForm.reset(
      server ?? { id: 0, name: '', monthlyCost: 0, billingDay: 1, startMonth: month, endMonth: '' },
    );
    setServerOpen(true);
  }

  async function submitServer(values: LedgerServerFormValues) {
    const parsed = LedgerServerFormSchema.safeParse(values);
    if (!parsed.success) {
      serverForm.setError('endMonth', { message: parsed.error.issues[0]?.message });
      return;
    }
    const msg = await post.mutateAsync({ path: 'servers/save', body: parsed.data });
    if (msg?.success) setServerOpen(false);
  }

  function openEntry(kind: LedgerEntryFormValues['kind']) {
    entryForm.reset({ month, kind, amount: 0, email: '', note: '' });
    setEntryOpen(true);
  }

  async function submitEntry(values: LedgerEntryFormValues) {
    const msg = await post.mutateAsync({ path: 'entries/add', body: values });
    if (msg?.success) setEntryOpen(false);
  }

  function setFreeGroups(next: string[]) {
    const current = report?.freeGroups ?? [];
    for (const group of next.filter((g) => !current.includes(g))) {
      post.mutate({ path: 'nonPaying', body: { group, nonPaying: true } });
    }
    for (const group of current.filter((g) => !next.includes(g))) {
      post.mutate({ path: 'nonPaying', body: { group, nonPaying: false } });
    }
  }

  function markClient(email: string, nonPaying: boolean) {
    post.mutate({ path: 'nonPaying', body: { email, nonPaying } });
    setMarkEmail(undefined);
  }

  const currency = report?.currency ?? '';
  const money = (amount: number) => `${amount} ${currency}`.trim();

  const serverColumns: TableColumnsType<LedgerServer> = [
    { title: t('pages.ledger.serverName'), dataIndex: 'name' },
    {
      title: t('pages.ledger.monthlyCost'),
      dataIndex: 'monthlyCost',
      render: (cost: number) => money(cost),
    },
    { title: t('pages.ledger.billingDay'), dataIndex: 'billingDay' },
    {
      title: t('pages.ledger.nextPayment'),
      key: 'next',
      render: (_, server) => (
        <Space size={4} wrap>
          {report?.upcoming.find((due) => due.serverId === server.id)?.date ?? '—'}
          {server.paidThrough >= dayjs().format(MONTH_FORMAT) && (
            <Tag color="green">{t('pages.ledger.paidThrough', { month: server.paidThrough })}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: t('pages.ledger.period'),
      key: 'period',
      render: (_, server) => (
        <Space size={4}>
          {monthRange(server, t('pages.ledger.ongoing'))}
          {!chargedIn(server, month) && <Tag>{t('pages.ledger.notThisMonth')}</Tag>}
        </Space>
      ),
    },
    {
      key: 'actions',
      align: 'right',
      render: (_, server) => (
        <Space>
          <Dropdown
            trigger={['click']}
            menu={{
              items: EXTEND_MONTHS.map((count) => ({
                key: count,
                label: t('pages.ledger.extendMonths', { count }),
              })),
              onClick: ({ key }) =>
                post.mutate({
                  path: 'servers/extend',
                  body: { id: server.id, months: Number(key) },
                }),
            }}
          >
            <Button size="small" icon={<CalendarOutlined aria-hidden />}>
              {t('pages.ledger.extend')}
            </Button>
          </Dropdown>
          <Button
            size="small"
            icon={<EditOutlined aria-hidden />}
            aria-label={t('edit')}
            onClick={() => openServer(server)}
          />
          <Popconfirm
            title={t('pages.ledger.confirmDelete')}
            onConfirm={() => post.mutate({ path: 'servers/delete', body: { id: server.id } })}
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

  const entryColumns: TableColumnsType<LedgerEntry> = [
    {
      title: t('pages.ledger.kind'),
      dataIndex: 'kind',
      render: (kind: LedgerEntry['kind']) =>
        kind === 'income' ? (
          <Tag color="green">{t('pages.ledger.income')}</Tag>
        ) : (
          <Tag color="red">{t('pages.ledger.expense')}</Tag>
        ),
    },
    { title: t('pages.ledger.amount'), dataIndex: 'amount', render: (a: number) => money(a) },
    { title: t('pages.ledger.client'), dataIndex: 'email' },
    { title: t('pages.ledger.note'), dataIndex: 'note' },
    {
      key: 'actions',
      align: 'right',
      render: (_, entry) => (
        <Popconfirm
          title={t('pages.ledger.confirmDelete')}
          onConfirm={() => post.mutate({ path: 'entries/delete', body: { id: entry.id } })}
        >
          <Button
            size="small"
            danger
            icon={<DeleteOutlined aria-hidden />}
            aria-label={t('delete')}
          />
        </Popconfirm>
      ),
    },
  ];

  const nonPayingColumns: TableColumnsType<LedgerNonPaying> = [
    { title: t('pages.ledger.client'), dataIndex: 'email' },
    {
      title: t('pages.ledger.reason'),
      key: 'reason',
      render: (_, row) =>
        row.reason === 'group'
          ? `${t('pages.ledger.reasonGroup')}: ${row.group}`
          : t(
              row.reason === 'client' ? 'pages.ledger.reasonClient' : 'pages.ledger.reasonFreePlan',
            ),
    },
    {
      key: 'actions',
      align: 'right',
      render: (_, row) =>
        row.reason === 'client' && (
          <Button size="small" onClick={() => markClient(row.email, false)}>
            {t('pages.ledger.markPaying')}
          </Button>
        ),
    },
  ];

  const pageClass = useMemo(() => {
    const classes = ['ledger-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  const markable = (emailsQuery.data ?? []).filter(
    (email) => !report?.nonPaying.some((row) => row.email === email && row.reason === 'client'),
  );

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin aria-live="off" spinning={!report && !reportQuery.isError} delay={200}>
              {reportQuery.isError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={(reportQuery.error as Error).message}
                  extra={
                    <Button type="primary" onClick={() => reportQuery.refetch()}>
                      {t('refresh')}
                    </Button>
                  }
                />
              ) : (
                <Row gutter={[isMobile ? 8 : 16, isMobile ? 8 : 12]}>
                  <Col span={24}>
                    <Space wrap>
                      <Button
                        icon={<LeftOutlined aria-hidden />}
                        aria-label={t('pages.ledger.prevMonth')}
                        onClick={() =>
                          setMonth(dayjs(month).subtract(1, 'month').format(MONTH_FORMAT))
                        }
                      />
                      <DatePicker
                        picker="month"
                        aria-label={t('pages.ledger.month')}
                        allowClear={false}
                        value={dayjs(month)}
                        onChange={(value) => value && setMonth(value.format(MONTH_FORMAT))}
                      />
                      <Button
                        icon={<RightOutlined aria-hidden />}
                        aria-label={t('pages.ledger.nextMonth')}
                        onClick={() => setMonth(dayjs(month).add(1, 'month').format(MONTH_FORMAT))}
                      />
                    </Space>
                  </Col>
                  <Col span={24}>
                    <Card size="small" className="summary-card">
                      <Row gutter={[16, 12]}>
                        <Col xs={12} md={6}>
                          <Statistic
                            title={t('pages.ledger.income')}
                            value={money(report?.income ?? 0)}
                          />
                        </Col>
                        <Col xs={12} md={6}>
                          <Statistic
                            title={t('pages.ledger.expenses')}
                            value={money(report?.expenses ?? 0)}
                          />
                        </Col>
                        <Col xs={12} md={6}>
                          <Statistic
                            title={t('pages.ledger.balance')}
                            value={money(report?.balance ?? 0)}
                            styles={{
                              content: {
                                color: (report?.balance ?? 0) >= 0 ? '#3f8600' : '#cf1322',
                              },
                            }}
                          />
                        </Col>
                        <Col xs={12} md={6}>
                          <Statistic
                            title={t('pages.ledger.expected', {
                              count: report?.payingClients ?? 0,
                            })}
                            value={money(report?.expectedIncome ?? 0)}
                          />
                        </Col>
                      </Row>
                    </Card>
                  </Col>
                  <Col xs={24} xl={12}>
                    <Card
                      size="small"
                      title={t('pages.ledger.servers')}
                      extra={
                        <Button
                          size="small"
                          icon={<PlusOutlined aria-hidden />}
                          onClick={() => openServer()}
                        >
                          {t('add')}
                        </Button>
                      }
                    >
                      <Table
                        size="small"
                        rowKey="id"
                        pagination={false}
                        scroll={{ x: true }}
                        columns={serverColumns}
                        dataSource={report?.servers ?? []}
                      />
                    </Card>
                  </Col>
                  <Col xs={24} xl={12}>
                    <Card
                      size="small"
                      title={t('pages.ledger.entries')}
                      extra={
                        <Space>
                          <Button size="small" onClick={() => openEntry('income')}>
                            {t('pages.ledger.addIncome')}
                          </Button>
                          <Button size="small" onClick={() => openEntry('expense')}>
                            {t('pages.ledger.addExpense')}
                          </Button>
                        </Space>
                      }
                    >
                      <Table
                        size="small"
                        rowKey="id"
                        pagination={false}
                        scroll={{ x: true }}
                        columns={entryColumns}
                        dataSource={report?.entries ?? []}
                      />
                    </Card>
                  </Col>
                  <Col span={24}>
                    <Card
                      size="small"
                      title={t('pages.ledger.payment')}
                      extra={
                        <Button
                          size="small"
                          type="primary"
                          loading={post.isPending}
                          onClick={paymentForm.handleSubmit(submitPayment)}
                        >
                          {t('save')}
                        </Button>
                      }
                    >
                      <FormProvider {...paymentForm}>
                        <Form layout="vertical" component="div">
                          <Row gutter={16}>
                            <Col xs={14} md={6}>
                              <FormField
                                name="price"
                                label={t('pages.ledger.price')}
                                tooltip={t('pages.ledger.priceDesc')}
                                rules={{
                                  validate: rhfZodValidate(LedgerPaymentSchema.shape.price),
                                }}
                              >
                                <InputNumber min={0} precision={0} style={{ width: '100%' }} />
                              </FormField>
                            </Col>
                            <Col xs={10} md={4}>
                              <FormField name="currency" label={t('pages.ledger.currency')}>
                                <Input placeholder="RUB" />
                              </FormField>
                            </Col>
                            <Col xs={24} md={14}>
                              <FormField
                                name="discounts"
                                label={t('pages.ledger.discounts')}
                                tooltip={t('pages.ledger.discountsDesc')}
                              >
                                <Input placeholder="3:10, 6:15, 12:20" />
                              </FormField>
                            </Col>
                            <Col xs={24} md={6}>
                              <FormField
                                name="friendDiscount"
                                label={t('pages.ledger.friendDiscount')}
                                tooltip={t('pages.ledger.friendDiscountDesc')}
                                rules={{
                                  validate: rhfZodValidate(
                                    LedgerPaymentSchema.shape.friendDiscount,
                                  ),
                                }}
                              >
                                <InputNumber
                                  min={0}
                                  max={99}
                                  precision={0}
                                  addonAfter="%"
                                  style={{ width: '100%' }}
                                />
                              </FormField>
                            </Col>
                            <Col xs={24} md={6}>
                              <FormField
                                name="aiExitPrice"
                                label={t('pages.ledger.aiExitPrice')}
                                tooltip={t('pages.ledger.aiExitPriceDesc')}
                                rules={{
                                  validate: rhfZodValidate(LedgerPaymentSchema.shape.aiExitPrice),
                                }}
                              >
                                <InputNumber min={0} precision={0} style={{ width: '100%' }} />
                              </FormField>
                            </Col>
                            <Col xs={24} md={18}>
                              <FormField
                                name="details"
                                label={t('pages.ledger.details')}
                                tooltip={t('pages.ledger.detailsDesc')}
                              >
                                <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
                              </FormField>
                            </Col>
                          </Row>
                        </Form>
                      </FormProvider>
                    </Card>
                  </Col>
                  <Col span={24}>
                    <Card size="small" title={t('pages.ledger.nonPaying')}>
                      <Space direction="vertical" style={{ width: '100%' }}>
                        <Select
                          mode="multiple"
                          tagRender={selectTagRender}
                          style={{ width: '100%' }}
                          placeholder={t('pages.ledger.freeGroups')}
                          aria-label={t('pages.ledger.freeGroups')}
                          value={report?.freeGroups ?? []}
                          options={(groupsQuery.data ?? []).map((g) => ({
                            value: g.name,
                            label: g.name,
                          }))}
                          onChange={setFreeGroups}
                        />
                        <Select
                          showSearch
                          style={{ width: '100%' }}
                          placeholder={t('pages.ledger.markClient')}
                          aria-label={t('pages.ledger.markClient')}
                          value={markEmail}
                          options={markable.map((email) => ({ value: email, label: email }))}
                          onChange={(email: string) => markClient(email, true)}
                        />
                        <Table
                          size="small"
                          rowKey="email"
                          pagination={false}
                          columns={nonPayingColumns}
                          dataSource={report?.nonPaying ?? []}
                        />
                      </Space>
                    </Card>
                  </Col>
                </Row>
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>

      <Modal
        open={serverOpen}
        title={t('pages.ledger.serverTitle')}
        onCancel={() => setServerOpen(false)}
        onOk={serverForm.handleSubmit(submitServer)}
        confirmLoading={post.isPending}
        destroyOnHidden
      >
        <FormProvider {...serverForm}>
          <Form layout="vertical" component="div">
            <FormField
              name="name"
              label={t('pages.ledger.serverName')}
              rules={{ validate: rhfZodValidate(LedgerServerFormSchema.shape.name) }}
            >
              <Input />
            </FormField>
            <FormField
              name="monthlyCost"
              label={t('pages.ledger.monthlyCost')}
              rules={{ validate: rhfZodValidate(LedgerServerFormSchema.shape.monthlyCost) }}
            >
              <InputNumber min={1} style={{ width: '100%' }} addonAfter={currency || undefined} />
            </FormField>
            <FormField
              name="billingDay"
              label={t('pages.ledger.billingDay')}
              tooltip={t('pages.ledger.billingDayHint')}
              rules={{ validate: rhfZodValidate(LedgerServerFormSchema.shape.billingDay) }}
            >
              <InputNumber min={1} max={31} precision={0} style={{ width: '100%' }} />
            </FormField>
            <FormField
              name="startMonth"
              label={t('pages.ledger.startMonth')}
              rules={{ validate: rhfZodValidate(LedgerServerFormSchema.shape.startMonth) }}
            >
              <Input placeholder={MONTH_FORMAT} />
            </FormField>
            <FormField
              name="endMonth"
              label={t('pages.ledger.endMonth')}
              tooltip={t('pages.ledger.endMonthHint')}
              rules={{ validate: rhfZodValidate(LedgerServerFormSchema.shape.endMonth) }}
            >
              <Input placeholder={MONTH_FORMAT} />
            </FormField>
          </Form>
        </FormProvider>
      </Modal>

      <Modal
        open={entryOpen}
        title={t('pages.ledger.entryTitle')}
        onCancel={() => setEntryOpen(false)}
        onOk={entryForm.handleSubmit(submitEntry)}
        confirmLoading={post.isPending}
        destroyOnHidden
      >
        <FormProvider {...entryForm}>
          <Form layout="vertical" component="div">
            <FormField name="kind" label={t('pages.ledger.kind')}>
              <Select
                options={[
                  { value: 'income', label: t('pages.ledger.income') },
                  { value: 'expense', label: t('pages.ledger.expense') },
                ]}
              />
            </FormField>
            <FormField
              name="amount"
              label={t('pages.ledger.amount')}
              rules={{ validate: rhfZodValidate(LedgerEntryFormSchema.shape.amount) }}
            >
              <InputNumber min={1} style={{ width: '100%' }} addonAfter={currency || undefined} />
            </FormField>
            <FormField
              name="month"
              label={t('pages.ledger.month')}
              rules={{ validate: rhfZodValidate(LedgerEntryFormSchema.shape.month) }}
            >
              <Input placeholder={MONTH_FORMAT} />
            </FormField>
            <FormField name="email" label={t('pages.ledger.client')}>
              <Select
                allowClear
                showSearch
                options={(emailsQuery.data ?? []).map((email) => ({ value: email, label: email }))}
              />
            </FormField>
            <FormField name="note" label={t('pages.ledger.note')}>
              <Input />
            </FormField>
          </Form>
        </FormProvider>
      </Modal>
    </ConfigProvider>
  );
}
