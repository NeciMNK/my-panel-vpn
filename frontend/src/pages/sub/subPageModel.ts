const DAY_MS = 86_400_000;

export type SubStatus = 'active' | 'unlimited' | 'expired' | 'depleted' | 'disabled';

export interface SubUsage {
  enabled: boolean;
  usedByte: number;
  totalByte: number;
  expireMs: number;
}

export function resolveSubStatus(sub: SubUsage, now: number): SubStatus {
  if (!sub.enabled) return 'disabled';
  if (sub.expireMs > 0 && now >= sub.expireMs) return 'expired';
  if (sub.totalByte > 0 && sub.usedByte >= sub.totalByte) return 'depleted';
  if (sub.totalByte <= 0 && sub.expireMs === 0) return 'unlimited';
  return 'active';
}

export function daysUntil(expireMs: number, now: number): number | null {
  if (expireMs <= 0) return null;
  return Math.max(0, Math.ceil((expireMs - now) / DAY_MS));
}

export function usagePercent(usedByte: number, totalByte: number): number {
  if (totalByte <= 0) return 0;
  const pct = (usedByte / totalByte) * 100;
  return Number.isFinite(pct) ? Math.min(100, Math.max(0, pct)) : 0;
}

export const APP_PLATFORMS = ['ios', 'android', 'windows', 'macos', 'linux'] as const;
export type AppPlatform = (typeof APP_PLATFORMS)[number];

export function detectPlatform(userAgent: string, maxTouchPoints: number): AppPlatform {
  if (/iphone|ipad|ipod/i.test(userAgent)) return 'ios';
  if (/android/i.test(userAgent)) return 'android';
  // iPadOS Safari sends a desktop Macintosh UA; a touch screen is the only tell.
  if (/macintosh|mac os x/i.test(userAgent)) return maxTouchPoints > 1 ? 'ios' : 'macos';
  if (/windows/i.test(userAgent)) return 'windows';
  if (/linux|x11|cros/i.test(userAgent)) return 'linux';
  return 'android';
}

export interface SubApp {
  name: string;
  // Empty when the app has no import URL scheme: the page falls back to copying the link.
  importUrl: string;
  installUrl: string;
  store: string;
}

export interface SubAppSource {
  subUrl: string;
  subClashUrl: string;
  sId: string;
  subTitle: string;
}

const appStore = (id: string) => `https://apps.apple.com/app/id${id}`;
const googlePlay = (pkg: string) => `https://play.google.com/store/apps/details?id=${pkg}`;
const githubLatest = (repo: string) => `https://github.com/${repo}/releases/latest`;

function storeName(installUrl: string): string {
  const host = new URL(installUrl).hostname;
  if (host === 'apps.apple.com') return 'App Store';
  if (host === 'play.google.com') return 'Google Play';
  if (host === 'github.com') return 'GitHub';
  return host;
}

const app = (name: string, importUrl: string, installUrl: string): SubApp => ({
  name,
  importUrl,
  installUrl,
  store: storeName(installUrl),
});

export function buildSubApps({
  subUrl,
  subClashUrl,
  sId,
  subTitle,
}: SubAppSource): Record<AppPlatform, SubApp[]> {
  const encSub = encodeURIComponent(subUrl);
  const profileName = encodeURIComponent(subTitle || sId);

  const happImport = `happ://add/${subUrl}`;
  const happDesktop = app('Happ', happImport, githubLatest('Happ-proxy/happ-desktop'));
  const hiddifyImport = `hiddify://import/${subUrl}#${profileName}`;
  const hiddifyDesktop = app('Hiddify', hiddifyImport, githubLatest('hiddify/hiddify-app'));
  const clashVerge = subClashUrl
    ? [
        app(
          'Clash Verge',
          `clash://install-config?url=${encodeURIComponent(subClashUrl)}&name=${profileName}`,
          githubLatest('clash-verge-rev/clash-verge-rev'),
        ),
      ]
    : [];
  const v2rayN = app('v2rayN', '', githubLatest('2dust/v2rayN'));

  const v2raytunImport = `v2raytun://import/${subUrl}`;
  const v2boxImport = `v2box://install-sub?url=${encSub}&name=${encodeURIComponent(sId)}`;
  const streisandImport = `streisand://import/${encSub}`;
  const incyImport = `incy://add/${subUrl}`;
  const rocketSource = `${subUrl}${subUrl.includes('?') ? '&' : '?'}flag=shadowrocket`;
  const rocketRemark = encodeURIComponent(subTitle || sId || 'Subscription');

  return {
    ios: [
      app('Happ', happImport, appStore('6504287215')),
      app('Streisand', streisandImport, appStore('6450534064')),
      app('V2RayTun', v2raytunImport, appStore('6476628951')),
      app('V2Box', v2boxImport, appStore('6446814690')),
      app(
        'Shadowrocket',
        `shadowrocket://add/sub://${btoa(rocketSource)}?remark=${rocketRemark}`,
        appStore('932747118'),
      ),
      app('Incy', incyImport, appStore('6756943388')),
    ],
    android: [
      app('Happ', happImport, googlePlay('com.happproxy')),
      app('V2RayTun', v2raytunImport, googlePlay('com.v2raytun.android')),
      app('V2RayNG', `v2rayng://install-config?url=${encSub}`, googlePlay('com.v2ray.ang')),
      app('Hiddify', hiddifyImport, googlePlay('app.hiddify.com')),
      app(
        'Sing-box',
        `sing-box://import-remote-profile?url=${encSub}#${profileName}`,
        googlePlay('io.nekohasekai.sfa'),
      ),
      app('V2Box', v2boxImport, googlePlay('dev.hexasoftware.v2box')),
    ],
    windows: [happDesktop, hiddifyDesktop, ...clashVerge, v2rayN],
    macos: [
      app('Happ', happImport, appStore('6504287215')),
      app('Streisand', streisandImport, appStore('6450534064')),
      app('V2Box', v2boxImport, appStore('6446814690')),
      hiddifyDesktop,
      ...clashVerge,
      v2rayN,
    ],
    linux: [happDesktop, hiddifyDesktop, ...clashVerge, v2rayN],
  };
}
