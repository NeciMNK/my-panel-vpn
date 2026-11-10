import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { Card, Select, Space, Typography } from 'antd';

import { keys } from '@/api/queryKeys';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import { setXrayTargetNode, useXrayTargetNode } from '@/api/xrayTarget';

const LOCAL = 'local';

export default function XrayTargetSelect() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const nodeId = useXrayTargetNode();
  const { nodes } = useNodesQuery();

  if (nodes.length === 0 && nodeId === null) return null;

  const options = [
    { value: LOCAL, label: t('pages.xray.targetLocal') },
    ...nodes.map((n) => ({
      value: String(n.id),
      label: `${n.name}${n.status === 'online' ? '' : ` (${n.status})`}`,
      disabled: !n.enable,
    })),
  ];

  const onChange = (value: string) => {
    setXrayTargetNode(value === LOCAL ? null : Number(value));
    queryClient.removeQueries({ queryKey: keys.xray.root() });
  };

  return (
    <Card hoverable style={{ marginBottom: 12 }}>
      <Space wrap>
        <Typography.Text strong>{t('pages.xray.targetNode')}</Typography.Text>
        <Select
          aria-label={t('pages.xray.targetNode')}
          style={{ minWidth: 240 }}
          value={nodeId === null ? LOCAL : String(nodeId)}
          options={options}
          onChange={onChange}
        />
      </Space>
    </Card>
  );
}
