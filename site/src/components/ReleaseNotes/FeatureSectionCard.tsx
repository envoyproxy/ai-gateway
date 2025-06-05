import React from 'react';
import styles from './ReleaseNotes.module.css';

interface Feature {
  icon?: string;
  title: string;
  description: string;
}

interface FeatureSection {
  title: string;
  items: Feature[];
}

interface FeatureSectionCardProps {
  section: FeatureSection;
}

export default function FeatureSectionCard({ section }: FeatureSectionCardProps) {
  return (
    <div className={styles.featureSectionCard}>
      <h3 className={styles.featureSectionTitle}>{section.title}</h3>
      <div className={styles.featureSectionItems}>
        {section.items.map((feature, index) => (
          <div key={index} className={styles.featureSectionItem}>
            <strong
              className={styles.featureSectionItemTitle}
              dangerouslySetInnerHTML={{ __html: feature.title }}
            />
            <p
              className={styles.featureSectionItemDescription}
              dangerouslySetInnerHTML={{ __html: feature.description }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
