import { act, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { AllSetting } from '@/models/setting';
import SettingsPage from '@/pages/settings/SettingsPage';
import { renderWithProviders } from './test-utils';

vi.mock('@/api/queries/useAllSettings', () => ({
  useAllSettings: () => ({
    allSetting: new AllSetting(),
    updateSetting: () => {},
    fetched: true,
    spinning: false,
    setSpinning: () => {},
    saveDisabled: true,
    saveAll: async () => {},
    savePayload: async () => {},
  }),
}));

async function renderSettings(hash: string) {
  renderWithProviders(
    <MemoryRouter initialEntries={[`/settings${hash}`]}>
      <SettingsPage />
    </MemoryRouter>,
  );
  await act(async () => {});
}

describe('Move from settings tab', () => {
  it('shows the migration form inline on its own tab', async () => {
    await renderSettings('#migrate');

    expect(await screen.findByText('Remnawave panel URL')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('button', { name: 'Check' })).toBeTruthy();
  });

  it('no longer hides the migration behind a header button on other tabs', async () => {
    await renderSettings('#telegram');

    expect(screen.queryByRole('button', { name: /Move from/ })).toBeNull();
    expect(screen.queryByText('Remnawave panel URL')).toBeNull();
  });
});
