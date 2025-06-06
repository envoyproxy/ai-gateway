import React from 'react';
import Link from '@docusaurus/Link';
import Heading from '@theme/Heading';
import styles from './ReleaseNotes.module.css';

interface ReleaseSeriesLayoutProps {
  title: string;
  subtitle: string;
  seriesVersion: string;
  badgeText: string;
  badgeType?: 'milestone' | 'major' | 'series';
  children: React.ReactNode;
  previousSeries?: { version: string; path: string };
  nextSeries?: { version: string; path: string };
}

export default function ReleaseSeriesLayout({
  title,
  subtitle,
  seriesVersion,
  badgeText,
  badgeType = 'series',
  children,
  previousSeries,
  nextSeries,
}: ReleaseSeriesLayoutProps) {
  return (
    <div className={styles.releaseSeriesPage}>
      <div className={`${styles.seriesHeader} ${styles[badgeType]}`}>
        <div className={styles.seriesMeta}>
          <Link to="/release-notes" className={styles.backLink}>
            ← Back to Release Notes
          </Link>
          <div className={`${styles.seriesBadge} ${styles[badgeType]}`}>
            {badgeText}
          </div>
        </div>
        <Heading as="h1" className={styles.seriesTitle}>
          {title}
        </Heading>
        <div className={styles.seriesSubtitle}>
          {subtitle}
        </div>
      </div>

      <div className={styles.seriesContent}>
        {children}
      </div>

      <div className={styles.navigationFooter}>
        {previousSeries && (
          <Link to={previousSeries.path} className={`${styles.navLink} ${styles.prev}`}>
            ← Previous: {previousSeries.version}
          </Link>
        )}
        {nextSeries && (
          <Link to={nextSeries.path} className={`${styles.navLink} ${styles.next}`}>
            Next: {nextSeries.version} →
          </Link>
        )}
        <Link to="/release-notes" className={styles.navLink}>
          All Releases
        </Link>
      </div>
    </div>
  );
}
