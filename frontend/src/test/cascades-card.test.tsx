import { describe, expect, it } from 'vitest';

import {
  countryFlag,
  emptyCascadeForm,
  exitOptions,
  nodeCountries,
} from '@/pages/nodes/CascadesCard';

describe('cascade form helpers', () => {
  it('starts on this panel as the entry with the ping strategy', () => {
    expect(emptyCascadeForm()).toMatchObject({
      entryNodeId: 0,
      exitNodeIds: [],
      strategy: 'leastPing',
    });
  });

  it('never offers the entry server as an exit', () => {
    const servers = [
      { value: 0, label: 'master' },
      { value: 2, label: 'de' },
      { value: 3, label: 'nl' },
    ];
    expect(exitOptions(servers, 2).map((s) => s.value)).toEqual([0, 3]);
  });
});

describe('country helpers', () => {
  it('collects distinct upper-cased countries of direct nodes only', () => {
    expect(
      nodeCountries([
        { country: 'de' },
        { country: 'DE' },
        { country: 'nl' },
        { country: 'FI', transitive: true },
        {},
      ]),
    ).toEqual(['DE', 'NL']);
  });

  it('turns a country code into its flag emoji', () => {
    expect(countryFlag('de')).toBe('🇩🇪');
    expect(countryFlag('xyz')).toBe('');
  });
});
