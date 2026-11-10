import { useTranslation } from 'react-i18next';
import { Form, Select } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { useTlsCertsQuery } from '@/api/queries/useTlsCerts';
import { issuedCertPaths } from '@/lib/tls/issuedCertPaths';

interface IssuedCertSelectProps {
  index: number;
}

export default function IssuedCertSelect({ index }: IssuedCertSelectProps) {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const nodeId = useWatch({ control, name: 'nodeId' }) as number | null | undefined;
  const { data: certs = [] } = useTlsCertsQuery();
  const issued = certs.filter((c) => c.certFile);
  if (issued.length === 0) return null;

  const notOnNode = t('pages.settings.tlsCerts.inboundNotOnNode');
  const options = issued.map((c) => {
    const paths = issuedCertPaths(c, nodeId);
    const label = c.domains.join(', ');
    return { value: c.id, label: paths ? label : `${label} (${notOnNode})`, disabled: !paths };
  });

  return (
    <Form.Item label={t('pages.settings.tlsCerts.inboundPick')}>
      <Select
        aria-label={t('pages.settings.tlsCerts.inboundPick')}
        value={null}
        options={options}
        onChange={(id: number) => {
          const cert = issued.find((c) => c.id === id);
          const paths = cert && issuedCertPaths(cert, nodeId);
          if (!paths) return;
          setValue(
            `streamSettings.tlsSettings.certificates.${index}.certificateFile`,
            paths.certFile,
          );
          setValue(`streamSettings.tlsSettings.certificates.${index}.keyFile`, paths.keyFile);
        }}
      />
    </Form.Item>
  );
}
