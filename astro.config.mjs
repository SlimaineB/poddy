import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import { unified } from '@astrojs/markdown-remark';

const base = '/poddy';

/**
 * MkDocs wrote links as `page.md` and `page.md#section`. Astro/Starlight
 * routes are extensionless and base-prefixed, so rewrite local Markdown links
 * to the generated routes.
 */
function remarkDocsLinks() {
  return (tree) => {
    const walk = (node) => {
      if (node.type === 'link' && typeof node.url === 'string') {
        const href = node.url;
        if (!/^(https?:|mailto:|#|\/)/.test(href)) {
          const [target, hash] = href.split('#');
          if (target.endsWith('.md')) {
            const slug = target.replace(/^\.\//, '').replace(/\.md$/, '');
            node.url = `${base}/${slug}/${hash ? `#${hash}` : ''}`;
          }
        }
      }
      if (Array.isArray(node.children)) node.children.forEach(walk);
    };
    walk(tree);
  };
}

export default defineConfig({
  site: 'https://slimaineb.github.io',
  base,
  trailingSlash: 'always',
  outDir: './site',
  markdown: {
    processor: unified({ remarkPlugins: [remarkDocsLinks] }),
  },
  integrations: [
    starlight({
      title: 'poddy docs',
      disable404Route: true,
      description:
        'Self-service Kubernetes development workspaces with persistent homes, one-click IDEs, SSO, quotas, and backups.',
      logo: {
        src: './src/assets/logo.svg',
        alt: 'poddy',
      },
      favicon: '/favicon.svg',
      customCss: ['./src/styles/docs.css'],
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/SlimaineB/poddy',
        },
      ],
      editLink: {
        baseUrl: 'https://github.com/SlimaineB/poddy/edit/main/',
      },
      tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
      sidebar: [
        {
          label: 'Start here',
          items: [
            { label: 'Quick start', slug: 'getting-started' },
            { label: 'Product tour', slug: 'guided-tour' },
            { label: 'Full demo', slug: 'demo' },
            { label: 'Local k3s test', slug: 'k3s-test' },
          ],
        },
        {
          label: 'Day-to-day',
          items: [
            { label: 'Workspaces', slug: 'workspaces' },
            { label: 'Templates', slug: 'templates' },
            { label: 'Environment variables', slug: 'environment' },
            { label: 'Volumes and backups', slug: 'volumes' },
            { label: 'External authentication', slug: 'external-auth' },
            { label: 'Links and sharing', slug: 'links' },
          ],
        },
        {
          label: 'Deploy and operate',
          items: [
            { label: 'Kubernetes (Helm)', slug: 'deploy' },
            { label: 'Operations', slug: 'operations' },
            { label: 'Regions', slug: 'regions' },
            { label: 'Amazon EKS', slug: 'eks' },
            { label: 'Troubleshooting', slug: 'troubleshooting' },
          ],
        },
        {
          label: 'Security',
          items: [
            { label: 'OIDC / SSO', slug: 'oidc' },
            { label: 'Okta SSO', slug: 'okta' },
            { label: 'RBAC and ownership', slug: 'rbac' },
            { label: 'SSH access', slug: 'ssh' },
            { label: 'Workspace access', slug: 'workspace-access' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { label: 'Architecture', slug: 'architecture' },
            { label: 'Data model', slug: 'data-model' },
            { label: 'PostgreSQL control plane', slug: 'postgres-schema' },
            { label: 'Development', slug: 'development' },
          ],
        },
      ],
    }),
  ],
});
