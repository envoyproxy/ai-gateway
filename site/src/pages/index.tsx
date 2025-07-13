import React from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import LLMProviders from '@site/src/components/LLMProviders';
import Heading from '@theme/Heading';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className="heroBanner">
      <div className="container">
        <img className="heroImage" src="./img/ai-gw-logo.svg" alt="Envoy AI Gateway"/>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        <div className="buttons">
          <Link
            className="button button--secondary button--lg"
            href="/docs/getting-started/">
            Get Started
          </Link>
          <Link
            className="button button--secondary button--lg"
            href="https://github.com/envoyproxy/ai-gateway">
            View on GitHub
          </Link>
        </div>
      </div>
    </header>
  );
}

export default function Home(): React.ReactElement {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`${siteConfig.title}`}
      description={`${siteConfig.tagline}`}>
      <HomepageHeader />
      <main>
        <HomepageFeatures />
        <LLMProviders />
      </main>
    </Layout>
  );
}
