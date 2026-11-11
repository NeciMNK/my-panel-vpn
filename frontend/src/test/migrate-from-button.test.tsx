import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import MigrateFromButton from '@/pages/settings/MigrateFromButton';
import { renderWithProviders } from './test-utils';

describe('MigrateFromButton', () => {
  it('opens the migration window from the settings header', async () => {
    renderWithProviders(<MigrateFromButton />);

    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Move from…' }));

    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('Move from another panel');
    expect(dialog.textContent).toContain('Remnawave panel URL');
  });
});
