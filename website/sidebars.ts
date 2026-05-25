import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: 'category',
      label: 'Getting Started',
      items: [
        'getting-started/installation',
        'getting-started/configuration',
        'getting-started/profiles',
      ],
      collapsed: false,
    },
    {
      type: 'category',
      label: 'Architecture',
      items: [
        'architecture/overview',
        'architecture/receiving',
        'architecture/exporting',
        'architecture/queue',
      ],
    },
    {
      type: 'category',
      label: 'Governance',
      items: [
        'governance/limits',
        'governance/processing-rules',
        'governance/cardinality-tracking',
        'governance/tenant',
      ],
    },
    {
      type: 'category',
      label: 'Operations',
      items: [
        'operations/production-guide',
        'operations/health',
        'operations/reload',
        'operations/resilience',
        'operations/sharding',
        'operations/compression',
        'operations/logging',
      ],
    },
    {
      type: 'category',
      label: 'Observability',
      items: [
        'observability/statistics',
        'observability/dashboards',
        'observability/alerting',
        'observability/slo',
      ],
    },
    {
      type: 'category',
      label: 'Protocols',
      items: [
        'protocols/prw',
        'protocols/http-settings',
      ],
    },
    {
      type: 'category',
      label: 'Security',
      items: [
        'security/authentication',
        'security/tls',
      ],
    },
    {
      type: 'category',
      label: 'Advanced',
      items: [
        'advanced/performance',
        'advanced/bloom-persistence',
        'advanced/stability-guide',
        'advanced/playground-guide',
      ],
    },
    {
      type: 'category',
      label: 'Contributing',
      items: [
        'contributing/development',
        'contributing/testing',
      ],
    },
  ],
};

export default sidebars;
