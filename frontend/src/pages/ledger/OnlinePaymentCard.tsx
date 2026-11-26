import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Divider, Input, InputNumber, Select, Space, Switch } from 'antd';

import { HttpUtil } from '@/utils';
import { onNumber } from '@/utils/onNumber';
import { SettingListItem } from '@/components/ui';
import { selectTagRender } from '@/components/form/SelectTag';
import { useAllSettings } from '@/api/queries/useAllSettings';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import SecretInput from '@/pages/settings/SecretInput';

// The bot's free plan and the acquiring provider that confirms card payments by
// itself; saved as panel settings, so it shares the settings page's store.
export default function OnlinePaymentCard() {
  const { t } = useTranslation();
  const { allSetting, updateSetting, fetched, spinning, saveDisabled, saveAll } = useAllSettings();
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

  return (
    <Card
      size="small"
      loading={!fetched}
      title={t('pages.settings.tgPayment')}
      extra={
        <Button
          size="small"
          type="primary"
          disabled={saveDisabled}
          loading={spinning}
          onClick={() => saveAll()}
        >
          {t('save')}
        </Button>
      }
    >
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
    </Card>
  );
}
