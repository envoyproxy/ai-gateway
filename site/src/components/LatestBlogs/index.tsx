import React from 'react';
import Heading from '@theme/Heading';
import Link from '@docusaurus/Link';
import { useLatestBlogs } from '@site/src/hooks/useLatestBlogs';
import styles from './styles.module.css';

function BlogCard({
  title,
  description,
  image,
  tags,
  permalink,
}: {
  title: string;
  description: string;
  image: string;
  tags: string[];
  permalink: string;
}) {
  return (
    <div className={styles.blogCard}>
      {image && (
        <Link to={permalink} className={styles.imageLink}>
          <div className={styles.imageContainer}>
            <img
              src={image}
              alt={title}
              className={styles.blogImage}
              loading="lazy"
            />
          </div>
        </Link>
      )}
      <div className={styles.cardContent}>
        <Link to={permalink} className={styles.titleLink}>
          <Heading as="h3" className={styles.blogTitle}>
            {title}
          </Heading>
        </Link>
        {description && (
          <p className={styles.blogDescription}>{description}</p>
        )}
        {tags.length > 0 && (
          <div className={styles.tagsContainer}>
            {tags.map((tag, index) => (
              <span key={`${tag}-${index}`} className={styles.tag}>
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export default function LatestBlogs(): React.ReactElement | null {
  const latestPosts = useLatestBlogs();

  if (latestPosts.length === 0) {
    return null;
  }

  return (
    <section className={styles.latestBlogsSection}>
      <div className="container">
        <div className={styles.sectionHeader}>
          <Heading as="h2" className={styles.sectionTitle}>
            Latest from the Blog
          </Heading>
          <div className={styles.titleUnderline}></div>
          <p className={styles.sectionDescription}>
            Stay up to date with the latest news, features, and insights from the Envoy AI Gateway team.
          </p>
        </div>
        <div className={styles.blogsGrid}>
          {latestPosts.map((post) => (
            <BlogCard key={post.slug} {...post} />
          ))}
        </div>
        <div className={styles.ctaSection}>
          <Link
            className="button button--primary button--lg"
            to="/blog">
            View All Posts
          </Link>
        </div>
      </div>
    </section>
  );
}
