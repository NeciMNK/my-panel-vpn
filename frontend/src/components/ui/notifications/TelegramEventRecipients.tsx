import { Card, Select, Space, Typography } from 'antd';
import { TeamOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { useClientGroupOptions, useClientOptions } from '@/api/queries/useClientOptions';
import { selectTagRender } from '@/components/form';
import type { NotificationGroupConfig } from './types';

// Login alerts name the admins and their IPs, so they never go to clients.
const ADMIN_ONLY = ['login.attempt'];

interface Audience {
  groups?: string[];
  clients?: string[];
}

type Recipients = Record<string, Audience>;

function parseRecipients(raw: string): Recipients {
  try {
    const parsed: unknown = JSON.parse(raw || '{}');
    return parsed && typeof parsed === 'object' ? (parsed as Recipients) : {};
  } catch {
    return {};
  }
}

interface Props {
  groups: NotificationGroupConfig[];
  selected: string[];
  value: string;
  onChange: (next: string) => void;
}

export function TelegramEventRecipients({ groups: eventGroups, selected, value, onChange }: Props) {
  const { t } = useTranslation();
  const { data: groups = [] } = useClientGroupOptions();
  const { data: clients = [] } = useClientOptions();
  const recipients = parseRecipients(value);
  const shown = eventGroups.flatMap((g) =>
    g.events
      .filter((e) => selected.includes(e.key) && !ADMIN_ONLY.includes(e.key))
      .map((e) => ({
        key: e.key,
        name: `${t(`pages.settings.${g.title}`)} · ${t(`pages.settings.${e.label}`)}`,
      })),
  );

  function update(key: string, patch: Audience) {
    const next: Recipients = { ...recipients, [key]: { ...recipients[key], ...patch } };
    const audience = next[key];
    if (!audience?.groups?.length && !audience?.clients?.length) delete next[key];
    onChange(JSON.stringify(next));
  }

  return (
    <Card
      size="small"
      variant="outlined"
      title={
        <span>
          <TeamOutlined aria-hidden /> {t('pages.settings.tgRecipientsTitle')}
        </span>
      }
      style={{ marginTop: 12 }}
    >
      <Typography.Paragraph type="secondary">
        {t('pages.settings.tgRecipientsHint')}
      </Typography.Paragraph>
      <Space orientation="vertical" size={12} style={{ width: '100%' }}>
        {shown.map((event) => (
          <div key={event.key}>
            <Typography.Text strong>{event.name}</Typography.Text>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 4 }}>
              <Select
                mode="multiple"
                allowClear
                tagRender={selectTagRender}
                style={{ flex: '1 1 220px', minWidth: 0 }}
                placeholder={t('pages.settings.tgRecipientsGroups')}
                aria-label={`${event.name}: ${t('pages.settings.tgRecipientsGroups')}`}
                value={recipients[event.key]?.groups ?? []}
                options={groups.map((g) => ({ value: g, label: g }))}
                onChange={(next: string[]) => update(event.key, { groups: next })}
              />
              <Select
                mode="multiple"
                allowClear
                tagRender={selectTagRender}
                showSearch
                style={{ flex: '1 1 220px', minWidth: 0 }}
                placeholder={t('pages.settings.tgRecipientsClients')}
                aria-label={`${event.name}: ${t('pages.settings.tgRecipientsClients')}`}
                value={recipients[event.key]?.clients ?? []}
                options={clients.map((c) => ({ value: c, label: c }))}
                onChange={(next: string[]) => update(event.key, { clients: next })}
              />
            </div>
          </div>
        ))}
      </Space>
    </Card>
  );
}
