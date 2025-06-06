import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  image: string;
  description: React.ReactElement;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Built together. Built in the open.',
    image: require('@site/static/img/1.png').default,
    description: (
      <>
        Envoy AI Gateway is the result of the community coming together to address GenAI traffic handling needs using Envoy.
      </>
    ),
  },
  {
    title: 'v0.2 Release now available',
    image: require('@site/static/img/3.png').default,
    description: (
      <>
        The v0.2 Release of Envoy AI Gateway is now available. See the <a href="/release-notes/v0.2">release notes</a> for more information.
      </>
    ),
  },
  {
    title: 'Get involved in the community',
    image: require('@site/static/img/2.png').default,
    description: (
      <>
        Join our community on Slack, join the conversation on GitHub, and attend our Thursday community meetings. See links in footer.
      </>
    ),
  },
];

function Feature({title, image, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <img src={image} className={styles.featureSvg} alt={title} />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): React.ReactElement {
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
