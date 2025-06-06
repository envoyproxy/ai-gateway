import React from 'react';
import styles from './ReleaseNotes.module.css';

interface Feature {
  icon?: string;
  title: string;
  description: string;
}

interface FeatureGridProps {
  features: Feature[];
  columns?: 1 | 2 | 3;
}

export default function FeatureGrid({
  features,
  columns = 1,
}: FeatureGridProps) {
  return (
    <div className={styles.featureGridContainer}>
      <div className={`${styles.featureGrid} ${styles[`columns${columns}`]}`}>
        {features.map((feature, index) => (
          <div key={index} className={styles.featureItem}>
            {feature.icon && <div className={styles.featureIcon}>{feature.icon}</div>}
            <div className={styles.featureContent}>
              <strong
                className={styles.featureTitle}
                dangerouslySetInnerHTML={{ __html: feature.title }}
              />
              <p
                className={styles.featureDescription}
                dangerouslySetInnerHTML={{ __html: feature.description }}
              />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
