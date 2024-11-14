package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
)

func getMetricByName(t *testing.T, ns string, selector string, metricName string) float64 {
	fwd := newPortForwarder(t, ns, selector, 9090)
	require.NoError(t, fwd.Start())
	defer fwd.Stop()

	mf, err := retrieveMetric(fmt.Sprintf("http://%s/metrics", fwd.Address()), metricName, 5*time.Second)
	require.NoError(t, err)

	total := float64(0)
	if mf == nil {
		return total
	}

	for _, metric := range mf.Metric {
		if metric.GetCounter() != nil {
			total += *metric.GetCounter().Value
		}
		if metric.GetGauge() != nil {
			total += *metric.GetGauge().Value
		}
	}
	return total
}

func retrieveMetric(url string, name string, timeout time.Duration) (*dto.MetricFamily, error) {
	metrics, err := retrieveMetrics(url, timeout)
	if err != nil {
		return nil, err
	}

	if mf, ok := metrics[name]; ok {
		return mf, nil
	}

	return nil, nil
}

var metricParser = &expfmt.TextParser{}

func retrieveMetrics(url string, timeout time.Duration) (map[string]*dto.MetricFamily, error) {
	httpClient := http.Client{
		Timeout: timeout,
	}
	res, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to scrape metrics: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to scrape metrics: %s", res.Status)
	}

	return metricParser.TextToMetricFamilies(res.Body)
}
