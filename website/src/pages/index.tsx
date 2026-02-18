import React from 'react';
import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import FeatureCard from '../components/FeatureCard';

const features = [
  {
    title: 'Adaptive Cardinality Limiting',
    description: 'Identify and drop only the top offenders — well-behaved services keep flowing.',
    icon: '\u{1F6E1}\u{FE0F}',
    link: '/docs/governance/limits',
  },
  {
    title: 'Tiered Escalation',
    description: 'Graduated responses: log \u2192 sample \u2192 strip labels \u2192 drop. No all-or-nothing.',
    icon: '\u{1F4CA}',
    link: '/docs/governance/limits#tiered-escalation',
  },
  {
    title: 'Dual Pipeline',
    description: 'Native OTLP (gRPC + HTTP) and Prometheus Remote Write. Zero conversion overhead.',
    icon: '\u{1F500}',
    link: '/docs/architecture/receiving',
  },
  {
    title: 'Zero Data Loss',
    description: 'Always-queue architecture with circuit breaker, persistent disk queue, and backoff.',
    icon: '\u{1F4BE}',
    link: '/docs/operations/resilience',
  },
  {
    title: 'Processing Rules',
    description: 'Filter, transform, sample, downsample, aggregate, and classify metrics in-flight.',
    icon: '\u{2699}\u{FE0F}',
    link: '/docs/governance/processing-rules',
  },
  {
    title: 'Multi-Tenant Governance',
    description: 'Per-tenant detection, quotas, and enforcement. Protect storage from noisy tenants.',
    icon: '\u{1F3E2}',
    link: '/docs/governance/tenant',
  },
  {
    title: 'Consistent Sharding',
    description: 'Fan out to N backends via K8s DNS discovery with stable consistent hash routing.',
    icon: '\u{1F310}',
    link: '/docs/operations/sharding',
  },
  {
    title: 'Real-time Observability',
    description: 'Internal metrics, 13 alert rules with runbooks, Grafana dashboards, and SLO framework.',
    icon: '\u{1F50D}',
    link: '/docs/observability/statistics',
  },
];

export default function Home(): React.JSX.Element {
  return (
    <Layout
      title="Home"
      description="High-performance metrics governance proxy for OTLP and Prometheus Remote Write">
      <main>
        {/* Hero */}
        <div className="hero">
          <img
            src={useBaseUrl('/img/logo.svg')}
            alt="metrics-governor logo"
            className="hero__logo"
          />
          <h1 className="hero__title">metrics-governor</h1>
          <p className="hero__subtitle">
            High-performance metrics governance proxy. Drop it between your apps
            and your backend to control cardinality, transform metrics in-flight,
            and scale horizontally &mdash; with zero data loss.
          </p>
          <div className="cta-buttons">
            <Link className="button button--primary button--lg" to="/docs/getting-started/installation">
              Get Started
            </Link>
            <Link className="button button--secondary button--lg" to="/playground">
              Try the Playground
            </Link>
          </div>
        </div>

        {/* Features */}
        <div className="section section--alt">
          <div className="container">
            <h2 style={{textAlign: 'center', marginBottom: '0.5rem'}}>Features</h2>
            <p style={{textAlign: 'center', color: 'var(--ifm-color-emphasis-600)', marginBottom: '2rem'}}>
              Everything you need to govern metrics at scale
            </p>
            <div className="features-grid">
              {features.map((f) => (
                <FeatureCard key={f.title} {...f} />
              ))}
            </div>
          </div>
        </div>

        {/* Architecture */}
        <div className="architecture-section">
          <h2>Architecture</h2>
          <p>
            Two native pipelines. Zero conversion. OTLP stays OTLP. PRW stays PRW.
          </p>
          <img
            src={useBaseUrl('/img/architecture.svg')}
            alt="metrics-governor architecture diagram"
          />
        </div>

        {/* Quick Start */}
        <div className="section section--alt">
          <div className="quick-start">
            <h2 style={{textAlign: 'center'}}>Quick Start</h2>
            <div>
              <h4>1. Install</h4>
              <pre><code>go install github.com/szibis/metrics-governor/cmd/metrics-governor@latest</code></pre>

              <h4>2. Configure</h4>
              <pre><code>{`# config.yaml
exporter:
  endpoint: your-backend:4317
receiver:
  grpc_port: 4317
  http_port: 4318`}</code></pre>

              <h4>3. Run</h4>
              <pre><code>metrics-governor -config config.yaml</code></pre>
            </div>
            <div style={{textAlign: 'center', marginTop: '2rem'}}>
              <Link className="button button--primary" to="/docs/getting-started/installation">
                Full Installation Guide
              </Link>
            </div>
          </div>
        </div>
      </main>
    </Layout>
  );
}
