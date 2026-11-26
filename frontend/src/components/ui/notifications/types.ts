import type { ReactNode } from 'react';

type NumberInput = (props: {
  value: number;
  onChange: (v: number | null) => void;
  ariaLabel: string;
}) => ReactNode;

export interface NotificationEventConfig {
  key: string;
  label: string;
  settingKey: string;
  hint?: string;
  extra?: NumberInput;
  // A second number the event uses, e.g. how long a node's load must hold.
  also?: { settingKey: string; label: string; hint?: string; extra: NumberInput };
}

export interface NotificationGroupConfig {
  icon: ReactNode;
  title: string;
  events: NotificationEventConfig[];
}
