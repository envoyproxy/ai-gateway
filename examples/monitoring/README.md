This `monitoring.yaml` file is a Kubernetes manifest file that deploys a Prometheus server that scrapes metrics from pods with the standard annotations.
More specifically, Envoy AI Gateway produced metrics can be collected by `prometheus.io/scrape: 'true'` annotation. so there's nothing Envoy AI Gateway specific in this file.
