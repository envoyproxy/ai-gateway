import v01Data from '../data/releases/v0.1.json';
import v02Data from '../data/releases/v0.2.json';

export const generateReleaseCardData = (versionData) => {
  const mainRelease = versionData.releases[0];
  const series = versionData.series;

  // Generate versions list
  const versionsList = versionData.releases.map(r => r.version).join(', ');

  // Extract key tags from main release (limit to 4)
  const keyTags = mainRelease.tags.slice(0, 4);

  // Get date from latest release
  const latestDate = versionData.releases[0]?.date;

  return {
    version: series.version,
    date: `Latest: ${latestDate}`,
    summary: series.subtitle,
    tags: keyTags,
    linkTo: `/release-notes/${series.version.replace('.x', '').replace('v', 'v')}`,
    linkText: `View ${series.version} Releases →`,
    badge: series.badge,
    featured: series.badgeType === 'milestone',
    versions: versionsList
  };
};

export const generateTimelineData = () => {
  const allReleases = [
    ...v02Data.releases,
    ...v01Data.releases
  ];

  // Sort by date (newest first)
  return allReleases.sort((a, b) => new Date(b.date) - new Date(a.date));
};

export const getReleaseSeries = () => [
  generateReleaseCardData(v02Data),
  generateReleaseCardData(v01Data)
];

export const getTimelineData = () => generateTimelineData();
