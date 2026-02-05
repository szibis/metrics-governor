# metrics-governor Configuration Helper

A single-page web app that helps operators plan their metrics-governor deployment.

## Usage

Open `index.html` directly in your browser - no build step, no server required:

```bash
open tools/config-helper/index.html
# or from repo root:
open index.html
```

## Features

- **Resource Estimation** - Given throughput inputs (datapoints/sec, metric count, cardinality), estimates CPU, memory, disk I/O, and queue sizing
- **K8s Pod Sizing** - Recommends replica count and per-pod resource requests/limits
- **Cloud Storage Recommendations** - Suggests the right storage class for AWS, Azure, or GCP
- **Limits Rules Builder** - Interactive form to build limits rules with match patterns, actions, and group-by labels
- **Live Config Preview** - Three tabs with real-time YAML generation for Helm values, app config, and limits rules
- **Copy & Download** - One-click copy to clipboard or download as file
- **Dark Mode** - Automatically follows your system preference

## Tech Stack

- [Alpine.js](https://alpinejs.dev/) (~14KB) - reactive data binding
- [Pico CSS](https://picocss.com/) (~10KB) - classless semantic styling
- Zero build tooling, single HTML file
