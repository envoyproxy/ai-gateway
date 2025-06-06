import React from 'react';
import ReleaseHeader from './ReleaseHeader';
import { ItemList } from './ItemList';
import styles from './ReleaseNotes.module.css';

interface Tag {
  text: string;
  type?: 'default' | 'milestone' | 'major' | 'patch' | 'feature';
}

interface ListItem {
  title: string;
  description: string;
  icon?: string;
}

interface Feature {
  title: string;
  items: ListItem[];
}

interface PatchReleaseProps {
  version: string;
  date: string;
  type?: 'milestone' | 'major' | 'patch';
  tags?: Tag[];
  overview: string;
  features?: Feature[];
  bugFixes?: ListItem[];
}

export default function PatchRelease({
  version,
  date,
  type = 'patch',
  tags = [],
  overview,
  features = [],
  bugFixes = [],
}: PatchReleaseProps) {
  return (
    <div className={styles.patchRelease}>
      <ReleaseHeader
        version={version}
        date={date}
        type={type}
        tags={tags}
      >
        {overview}
      </ReleaseHeader>

      {features.length > 0 && (
        <div className={`${styles.patchReleaseSection} ${styles.featuresSection}`}>
          <h4>New Features</h4>
          {features.map((feature, idx) => (
            <div key={idx} className={styles.featureSection}>
              <h5>{feature.title}</h5>
              <ItemList items={feature.items} />
            </div>
          ))}
        </div>
      )}

      {bugFixes.length > 0 && (
        <div className={`${styles.patchReleaseSection} ${styles.bugFixesSection}`}>
          <h4>Bug Fixes</h4>
          <ItemList items={bugFixes} />
        </div>
      )}
    </div>
  );
}
