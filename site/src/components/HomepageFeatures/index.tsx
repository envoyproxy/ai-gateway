import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  Svg: React.ComponentType<React.ComponentProps<'svg'>>;
  description: JSX.Element;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Built together. Built in the open.',
    Svg: require('@site/static/img/2.svg').default,
    description: (
      <>
        Envoy AI Gateway is the result of the community coming together to address GenAI traffic handling needs using Envoy.
      </>
    ),
  },
  {
    title: 'Get involved in the community',
    Svg: require('@site/static/img/1.svg').default,
    description: (
      <>
        Join our community on Slack, join the conversation on GitHub, and attend our Thursday community meetings. See links in footer.
      </>
    ),
  },
  {
    title: 'Coming January 2025',
    Svg: require('@site/static/img/3.svg').default,
    description: (
      <>
        The v0.1 Release of Envoy AI Gateway coming end of January. Including features such as Unified LLM API and LLM token usage limiting.
      </>
    ),
  },
];

function Feature({title, Svg, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <Svg className={styles.featureSvg} role="img" />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): JSX.Element {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
