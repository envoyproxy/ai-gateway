import React from 'react';
import Link from '@docusaurus/Link';
import Heading from '@theme/Heading';
import styles from './ReleaseNotes.module.css';

interface Tag {
  text: string;
  type?: 'default' | 'milestone' | 'major' | 'patch' | 'feature';
}

interface ReleaseCardProps {
  version: string;
  date: string;
  summary: string;
  tags: Tag[];
  linkTo: string;
  linkText?: string;
  badge?: string;
  featured?: boolean;
  versions?: string;
}

export default function ReleaseCard({
  version,
  date,
  summary,
  tags,
  linkTo,
  linkText = 'View Details →',
  badge,
  featured = false,
  versions,
}: ReleaseCardProps) {
  return (
    <Link
      to={linkTo}
      className={`${styles.releaseCard} ${featured ? styles.featured : ''} ${styles.clickableCard}`}
    >
      <div className={styles.releaseHeader}>
        <Heading as="h2" className={styles.releaseVersion}>
          {version}
        </Heading>
        <div className={styles.releaseDate}>{date}</div>
        {badge && <div className={styles.releaseBadge}>{badge}</div>}
      </div>

      <div className={styles.releaseSummary}>{summary}</div>

      <div className={styles.releaseHighlights}>
        {tags.map((tag, index) => (
          <span
            key={index}
            className={`${styles.highlightTag} ${tag.type ? styles[tag.type] : ''}`}
          >
            {tag.text}
          </span>
        ))}
      </div>

      {versions && (
        <div className={styles.versionList}>
          <strong>Releases:</strong> {versions}
        </div>
      )}

      <div className={styles.releaseLink}>
        {linkText}
      </div>
    </Link>
  );
}
