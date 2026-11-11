import { useTranslation } from 'react-i18next';
import { Alert } from 'antd';

import MigrateForm from '@/pages/index/MigrateForm';

export default function MigrateTab() {
  const { t } = useTranslation();
  return (
    <>
      <Alert
        type="info"
        showIcon
        title={t('pages.index.migrate.desc')}
        style={{ marginBottom: 16 }}
      />
      <MigrateForm />
    </>
  );
}
