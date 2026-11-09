import { describe, expect, it } from 'vitest';

import { buildJoinCommand } from '@/pages/nodes/NodeJoinModal';

describe('buildJoinCommand', () => {
  it('pipes the master installer into bash with the master URL and token', () => {
    expect(buildJoinCommand('https://panel.example.com/abc', 'tok_1', false)).toBe(
      "curl -fsSL 'https://panel.example.com/abc/node-join/install.sh' | bash -s -- --master 'https://panel.example.com/abc' --join 'tok_1'",
    );
  });

  it('skips certificate checks on both downloads for a self-signed master', () => {
    const cmd = buildJoinCommand('https://1.2.3.4:2053', 'tok_2', true);
    expect(cmd.startsWith('curl -fsSLk ')).toBe(true);
    expect(cmd.endsWith(' --insecure')).toBe(true);
  });
});
