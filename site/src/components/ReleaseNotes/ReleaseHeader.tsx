import React from 'react';
import styles from './ReleaseNotes.module.css';

interface Tag {
  text: string;
  type?: 'default' | 'milestone' | 'major' | 'patch' | 'feature';
}

interface ReleaseHeaderProps {
  version: string;
  date: string;
  type?: 'milestone' | 'major' | 'patch';
  tags?: Tag[];
  children?: React.ReactNode;
}

export default function ReleaseHeader({
  version,
  date,
  type = 'patch',
  tags = [],
  children,
}: ReleaseHeaderProps) {
  return (
    <div className={`${styles.releaseHeader} ${styles[type]}`}>
      <div className={styles.releaseInfo}>
        <div className={styles.releaseVersionDate}>
          <h2 className={styles.releaseVersion}>{version}</h2>
          <div className={styles.releaseDate}>{date}</div>
        </div>
        {tags.length > 0 && (
          <div className={styles.releaseTags}>
            {tags.map((tag, index) => (
              <span
                key={index}
                className={`${styles.tag} ${tag.type ? styles[tag.type] : ''}`}
              >
                {tag.text}
              </span>
            ))}
          </div>
        )}
      </div>
      {children && <div className={styles.releaseHeaderContent}>{children}</div>}
    </div>
  );
}
