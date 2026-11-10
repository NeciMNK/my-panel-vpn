import { describe, expect, it } from 'vitest';

import {
  buildSubApps,
  daysUntil,
  detectPlatform,
  resolveSubStatus,
  usagePercent,
} from '@/pages/sub/subPageModel';

const DAY = 86_400_000;
const NOW = Date.UTC(2026, 8, 15, 12, 0, 0);

describe('resolveSubStatus', () => {
  const base = { enabled: true, usedByte: 10, totalByte: 100, expireMs: NOW + DAY };

  it.each([
    [
      'disabled wins over expiry and quota',
      { ...base, enabled: false, expireMs: NOW - DAY },
      'disabled',
    ],
    ['expired at the expiry instant', { ...base, expireMs: NOW }, 'expired'],
    ['expired beats depleted', { ...base, usedByte: 100, expireMs: NOW - DAY }, 'expired'],
    ['depleted once usage reaches the quota', { ...base, usedByte: 100 }, 'depleted'],
    [
      'unlimited with neither quota nor expiry',
      { ...base, totalByte: 0, expireMs: 0 },
      'unlimited',
    ],
    ['active without quota but with a future expiry', { ...base, totalByte: 0 }, 'active'],
    ['active inside quota and before expiry', base, 'active'],
  ] as const)('%s', (_name, input, want) => {
    expect(resolveSubStatus(input, NOW)).toBe(want);
  });
});

describe('daysUntil', () => {
  it('is null for a subscription that never expires', () => {
    expect(daysUntil(0, NOW)).toBeNull();
  });

  it('rounds the last partial day up to 1', () => {
    expect(daysUntil(NOW + 3 * 3_600_000, NOW)).toBe(1);
  });

  it('counts whole days', () => {
    expect(daysUntil(NOW + 23 * DAY, NOW)).toBe(23);
  });

  it('stays at 0 after expiry', () => {
    expect(daysUntil(NOW - DAY, NOW)).toBe(0);
  });
});

describe('usagePercent', () => {
  it('is 0 without a quota instead of NaN or Infinity', () => {
    expect(usagePercent(5_000, 0)).toBe(0);
  });

  it('clamps an over-quota client to 100', () => {
    expect(usagePercent(150, 100)).toBe(100);
  });
});

describe('detectPlatform', () => {
  it.each([
    [
      'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/128.0 Mobile Safari/537.36',
      0,
      'android',
    ],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148',
      5,
      'ios',
    ],
    // iPadOS Safari asks for the desktop site and reports itself as a Mac; only touch gives it away.
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15',
      5,
      'ios',
    ],
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15',
      0,
      'macos',
    ],
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/128.0 Safari/537.36',
      0,
      'windows',
    ],
    ['Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/128.0 Safari/537.36', 0, 'linux'],
    ['Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 Chrome/128.0', 0, 'linux'],
    ['', 0, 'android'],
  ] as const)('%s (touch %i) -> %s', (ua, touch, want) => {
    expect(detectPlatform(ua, touch)).toBe(want);
  });
});

describe('buildSubApps', () => {
  const subUrl = 'https://sub.example.com/sub/abc';
  const subClashUrl = 'https://sub.example.com/clash/abc';
  const encSub = encodeURIComponent(subUrl);
  const sub = { subUrl, subClashUrl, sId: 'abc', subTitle: 'Nova Net' };
  const imports = (list: { name: string; importUrl: string }[]) =>
    list.map(({ name, importUrl }) => [name, importUrl]);

  it('offers a list for every platform the page can detect', () => {
    expect(Object.keys(buildSubApps(sub))).toEqual(['ios', 'android', 'windows', 'macos', 'linux']);
  });

  it('gives every Android app a one-tap import link', () => {
    expect(imports(buildSubApps(sub).android)).toEqual([
      ['Happ', `happ://add/${subUrl}`],
      ['V2RayTun', `v2raytun://import/${subUrl}`],
      ['V2RayNG', `v2rayng://install-config?url=${encSub}`],
      ['Hiddify', `hiddify://import/${subUrl}#Nova%20Net`],
      ['Sing-box', `sing-box://import-remote-profile?url=${encSub}#Nova%20Net`],
      ['V2Box', `v2box://install-sub?url=${encSub}&name=abc`],
    ]);
  });

  it('gives every iOS app a one-tap import link', () => {
    const rocket = Buffer.from(`${subUrl}?flag=shadowrocket`).toString('base64');
    expect(imports(buildSubApps(sub).ios)).toEqual([
      ['Happ', `happ://add/${subUrl}`],
      ['Streisand', `streisand://import/${encSub}`],
      ['V2RayTun', `v2raytun://import/${subUrl}`],
      ['V2Box', `v2box://install-sub?url=${encSub}&name=abc`],
      ['Shadowrocket', `shadowrocket://add/sub://${rocket}?remark=Nova%20Net`],
      ['Incy', `incy://add/${subUrl}`],
    ]);
  });

  it('imports the Clash subscription into Clash Verge and leaves v2rayN to the clipboard', () => {
    expect(imports(buildSubApps(sub).windows)).toEqual([
      ['Happ', `happ://add/${subUrl}`],
      ['Hiddify', `hiddify://import/${subUrl}#Nova%20Net`],
      [
        'Clash Verge',
        `clash://install-config?url=${encodeURIComponent(subClashUrl)}&name=Nova%20Net`,
      ],
      ['v2rayN', ''],
    ]);
  });

  it('drops Clash Verge everywhere when the Clash subscription is disabled', () => {
    const apps = buildSubApps({ ...sub, subClashUrl: '' });
    const names = Object.values(apps).flatMap((list) => list.map((app) => app.name));
    expect(names).not.toContain('Clash Verge');
    expect(apps.linux.map((app) => app.name)).toEqual(['Happ', 'Hiddify', 'v2rayN']);
  });

  it('names where each install link leads', () => {
    const { ios, android, windows } = buildSubApps(sub);
    expect([ios[0], android[0], windows[0]].map((app) => [app.installUrl, app.store])).toEqual([
      ['https://apps.apple.com/app/id6504287215', 'App Store'],
      ['https://play.google.com/store/apps/details?id=com.happproxy', 'Google Play'],
      ['https://github.com/Happ-proxy/happ-desktop/releases/latest', 'GitHub'],
    ]);
  });

  it('names the profile after the subscription id when there is no title', () => {
    const android = buildSubApps({ ...sub, subTitle: '' }).android;
    expect(android.find((app) => app.name === 'Sing-box')?.importUrl).toBe(
      `sing-box://import-remote-profile?url=${encSub}#abc`,
    );
  });

  it('appends flag=shadowrocket with & when the subscription URL already has a query', () => {
    const withQuery = { ...sub, subUrl: `${subUrl}?token=1` };
    const rocket = Buffer.from(`${subUrl}?token=1&flag=shadowrocket`).toString('base64');
    expect(buildSubApps(withQuery).ios.find((app) => app.name === 'Shadowrocket')?.importUrl).toBe(
      `shadowrocket://add/sub://${rocket}?remark=Nova%20Net`,
    );
  });
});
