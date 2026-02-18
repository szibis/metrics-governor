import React from 'react';
import Layout from '@theme/Layout';

export default function Playground(): React.JSX.Element {
  return (
    <Layout
      title="Config Playground"
      description="Interactive deployment planning tool for metrics-governor">
      <main style={{padding: 0}}>
        <iframe
          src="/playground/index.html"
          style={{
            width: '100%',
            height: 'calc(100vh - 60px)',
            border: 'none',
            display: 'block',
          }}
          title="metrics-governor Config Playground"
        />
      </main>
    </Layout>
  );
}
