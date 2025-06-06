import React from 'react';
import Heading from '@theme/Heading';
import styles from './ReleaseNotes.module.css';

interface InfoBoxProps {
  type?: 'info' | 'warning' | 'danger' | 'success';
  title?: string;
  icon?: string;
  children: React.ReactNode;
}

export default function InfoBox({
  type = 'info',
  title,
  icon,
  children,
}: InfoBoxProps) {
  return (
    <div className={`${styles.infoBox} ${styles[type]}`}>
      {(title || icon) && (
        <div className={styles.infoBoxHeader}>
          {icon && <span className={styles.infoBoxIcon}>{icon}</span>}
          {title && (
            <Heading as="h4" className={styles.infoBoxTitle}>
              {title}
            </Heading>
          )}
        </div>
      )}
      <div className={styles.infoBoxContent}>{children}</div>
    </div>
  );
}
