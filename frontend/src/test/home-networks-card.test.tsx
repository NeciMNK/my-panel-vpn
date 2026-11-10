import { describe, expect, it } from 'vitest';

import { emptyHomeForm, homeFormOf, pruneRuleTargets } from '@/pages/nodes/HomeNetworksCard';

describe('home network form helpers', () => {
  it('drops rule targets the network no longer has', () => {
    const form = {
      ...emptyHomeForm(),
      subnets: ['192.168.1.0/24'],
      hosts: [],
      rules: [{ emails: ['anna'], groups: [], targets: ['192.168.1.0/24', '10.0.0.5'] }],
    };
    expect(pruneRuleTargets(form).rules[0].targets).toEqual(['192.168.1.0/24']);
  });

  it('reads null lists from the server as empty', () => {
    const form = homeFormOf({
      ...emptyHomeForm(),
      id: 1,
      tunnelState: '',
      errors: null,
      subnets: null as unknown as string[],
      rules: [{ emails: null as unknown as string[], groups: ['family'], targets: [] }],
    });
    expect(form.subnets).toEqual([]);
    expect(form.rules[0]).toEqual({ emails: [], groups: ['family'], targets: [] });
  });
});
