import React from 'react';
import styles from './ReleaseNotes.module.css';

interface Dependency {
  title: string;
  description: string;
}

interface DependenciesProps {
  title?: string;
  dependencies: Dependency[];
}

export default function Dependencies({
  dependencies,
}: DependenciesProps) {
  if (!dependencies || dependencies.length === 0) {
    return null;
  }

  return (
    <div className={styles.dependenciesContainer}>
      <div className={styles.dependenciesGrid}>
        {dependencies.map((dep, index) => (
          <div key={index} className={styles.dependencyItem}>
            <strong className={styles.dependencyTitle}>
              {dep.title}
            </strong>
            <p className={styles.dependencyDescription}>
              {dep.description}
            </p>
          </div>
        ))}
      </div>
    </div>
  );
}
