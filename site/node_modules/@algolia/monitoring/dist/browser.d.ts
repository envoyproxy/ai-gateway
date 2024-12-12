import * as _algolia_client_common from '@algolia/client-common';
import { CreateClientOptions, RequestOptions, ClientOptions } from '@algolia/client-common';

/**
 * Status of the cluster.
 */
type Status = 'operational' | 'degraded_performance' | 'partial_outage' | 'major_outage';

/**
 * Incident details.
 */
type Incident = {
    /**
     * Description of the incident.
     */
    title?: string;
    status?: Status;
};

type IncidentEntry = {
    /**
     * Timestamp, measured in milliseconds since the Unix epoch.
     */
    t?: number;
    v?: Incident;
};

type IncidentsResponse = {
    incidents?: {
        [key: string]: Array<IncidentEntry>;
    };
};

type TimeEntry = {
    /**
     * Timestamp, measured in milliseconds since the Unix epoch.
     */
    t?: number;
    /**
     * Time in ms.
     */
    v?: number;
};

type IndexingMetric = {
    indexing?: {
        [key: string]: Array<TimeEntry>;
    };
};

type IndexingTimeResponse = {
    metrics?: IndexingMetric;
};

type ProbesMetric = {
    /**
     * Timestamp, measured in milliseconds since the Unix epoch.
     */
    t?: number;
    /**
     * Value of the metric.
     */
    v?: number;
};

type Metrics = {
    /**
     * CPU idleness in %.
     */
    cpu_usage?: {
        [key: string]: Array<ProbesMetric>;
    };
    /**
     * RAM used for indexing in MB.
     */
    ram_indexing_usage?: {
        [key: string]: Array<ProbesMetric>;
    };
    /**
     * RAM used for search in MB.
     */
    ram_search_usage?: {
        [key: string]: Array<ProbesMetric>;
    };
    /**
     * Solid-state disk (SSD) usage expressed as % of RAM.  0% means no SSD usage. A value of 50% indicates 32&nbsp;GB SSD usage for a machine with 64&nbsp;RAM.
     */
    ssd_usage?: {
        [key: string]: Array<ProbesMetric>;
    };
    /**
     * Average build time of the indices in seconds.
     */
    avg_build_time?: {
        [key: string]: Array<ProbesMetric>;
    };
};

type InfrastructureResponse = {
    metrics?: Metrics;
};

/**
 * Region where the cluster is located.
 */
type Region = 'au' | 'br' | 'ca' | 'de' | 'eu' | 'hk' | 'in' | 'jp' | 'sg' | 'uae' | 'uk' | 'usc' | 'use' | 'usw' | 'za';

type ServerStatus = 'PRODUCTION';

type Type = 'cluster';

type Server = {
    /**
     * Server name.
     */
    name?: string;
    region?: Region;
    /**
     * Included to support legacy applications. Use `is_replica` instead.
     */
    is_slave?: boolean;
    /**
     * Whether this server is a replica of another server.
     */
    is_replica?: boolean;
    /**
     * Name of the cluster to which this server belongs.
     */
    cluster?: string;
    status?: ServerStatus;
    type?: Type;
};

type InventoryResponse = {
    inventory?: Array<Server>;
};

type LatencyMetric = {
    latency?: {
        [key: string]: Array<TimeEntry>;
    };
};

type LatencyResponse = {
    metrics?: LatencyMetric;
};

type StatusResponse = {
    status?: {
        [key: string]: Status;
    };
};

type Metric = 'avg_build_time' | 'ssd_usage' | 'ram_search_usage' | 'ram_indexing_usage' | 'cpu_usage' | '*';

type Period = 'minute' | 'hour' | 'day' | 'week' | 'month';

/**
 * Properties for the `customDelete` method.
 */
type CustomDeleteProps = {
    /**
     * Path of the endpoint, anything after \"/1\" must be specified.
     */
    path: string;
    /**
     * Query parameters to apply to the current query.
     */
    parameters?: {
        [key: string]: any;
    };
};
/**
 * Properties for the `customGet` method.
 */
type CustomGetProps = {
    /**
     * Path of the endpoint, anything after \"/1\" must be specified.
     */
    path: string;
    /**
     * Query parameters to apply to the current query.
     */
    parameters?: {
        [key: string]: any;
    };
};
/**
 * Properties for the `customPost` method.
 */
type CustomPostProps = {
    /**
     * Path of the endpoint, anything after \"/1\" must be specified.
     */
    path: string;
    /**
     * Query parameters to apply to the current query.
     */
    parameters?: {
        [key: string]: any;
    };
    /**
     * Parameters to send with the custom request.
     */
    body?: Record<string, unknown>;
};
/**
 * Properties for the `customPut` method.
 */
type CustomPutProps = {
    /**
     * Path of the endpoint, anything after \"/1\" must be specified.
     */
    path: string;
    /**
     * Query parameters to apply to the current query.
     */
    parameters?: {
        [key: string]: any;
    };
    /**
     * Parameters to send with the custom request.
     */
    body?: Record<string, unknown>;
};
/**
 * Properties for the `getClusterIncidents` method.
 */
type GetClusterIncidentsProps = {
    /**
     * Subset of clusters, separated by commas.
     */
    clusters: string;
};
/**
 * Properties for the `getClusterStatus` method.
 */
type GetClusterStatusProps = {
    /**
     * Subset of clusters, separated by commas.
     */
    clusters: string;
};
/**
 * Properties for the `getIndexingTime` method.
 */
type GetIndexingTimeProps = {
    /**
     * Subset of clusters, separated by commas.
     */
    clusters: string;
};
/**
 * Properties for the `getLatency` method.
 */
type GetLatencyProps = {
    /**
     * Subset of clusters, separated by commas.
     */
    clusters: string;
};
/**
 * Properties for the `getMetrics` method.
 */
type GetMetricsProps = {
    /**
     * Metric to report.  For more information about the individual metrics, see the description of the API response. To include all metrics, use `*`.
     */
    metric: Metric;
    /**
     * Period over which to aggregate the metrics:  - `minute`. Aggregate the last minute. 1 data point per 10 seconds. - `hour`. Aggregate the last hour. 1 data point per minute. - `day`. Aggregate the last day. 1 data point per 10 minutes. - `week`. Aggregate the last week. 1 data point per hour. - `month`. Aggregate the last month. 1 data point per day.
     */
    period: Period;
};
/**
 * Properties for the `getReachability` method.
 */
type GetReachabilityProps = {
    /**
     * Subset of clusters, separated by commas.
     */
    clusters: string;
};

declare const apiClientVersion = "1.17.0";
declare function createMonitoringClient({ appId: appIdOption, apiKey: apiKeyOption, authMode, algoliaAgents, ...options }: CreateClientOptions): {
    transporter: _algolia_client_common.Transporter;
    /**
     * The `appId` currently in use.
     */
    appId: string;
    /**
     * Clears the cache of the transporter for the `requestsCache` and `responsesCache` properties.
     */
    clearCache(): Promise<void>;
    /**
     * Get the value of the `algoliaAgent`, used by our libraries internally and telemetry system.
     */
    readonly _ua: string;
    /**
     * Adds a `segment` to the `x-algolia-agent` sent with every requests.
     *
     * @param segment - The algolia agent (user-agent) segment to add.
     * @param version - The version of the agent.
     */
    addAlgoliaAgent(segment: string, version?: string): void;
    /**
     * Helper method to switch the API key used to authenticate the requests.
     *
     * @param params - Method params.
     * @param params.apiKey - The new API Key to use.
     */
    setClientApiKey({ apiKey }: {
        apiKey: string;
    }): void;
    /**
     * This method allow you to send requests to the Algolia REST API.
     * @param customDelete - The customDelete object.
     * @param customDelete.path - Path of the endpoint, anything after \"/1\" must be specified.
     * @param customDelete.parameters - Query parameters to apply to the current query.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    customDelete({ path, parameters }: CustomDeleteProps, requestOptions?: RequestOptions): Promise<Record<string, unknown>>;
    /**
     * This method allow you to send requests to the Algolia REST API.
     * @param customGet - The customGet object.
     * @param customGet.path - Path of the endpoint, anything after \"/1\" must be specified.
     * @param customGet.parameters - Query parameters to apply to the current query.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    customGet({ path, parameters }: CustomGetProps, requestOptions?: RequestOptions): Promise<Record<string, unknown>>;
    /**
     * This method allow you to send requests to the Algolia REST API.
     * @param customPost - The customPost object.
     * @param customPost.path - Path of the endpoint, anything after \"/1\" must be specified.
     * @param customPost.parameters - Query parameters to apply to the current query.
     * @param customPost.body - Parameters to send with the custom request.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    customPost({ path, parameters, body }: CustomPostProps, requestOptions?: RequestOptions): Promise<Record<string, unknown>>;
    /**
     * This method allow you to send requests to the Algolia REST API.
     * @param customPut - The customPut object.
     * @param customPut.path - Path of the endpoint, anything after \"/1\" must be specified.
     * @param customPut.parameters - Query parameters to apply to the current query.
     * @param customPut.body - Parameters to send with the custom request.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    customPut({ path, parameters, body }: CustomPutProps, requestOptions?: RequestOptions): Promise<Record<string, unknown>>;
    /**
     * Retrieves known incidents for the selected clusters.
     * @param getClusterIncidents - The getClusterIncidents object.
     * @param getClusterIncidents.clusters - Subset of clusters, separated by commas.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getClusterIncidents({ clusters }: GetClusterIncidentsProps, requestOptions?: RequestOptions): Promise<IncidentsResponse>;
    /**
     * Retrieves the status of selected clusters.
     * @param getClusterStatus - The getClusterStatus object.
     * @param getClusterStatus.clusters - Subset of clusters, separated by commas.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getClusterStatus({ clusters }: GetClusterStatusProps, requestOptions?: RequestOptions): Promise<StatusResponse>;
    /**
     * Retrieves known incidents for all clusters.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getIncidents(requestOptions?: RequestOptions): Promise<IncidentsResponse>;
    /**
     * Retrieves average times for indexing operations for selected clusters.
     * @param getIndexingTime - The getIndexingTime object.
     * @param getIndexingTime.clusters - Subset of clusters, separated by commas.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getIndexingTime({ clusters }: GetIndexingTimeProps, requestOptions?: RequestOptions): Promise<IndexingTimeResponse>;
    /**
     * Retrieves the average latency for search requests for selected clusters.
     * @param getLatency - The getLatency object.
     * @param getLatency.clusters - Subset of clusters, separated by commas.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getLatency({ clusters }: GetLatencyProps, requestOptions?: RequestOptions): Promise<LatencyResponse>;
    /**
     * Retrieves metrics related to your Algolia infrastructure, aggregated over a selected time window.  Access to this API is available as part of the [Premium or Elevate plans](https://www.algolia.com/pricing). You must authenticate requests with the `x-algolia-application-id` and `x-algolia-api-key` headers (using the Monitoring API key).
     * @param getMetrics - The getMetrics object.
     * @param getMetrics.metric - Metric to report.  For more information about the individual metrics, see the description of the API response. To include all metrics, use `*`.
     * @param getMetrics.period - Period over which to aggregate the metrics:  - `minute`. Aggregate the last minute. 1 data point per 10 seconds. - `hour`. Aggregate the last hour. 1 data point per minute. - `day`. Aggregate the last day. 1 data point per 10 minutes. - `week`. Aggregate the last week. 1 data point per hour. - `month`. Aggregate the last month. 1 data point per day.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getMetrics({ metric, period }: GetMetricsProps, requestOptions?: RequestOptions): Promise<InfrastructureResponse>;
    /**
     * Test whether clusters are reachable or not.
     * @param getReachability - The getReachability object.
     * @param getReachability.clusters - Subset of clusters, separated by commas.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getReachability({ clusters }: GetReachabilityProps, requestOptions?: RequestOptions): Promise<{
        [key: string]: {
            [key: string]: boolean;
        };
    }>;
    /**
     * Retrieves the servers that belong to clusters.  The response depends on whether you authenticate your API request:  - With authentication, the response lists the servers assigned to your Algolia application\'s cluster.  - Without authentication, the response lists the servers for all Algolia clusters.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getServers(requestOptions?: RequestOptions): Promise<InventoryResponse>;
    /**
     * Retrieves the status of all Algolia clusters and instances.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getStatus(requestOptions?: RequestOptions): Promise<StatusResponse>;
};

type BadRequest = {
    reason?: string;
};

/**
 * Error.
 */
type ErrorBase = Record<string, any> & {
    message?: string;
};

type Forbidden = {
    reason?: string;
};

type Unauthorized = {
    reason?: string;
};

declare function monitoringClient(appId: string, apiKey: string, options?: ClientOptions): MonitoringClient;
type MonitoringClient = ReturnType<typeof createMonitoringClient>;

export { type BadRequest, type CustomDeleteProps, type CustomGetProps, type CustomPostProps, type CustomPutProps, type ErrorBase, type Forbidden, type GetClusterIncidentsProps, type GetClusterStatusProps, type GetIndexingTimeProps, type GetLatencyProps, type GetMetricsProps, type GetReachabilityProps, type Incident, type IncidentEntry, type IncidentsResponse, type IndexingMetric, type IndexingTimeResponse, type InfrastructureResponse, type InventoryResponse, type LatencyMetric, type LatencyResponse, type Metric, type Metrics, type MonitoringClient, type Period, type ProbesMetric, type Region, type Server, type ServerStatus, type Status, type StatusResponse, type TimeEntry, type Type, type Unauthorized, apiClientVersion, monitoringClient };
