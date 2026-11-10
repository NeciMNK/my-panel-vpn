import { useState } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Segmented, Tag } from 'antd';
import {
  AndroidOutlined,
  AppleOutlined,
  CheckCircleOutlined,
  CopyOutlined,
  DownloadOutlined,
  LinuxOutlined,
  PlusCircleOutlined,
  WindowsOutlined,
} from '@ant-design/icons';

import { APP_ICONS } from './appIcons';
import { APP_PLATFORMS } from './subPageModel';
import type { AppPlatform, SubApp } from './subPageModel';

interface SubAppsTabProps {
  apps: Record<AppPlatform, SubApp[]>;
  detectedPlatform: AppPlatform;
  subUrl: string;
  onImport: (url: string) => void;
  onCopy: (value: string) => void;
}

const PLATFORM_META: Record<AppPlatform, { label: string; icon: ReactNode }> = {
  ios: { label: 'iOS', icon: <AppleOutlined aria-hidden /> },
  android: { label: 'Android', icon: <AndroidOutlined aria-hidden /> },
  windows: { label: 'Windows', icon: <WindowsOutlined aria-hidden /> },
  macos: { label: 'macOS', icon: <AppleOutlined aria-hidden /> },
  linux: { label: 'Linux', icon: <LinuxOutlined aria-hidden /> },
};

function AppIcon({ name }: { name: string }) {
  const icon = APP_ICONS[name];
  if (!icon) {
    return (
      <span className="sub-app-mark" aria-hidden="true">
        {name.charAt(0)}
      </span>
    );
  }
  if (icon.tinted) {
    const mask = `url("${icon.src}")`;
    return (
      <span className="sub-app-mark" aria-hidden="true">
        <span className="sub-app-glyph" style={{ maskImage: mask, WebkitMaskImage: mask }} />
      </span>
    );
  }
  return <img className="sub-app-logo" src={icon.src} alt="" width={32} height={32} />;
}

export default function SubAppsTab({
  apps,
  detectedPlatform,
  subUrl,
  onImport,
  onCopy,
}: SubAppsTabProps) {
  const { t } = useTranslation();
  const [platform, setPlatform] = useState<AppPlatform>(detectedPlatform);
  const [appName, setAppName] = useState('');
  const platformApps = apps[platform];
  const selected = platformApps.find((app) => app.name === appName) ?? platformApps[0];

  const platformOptions = APP_PLATFORMS.map((key) => ({
    value: key,
    label: PLATFORM_META[key].label,
    icon: PLATFORM_META[key].icon,
  }));

  return (
    <div className="sub-apps">
      <div className="sub-platforms">
        <Segmented<AppPlatform> value={platform} onChange={setPlatform} options={platformOptions} />
        {platform === detectedPlatform && (
          <span className="sub-muted">
            <CheckCircleOutlined aria-hidden /> {t('subscription.platformDetected')}
          </span>
        )}
      </div>

      <div className="sub-app-grid" role="radiogroup">
        {platformApps.map((app, index) => (
          <button
            key={app.name}
            type="button"
            role="radio"
            aria-checked={app.name === selected?.name}
            className={`sub-row sub-app-option${app.name === selected?.name ? ' is-selected' : ''}`}
            onClick={() => setAppName(app.name)}
          >
            <AppIcon name={app.name} />
            <span className="sub-app-name">{app.name}</span>
            {index === 0 && (
              <Tag color="green" className="sub-row-tag">
                {t('subscription.recommended')}
              </Tag>
            )}
          </button>
        ))}
      </div>

      {selected && (
        <ol className="sub-steps">
          <li className="sub-step">
            <div className="sub-step-title">{t('subscription.stepInstall')}</div>
            <div className="sub-muted">
              {t('subscription.stepInstallHint', { app: selected.name })}
            </div>
            <Button
              href={selected.installUrl}
              target="_blank"
              rel="noopener noreferrer"
              icon={<DownloadOutlined aria-hidden />}
            >
              {selected.store}
            </Button>
          </li>
          <li className="sub-step">
            <div className="sub-step-title">{t('subscription.stepAdd')}</div>
            <div className="sub-muted">
              {selected.importUrl
                ? t('subscription.stepAddHint', { app: selected.name })
                : t('subscription.stepAddCopyHint', { app: selected.name })}
            </div>
            <div className="sub-step-actions">
              {selected.importUrl && (
                <Button
                  type="primary"
                  icon={<PlusCircleOutlined aria-hidden />}
                  onClick={() => onImport(selected.importUrl)}
                >
                  {t('subscription.addSubscription')}
                </Button>
              )}
              <Button
                type={selected.importUrl ? 'default' : 'primary'}
                icon={<CopyOutlined aria-hidden />}
                onClick={() => onCopy(subUrl)}
              >
                {t('subscription.copyLink')}
              </Button>
            </div>
          </li>
          <li className="sub-step">
            <div className="sub-step-title">{t('subscription.stepConnect')}</div>
            <div className="sub-muted">
              {t('subscription.stepConnectHint', { app: selected.name })}
            </div>
          </li>
        </ol>
      )}
    </div>
  );
}
