import { Suspense, lazy, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from 'antd';
import { SwapOutlined } from '@ant-design/icons';

const MigrateModal = lazy(() => import('@/pages/index/MigrateModal'));

export default function MigrateFromButton() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button icon={<SwapOutlined aria-hidden />} onClick={() => setOpen(true)}>
        {t('pages.settings.migrateFrom')}
      </Button>
      {open && (
        <Suspense fallback={null}>
          <MigrateModal open={open} onClose={() => setOpen(false)} />
        </Suspense>
      )}
    </>
  );
}
