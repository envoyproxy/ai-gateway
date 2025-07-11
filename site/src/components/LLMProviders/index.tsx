import React from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type LLMProvider = {
  name: string;
  logoUrl: string;
  status: 'supported' | 'in-progress';
};

const LLMProvidersList: LLMProvider[] = [
  {
    name: 'OpenAI',
    logoUrl: '/img/providers/openai.svg',
    status: 'supported',
  },
  {
    name: 'AWS Bedrock',
    logoUrl: '/img/providers/aws-bedrock.svg',
    status: 'supported',
  },
  {
    name: 'Azure OpenAI',
    logoUrl: '/img/providers/azure-openai.svg',
    status: 'supported',
  },
  {
    name: 'Google Gemini',
    logoUrl: '/img/providers/google-gemini.svg',
    status: 'supported',
  },
  {
    name: 'Groq',
    logoUrl: '/img/providers/groq.svg',
    status: 'supported',
  },
  {
    name: 'Mistral',
    logoUrl: '/img/providers/mistral.svg',
    status: 'supported',
  },
  {
    name: 'Cohere',
    logoUrl: '/img/providers/cohere.svg',
    status: 'supported',
  },
  {
    name: 'Together AI',
    logoUrl: '/img/providers/together-ai.svg',
    status: 'supported',
  },
  {
    name: 'DeepInfra',
    logoUrl: '/img/providers/deepinfra.svg',
    status: 'supported',
  },
  {
    name: 'DeepSeek',
    logoUrl: '/img/providers/deepseek.svg',
    status: 'supported',
  },
  {
    name: 'Hunyuan',
    logoUrl: '/img/providers/hunyuan.svg',
    status: 'supported',
  },
  {
    name: 'SambaNova',
    logoUrl: '/img/providers/sambanova.svg',
    status: 'supported',
  },
  {
    name: 'Grok',
    logoUrl: '/img/providers/grok.svg',
    status: 'supported',
  },
  {
    name: 'Vertex AI',
    logoUrl: '/img/providers/vertex-ai.svg',
    status: 'in-progress',
  },
];

function ProviderLogo({ name, logoUrl, status }: LLMProvider) {
  return (
    <div className={styles.providerCol}>
      <div className={clsx(styles.providerCard, status === 'in-progress' && styles.inProgress)}>
        <div className={styles.logoContainer}>
          <img
            src={logoUrl}
            alt={`${name} logo`}
            className={styles.providerLogo}
            loading="lazy"
            onError={(e) => {
              const target = e.target as HTMLImageElement;
              target.src = '/img/providers/placeholder.svg';
            }}
          />
        </div>
        <div className={styles.providerName}>{name}</div>
        {status === 'in-progress' && (
          <div className={styles.statusBadge}>Coming Soon</div>
        )}
      </div>
    </div>
  );
}

export default function LLMProviders(): React.ReactElement {
  return (
    <section className={styles.providersSection}>
      <div className="container">
        <div className={styles.sectionHeader}>
          <Heading as="h2" className={styles.sectionTitle}>
            Supported LLM Providers
          </Heading>
          <div className={styles.titleUnderline}></div>
          <p className={styles.sectionDescription}>
            With the latest version of Envoy AI Gateway you can route traffic to these LLM providers out of the box.
            For more information and the most up-to-date provider integrations, check out our <a href="/docs/latest/capabilities/llm-integrations/supported-providers" className={styles.docsLink}>
            provider documentation</a>.
          </p>
        </div>
        <div className={styles.providersGrid}>
          {LLMProvidersList.map((provider, idx) => (
            <ProviderLogo key={idx} {...provider} />
          ))}
        </div>
      </div>
    </section>
  );
}
