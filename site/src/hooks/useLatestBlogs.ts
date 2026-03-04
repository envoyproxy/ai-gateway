import { usePluginData } from '@docusaurus/useGlobalData';
import type { BlogPostMeta, LatestBlogsPluginData } from '../plugins/latestBlogsPlugin';

export function useLatestBlogs(): BlogPostMeta[] {
  const data = usePluginData('docusaurus-plugin-latest-blogs') as LatestBlogsPluginData | undefined;
  return data?.latestPosts || [];
}
