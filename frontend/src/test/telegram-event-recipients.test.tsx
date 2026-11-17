import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { AllSetting } from '@/models/setting';
import { TelegramNotifications } from '@/components/ui/notifications/TelegramNotifications';

function renderRecipients(recipients: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  qc.setQueryData(keys.clients.all(), [
    { email: 'anna', group: 'paid' },
    { email: 'boris', group: 'friends' },
  ]);
  const updateSetting = vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <TelegramNotifications
        allSetting={
          new AllSetting({
            tgEnabledEvents: 'node.down,login.attempt',
            tgEventRecipients: recipients,
          })
        }
        updateSetting={updateSetting}
      />
    </QueryClientProvider>,
  );
  return updateSetting;
}

describe('TelegramEventRecipients', () => {
  // Login alerts carry admin names and IPs: they must not be offered to clients.
  it('offers recipients only for enabled events other than login alerts', () => {
    renderRecipients('{}');
    const groupPickers = screen.getAllByRole('combobox', { name: /Client groups/ });
    expect(groupPickers).toHaveLength(1);
    expect(groupPickers[0].getAttribute('aria-label')).toBe('Nodes · Down: Client groups');
  });

  it('adds a picked client to that event', async () => {
    const updateSetting = renderRecipients('{"node.down":{"groups":["friends"]}}');
    fireEvent.mouseDown(screen.getByRole('combobox', { name: /Clients$/ }));
    fireEvent.click(await screen.findByTitle('anna'));
    expect(updateSetting).toHaveBeenLastCalledWith({
      tgEventRecipients: '{"node.down":{"groups":["friends"],"clients":["anna"]}}',
    });
  });

  it('drops an event with nobody left so it goes to the admins only', () => {
    const updateSetting = renderRecipients('{"node.down":{"groups":["friends"]}}');
    const picker = screen.getByRole('combobox', { name: /Client groups/ }).closest('.ant-select');
    const remove = within(picker as HTMLElement).getByRole('button', { name: /friends/ });
    fireEvent.click(remove);
    expect(updateSetting).toHaveBeenLastCalledWith({ tgEventRecipients: '{}' });
  });
});
