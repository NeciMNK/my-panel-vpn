import { describe, expect, it } from 'vitest';

import {
  cascadeFormOf,
  countryFlag,
  emptyCascadeForm,
  toggleCascadeMode,
  exitOptions,
  nodeCountries,
} from '@/pages/nodes/CascadesCard';

describe('cascade form helpers', () => {
  it('starts on this panel as the entry with the ping strategy', () => {
    expect(emptyCascadeForm()).toMatchObject({
      entryNodeId: 0,
      exitNodeIds: [],
      strategy: 'leastPing',
      mode: 'split',
    });
  });

  it('opens a cascade saved before modes as a pure relay', () => {
    const form = cascadeFormOf({
      id: 1,
      name: 'old',
      enable: true,
      entryNodeId: 0,
      exitNodeIds: [2],
      exitCountry: '',
      entryInboundTags: null,
      strategy: 'leastPing',
      links: null,
      lastError: '',
    });
    expect(form.mode).toBe('relay');
  });

  it('maps the two switches to a mode and never turns both off', () => {
    expect(toggleCascadeMode('split', 'relay', true)).toBe('both');
    expect(toggleCascadeMode('both', 'split', false)).toBe('relay');
    expect(toggleCascadeMode('both', 'relay', false)).toBe('split');
    expect(toggleCascadeMode('relay', 'split', true)).toBe('both');
    expect(toggleCascadeMode('split', 'split', false)).toBe('split');
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
