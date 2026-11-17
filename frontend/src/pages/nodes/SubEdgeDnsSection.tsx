import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Divider, Form, Input, Select, Space, Tag, Typography } from 'antd';

import { selectTagRender } from '@/components/form';
import { HttpUtil } from '@/utils';
import type { SubEdgeDNSRequest, SubEdgeDNSView } from '@/generated/types';

const SUB_EDGE_DNS_KEY = ['subEdges', 'dns'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };
const TIER_KEYS = ['masters', 'ru', 'foreign'] as const;

async function fetchDns(): Promise<SubEdgeDNSView> {
  const msg = await HttpUtil.get<SubEdgeDNSView>('/panel/api/subEdges/dns', undefined, {
    silent: true,
  });
  if (!msg?.success || !msg.obj) throw new Error(msg?.msg || 'Failed to fetch subscription DNS');
  return msg.obj;
}

export default function SubEdgeDnsSection() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: view } = useQuery({
    queryKey: SUB_EDGE_DNS_KEY,
    queryFn: fetchDns,
    refetchInterval: 30000,
  });
  const [draft, setForm] = useState<SubEdgeDNSRequest | null>(null);
  const form = draft ?? { domain: view?.domain ?? '', token: '', masterIps: view?.masterIps ?? [] };

  const save = useMutation({
    mutationFn: (req: SubEdgeDNSRequest) =>
      HttpUtil.post('/panel/api/subEdges/dns', req, JSON_HEADERS),
    onSuccess: (msg) => {
      if (msg?.success) setForm(null);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: SUB_EDGE_DNS_KEY }),
  });

  const status = view?.status;
  const tierKey = status && status.tier >= 0 ? TIER_KEYS[status.tier] : undefined;

  return (
    <>
      <Divider titlePlacement="start">{t('pages.nodes.subEdge.dns.title')}</Divider>
      <Typography.Paragraph type="secondary">
        {t('pages.nodes.subEdge.dns.desc')}
      </Typography.Paragraph>
      <Form layout="vertical">
        <Form.Item label={t('pages.nodes.subEdge.dns.domain')}>
          <Input
            aria-label={t('pages.nodes.subEdge.dns.domain')}
            placeholder="sub.example.com"
            value={form.domain}
            onChange={(e) => setForm({ ...form, domain: e.target.value })}
          />
        </Form.Item>
        <Form.Item
          label={t('pages.nodes.subEdge.dns.token')}
          help={view?.tokenSet ? t('pages.nodes.subEdge.dns.tokenKeep') : undefined}
        >
          <Input.Password
            aria-label={t('pages.nodes.subEdge.dns.token')}
            autoComplete="new-password"
            value={form.token}
            onChange={(e) => setForm({ ...form, token: e.target.value })}
          />
        </Form.Item>
        <Form.Item
          label={t('pages.nodes.subEdge.dns.masterIps')}
          help={t('pages.nodes.subEdge.dns.masterIpsHint')}
        >
          <Select
            mode="tags"
            tagRender={selectTagRender}
            aria-label={t('pages.nodes.subEdge.dns.masterIps')}
            value={form.masterIps}
            open={false}
            onChange={(masterIps: string[]) => setForm({ ...form, masterIps })}
          />
        </Form.Item>
        <Button type="primary" loading={save.isPending} onClick={() => save.mutate(form)}>
          {t('save')}
        </Button>
      </Form>

      {view?.domain && status && status.at > 0 && (
        <Space orientation="vertical" style={{ width: '100%', marginTop: 16 }}>
          <Space wrap>
            {t('pages.nodes.subEdge.dns.tier')}:
            <Tag color={tierKey === 'masters' ? 'green' : tierKey ? 'orange' : 'default'}>
              {t(`pages.nodes.subEdge.dns.tiers.${tierKey ?? 'none'}`)}
            </Tag>
            {(status.published ?? []).map((ip) => (
              <Tag key={ip}>{ip}</Tag>
            ))}
          </Space>
          {status.error && <Alert type="error" showIcon message={status.error} />}
          {(status.unmanaged ?? []).length > 0 && (
            <Alert
              type="warning"
              showIcon
              message={t('pages.nodes.subEdge.dns.unmanaged', {
                ips: (status.unmanaged ?? []).join(', '),
              })}
            />
          )}
        </Space>
      )}
    </>
  );
}
