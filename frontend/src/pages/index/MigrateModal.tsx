import { useTranslation } from 'react-i18next';
import { Modal } from 'antd';

import MigrateForm from './MigrateForm';

interface MigrateModalProps {
  open: boolean;
  onClose: () => void;
}

export default function MigrateModal({ open, onClose }: MigrateModalProps) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      title={t('pages.index.migrate.title')}
      width={760}
      onCancel={onClose}
      footer={null}
    >
      <MigrateForm onClose={onClose} />
    </Modal>
  );
}
