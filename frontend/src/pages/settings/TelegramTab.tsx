import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { Alert, Button, Divider, Input, InputNumber, Select, Space, Switch, Tabs } from 'antd';
import {
  BellOutlined,
  SendOutlined,
  SettingOutlined,
  WalletOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import { HttpUtil, LanguageManager } from '@/utils';
import { onNumber } from '@/utils/onNumber';
import type { AllSetting } from '@/models/setting';
import { SettingListItem } from '@/components/ui';
import { TelegramNotifications } from '@/components/ui/notifications/TelegramNotifications';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import { catTabLabel } from './catTabLabel';
import { NotifyTimeField } from './NotifyTimeField';
import SecretInput from './SecretInput';
import { selectTagRender } from '@/components/form/SelectTag';

interface TelegramTabProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
}

export default function TelegramTab({ allSetting, updateSetting }: TelegramTabProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { isMobile } = useMediaQuery();
  const [testLoading, setTestLoading] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; msg: string } | null>(null);
  const [inlineWarning, setInlineWarning] = useState<string | null>(null);
  const [privacyWarning, setPrivacyWarning] = useState<string | null>(null);
  const [capsLoading, setCapsLoading] = useState(false);
  const [payTestLoading, setPayTestLoading] = useState(false);
  const [payTestResult, setPayTestResult] = useState<{ success: boolean; msg: string } | null>(
    null,
  );
  const { data: inboundOptions = [] } = useInboundOptions();
  const freePlanInbounds = useMemo(
    () =>
      allSetting.tgFreePlanInbounds
        .split(',')
        .map((id) => Number(id.trim()))
        .filter((id) => id > 0),
    [allSetting.tgFreePlanInbounds],
  );

  // Checks the saved credentials, so the admin must save first; a sandbox shop says so.
  async function handleTestPayment() {
    setPayTestLoading(true);
    setPayTestResult(null);
    try {
      const res = (await HttpUtil.post('/panel/api/setting/testPayment')) as {
        success?: boolean;
        msg?: string;
      };
      setPayTestResult({ success: !!res.success, msg: res.msg || '' });
    } catch (e: unknown) {
      setPayTestResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setPayTestLoading(false);
    }
  }

  // The bot's lists live in inline mode, which only BotFather can enable, so the
  // panel checks the live capability and warns instead of leaving dead buttons.
  async function checkInlineMode() {
    setCapsLoading(true);
    try {
      const res = (await HttpUtil.post('/panel/api/setting/tgBotCapabilities')) as {
        success?: boolean;
        obj?: { inlineEnabled?: boolean; groupPrivacy?: boolean; username?: string };
      };
      if (res.success && res.obj && !res.obj.inlineEnabled) {
        setInlineWarning(res.obj.username || '');
      } else {
        setInlineWarning(null);
      }
      // Privacy mode is the mirror-image trap: with it on, a bare /start typed
      // in a group never reaches the bot, so the command looks dead.
      setPrivacyWarning(res.success && !!res.obj?.groupPrivacy ? res.obj?.username || '' : null);
    } catch {
      // The bot may simply be stopped; the token field still tells that story.
      setInlineWarning(null);
    } finally {
      setCapsLoading(false);
    }
  }

  async function handleTestTgBot() {
    setTestLoading(true);
    setTestResult(null);
    try {
      const res = (await HttpUtil.post('/panel/api/setting/testTgBot')) as {
        success?: boolean;
        msg?: string;
      };
      setTestResult({ success: !!res.success, msg: res.msg || '' });
    } catch (e: unknown) {
      setTestResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setTestLoading(false);
    }
  }

  const langOptions = useMemo(
    () =>
      LanguageManager.supportedLanguages.map(
        (l: { value: string; name: string; icon: string }) => ({
          value: l.value,
          label: (
            <>
              <span role="img" aria-label={l.name}>
                {l.icon}
              </span>
              &nbsp;&nbsp;<span>{l.name}</span>
            </>
          ),
        }),
      ),
    [],
  );

  return (
    <Tabs
      defaultActiveKey="1"
      items={[
        {
          key: '1',
          label: catTabLabel(
            <SettingOutlined aria-hidden />,
            t('pages.settings.panelSettings'),
            isMobile,
          ),
          children: (
            <>
              <SettingListItem
                paddings="small"
                title={t('pages.settings.telegramBotEnable')}
                description={t('pages.settings.telegramBotEnableDesc')}
              >
                <Switch
                  checked={allSetting.tgBotEnable}
                  onChange={(v) => updateSetting({ tgBotEnable: v })}
                />
              </SettingListItem>

              <SettingListItem
                paddings="small"
                title={t('pages.settings.telegramToken')}
                description={
                  allSetting.hasTgBotToken && !allSetting.clearTgBotToken
                    ? t('pages.settings.telegramTokenConfigured')
                    : t('pages.settings.telegramTokenDesc')
                }
              >
                <SecretInput
                  value={allSetting.tgBotToken}
                  configured={allSetting.hasTgBotToken}
                  clearArmed={allSetting.clearTgBotToken}
                  placeholder={t('pages.settings.telegramTokenPlaceholder')}
                  onChange={(v) => updateSetting({ tgBotToken: v })}
                  onClearArmedChange={(armed) => updateSetting({ clearTgBotToken: armed })}
                />
              </SettingListItem>

              <SettingListItem
                paddings="small"
                title={t('pages.settings.telegramChatId')}
                description={t('pages.settings.telegramChatIdDesc')}
              >
                <Input
                  value={allSetting.tgBotChatId}
                  onChange={(e) => updateSetting({ tgBotChatId: e.target.value })}
                />
              </SettingListItem>

              <SettingListItem paddings="small" title={t('pages.settings.telegramBotLanguage')}>
                <Select
                  value={allSetting.tgLang}
                  onChange={(v) => updateSetting({ tgLang: v })}
                  style={{ width: '100%' }}
                  options={langOptions}
                />
              </SettingListItem>

              <SettingListItem
                paddings="small"
                title={t('pages.settings.telegramAPIServer')}
                description={t('pages.settings.telegramAPIServerDesc')}
              >
                <Input
                  value={allSetting.tgBotAPIServer}
                  placeholder="https://api.example.com"
                  onChange={(e) => updateSetting({ tgBotAPIServer: e.target.value })}
                />
              </SettingListItem>

              <SettingListItem
                paddings="small"
                title={t('pages.settings.telegramVpnFallback')}
                description={t('pages.settings.telegramVpnFallbackDesc')}
              >
                <Switch
                  checked={allSetting.tgBotVpnFallback}
                  onChange={(v) => updateSetting({ tgBotVpnFallback: v })}
                />
              </SettingListItem>

              {inlineWarning !== null && (
                <Alert
                  type="warning"
                  showIcon
                  icon={<WarningOutlined aria-hidden />}
                  style={{ marginTop: 16 }}
                  title={t('pages.settings.tgInlineMissing')}
                  description={t('pages.settings.tgInlineMissingDesc')}
                  action={
                    <Button size="small" loading={capsLoading} onClick={checkInlineMode}>
                      {t('pages.settings.tgInlineRecheck')}
                    </Button>
                  }
                />
              )}

              {privacyWarning !== null && (
                <Alert
                  type="warning"
                  showIcon
                  icon={<WarningOutlined aria-hidden />}
                  style={{ marginTop: 16 }}
                  title={t('pages.settings.tgPrivacyOn')}
                  description={t('pages.settings.tgPrivacyOnDesc')}
                />
              )}

              <Space orientation="vertical" size={8} style={{ width: '100%', marginTop: 16 }}>
                <Button
                  type="primary"
                  icon={<SendOutlined aria-hidden />}
                  loading={testLoading || capsLoading}
                  onClick={() => {
                    setTestResult(null);
                    void handleTestTgBot();
                    void checkInlineMode();
                  }}
                >
                  {t('pages.settings.testTgBot')}
                </Button>
                {testResult && (
                  <Alert
                    type={testResult.success ? 'success' : 'error'}
                    title={testResult.msg}
                    showIcon
                    closable={{ onClose: () => setTestResult(null) }}
                  />
                )}
              </Space>
            </>
          ),
        },
        {
          key: '2',
          label: catTabLabel(
            <BellOutlined aria-hidden />,
            t('pages.settings.notifications'),
            isMobile,
          ),
          children: (
            <>
              <SettingListItem
                paddings="small"
                title={t('pages.settings.telegramNotifyTime')}
                description={t('pages.settings.telegramNotifyTimeDesc')}
              >
                <NotifyTimeField
                  value={allSetting.tgRunTime}
                  onChange={(v) => updateSetting({ tgRunTime: v })}
                />
              </SettingListItem>
              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgNotifyBackup')}
                description={t('pages.settings.tgNotifyBackupDesc')}
              >
                <Switch
                  checked={allSetting.tgBotBackup}
                  onChange={(v) => updateSetting({ tgBotBackup: v })}
                />
              </SettingListItem>

              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgEventBusNotify')}
                description={t('pages.settings.tgEventBusNotifyDesc')}
              >
                <TelegramNotifications allSetting={allSetting} updateSetting={updateSetting} />
              </SettingListItem>
            </>
          ),
        },
        {
          key: '3',
          label: catTabLabel(
            <WalletOutlined aria-hidden />,
            t('pages.settings.tgPayment'),
            isMobile,
          ),
          children: (
            <>
              <Alert
                type="info"
                showIcon
                title={t('pages.settings.tgPaymentMoved')}
                action={
                  <Button size="small" onClick={() => navigate('/ledger')}>
                    {t('menu.ledger')}
                  </Button>
                }
              />
              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgFreePlan')}
                description={t('pages.settings.tgFreePlanDesc')}
              >
                <Switch
                  checked={allSetting.tgFreePlanEnable}
                  onChange={(v) => updateSetting({ tgFreePlanEnable: v })}
                />
              </SettingListItem>
              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgFreePlanGB')}
                description={t('pages.settings.tgFreePlanGBDesc')}
              >
                <InputNumber
                  value={allSetting.tgFreePlanGB}
                  min={1}
                  max={100000}
                  style={{ width: '100%' }}
                  onChange={onNumber((v) => updateSetting({ tgFreePlanGB: v }))}
                />
              </SettingListItem>
              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgFreePlanPrice')}
                description={t('pages.settings.tgFreePlanPriceDesc')}
              >
                <InputNumber
                  value={allSetting.tgFreePlanPrice}
                  min={0}
                  max={100000}
                  precision={0}
                  style={{ width: '100%' }}
                  onChange={onNumber((v) => updateSetting({ tgFreePlanPrice: v }))}
                />
              </SettingListItem>
              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgFreePlanInbounds')}
                description={t('pages.settings.tgFreePlanInboundsDesc')}
              >
                <Select
                  mode="multiple"
                  tagRender={selectTagRender}
                  style={{ width: '100%' }}
                  aria-label={t('pages.settings.tgFreePlanInbounds')}
                  value={freePlanInbounds}
                  options={inboundOptions.map((ib) => ({
                    value: ib.id,
                    label: ib.remark || ib.tag || `#${ib.id}`,
                  }))}
                  onChange={(ids: number[]) => updateSetting({ tgFreePlanInbounds: ids.join(',') })}
                />
              </SettingListItem>

              <Divider titlePlacement="start">{t('pages.settings.tgPayOnline')}</Divider>
              <Alert
                type="info"
                showIcon
                title={t('pages.settings.tgPayHowTo')}
                style={{ marginBottom: 8 }}
              />
              <SettingListItem
                paddings="small"
                title={t('pages.settings.tgPayProvider')}
                description={t('pages.settings.tgPayProviderDesc')}
              >
                <Select
                  style={{ width: '100%' }}
                  aria-label={t('pages.settings.tgPayProvider')}
                  value={allSetting.tgPayProvider}
                  options={[
                    { value: '', label: t('pages.settings.tgPayManual') },
                    { value: 'yookassa', label: t('pages.settings.tgPayYooKassa') },
                  ]}
                  onChange={(v: string) => updateSetting({ tgPayProvider: v })}
                />
              </SettingListItem>
              {allSetting.tgPayProvider !== '' && (
                <>
                  <SettingListItem
                    paddings="small"
                    title={t('pages.settings.tgPayShopId')}
                    description={t('pages.settings.tgPayShopIdDesc')}
                  >
                    <Input
                      value={allSetting.tgPayShopId}
                      placeholder="123456"
                      onChange={(e) => updateSetting({ tgPayShopId: e.target.value.trim() })}
                    />
                  </SettingListItem>
                  <SettingListItem
                    paddings="small"
                    title={t('pages.settings.tgPaySecret')}
                    description={
                      allSetting.hasTgPaySecret && !allSetting.clearTgPaySecret
                        ? t('pages.settings.tgPaySecretConfigured')
                        : t('pages.settings.tgPaySecretDesc')
                    }
                  >
                    <SecretInput
                      value={allSetting.tgPaySecret}
                      configured={allSetting.hasTgPaySecret}
                      clearArmed={allSetting.clearTgPaySecret}
                      placeholder="live_..."
                      onChange={(v) => updateSetting({ tgPaySecret: v })}
                      onClearArmedChange={(armed) => updateSetting({ clearTgPaySecret: armed })}
                    />
                  </SettingListItem>
                  <Space orientation="vertical" style={{ width: '100%', padding: '8px 0' }}>
                    <Button loading={payTestLoading} onClick={handleTestPayment}>
                      {t('pages.settings.tgPayTest')}
                    </Button>
                    {payTestResult && (
                      <Alert
                        type={payTestResult.success ? 'success' : 'error'}
                        showIcon
                        title={payTestResult.msg}
                        closable={{ onClose: () => setPayTestResult(null) }}
                      />
                    )}
                  </Space>
                </>
              )}
            </>
          ),
        },
      ]}
    />
  );
}
