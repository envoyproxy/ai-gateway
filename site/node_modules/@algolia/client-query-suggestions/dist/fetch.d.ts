import * as _algolia_client_common from '@algolia/client-common';
import { CreateClientOptions, RequestOptions, ClientOptions } from '@algolia/client-common';

type BaseResponse = {
    /**
     * HTTP status code.
     */
    status?: number;
    /**
     * Details about the response, such as error messages.
     */
    message?: string;
};

type ConfigStatus = {
    /**
     * Name of the Query Suggestions index (case-sensitive).
     */
    indexName?: string;
    /**
     * Whether the creation or update of the Query Suggestions index is in progress.
     */
    isRunning?: boolean;
    /**
     * Date and time when the Query Suggestions index was last built, in RFC 3339 format.
     */
    lastBuiltAt?: string;
    /**
     * Date and time when the Query Suggestions index was last updated successfully.
     */
    lastSuccessfulBuiltAt?: string;
    /**
     * Duration of the last successful build in seconds.
     */
    lastSuccessfulBuildDuration?: string;
};

type AppID = {
    /**
     * Algolia application ID to which this Query Suggestions configuration belongs.
     */
    appID?: string;
};

/**
 * Languages for deduplicating singular and plural suggestions. If specified, only the more popular form is included.
 */
type Languages = Array<string> | boolean;

/**
 * Facet to use as category.
 */
type Facet = {
    /**
     * Facet name.
     */
    attribute?: string;
    /**
     * Number of suggestions.
     */
    amount?: number;
};

/**
 * Configuration of an Algolia index for Query Suggestions.
 */
type SourceIndex = {
    /**
     * Name of the Algolia index (case-sensitive) to use as source for query suggestions.
     */
    indexName: string;
    /**
     * If true, Query Suggestions uses all replica indices to find popular searches. If false, only the primary index is used.
     */
    replicas?: boolean;
    analyticsTags?: Array<string> | null;
    facets?: Array<Facet> | null;
    /**
     * Minimum number of hits required to be included as a suggestion.  A search query must at least generate `minHits` search results to be included in the Query Suggestions index.
     */
    minHits?: number;
    /**
     * Minimum letters required to be included as a suggestion.  A search query must be at least `minLetters` long to be included in the Query Suggestions index.
     */
    minLetters?: number;
    generate?: Array<Array<string>> | null;
    external?: Array<string> | null;
};

/**
 * Query Suggestions configuration.
 */
type Configuration = {
    /**
     * Algolia indices from which to get the popular searches for query suggestions.
     */
    sourceIndices: Array<SourceIndex>;
    languages?: Languages;
    exclude?: Array<string> | null;
    /**
     * Whether to turn on personalized query suggestions.
     */
    enablePersonalization?: boolean;
    /**
     * Whether to include suggestions with special characters.
     */
    allowSpecialCharacters?: boolean;
};

/**
 * Query Suggestions configuration.
 */
type ConfigurationWithIndex = Record<string, unknown> & Configuration;

/**
 * API response for retrieving Query Suggestions configurations.
 */
type ConfigurationResponse = AppID & ConfigurationWithIndex;

/**
 * Type of log entry.  - `SKIP`. A query is skipped because it doesn\'t match the conditions for successful inclusion. For example, when a query doesn\'t generate enough search results. - `INFO`. An informative log entry. - `ERROR`. The Query Suggestions process encountered an error.
 */
type LogLevel = 'SKIP' | 'INFO' | 'ERROR';

type LogFile = {
    /**
     * Date and time of the log entry, in RFC 3339 format.
     */
    timestamp?: string;
    level?: LogLevel;
    /**
     * Details about this log entry.
     */
    message?: string;
    /**
     * Level indicating the position of a suggestion in a hierarchy of records.  For example, a `contextLevel` of 1 indicates that this suggestion belongs to a previous suggestion with `contextLevel` 0.
     */
    contextLevel?: number;
};

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
 * Properties for the `deleteConfig` method.
 */
type DeleteConfigProps = {
    /**
     * Query Suggestions index name.
     */
    indexName: string;
};
/**
 * Properties for the `getConfig` method.
 */
type GetConfigProps = {
    /**
     * Query Suggestions index name.
     */
    indexName: string;
};
/**
 * Properties for the `getConfigStatus` method.
 */
type GetConfigStatusProps = {
    /**
     * Query Suggestions index name.
     */
    indexName: string;
};
/**
 * Properties for the `getLogFile` method.
 */
type GetLogFileProps = {
    /**
     * Query Suggestions index name.
     */
    indexName: string;
};
/**
 * Properties for the `updateConfig` method.
 */
type UpdateConfigProps = {
    /**
     * Query Suggestions index name.
     */
    indexName: string;
    configuration: Configuration;
};

declare const apiClientVersion = "5.17.0";
declare const REGIONS: readonly ["eu", "us"];
type Region = (typeof REGIONS)[number];
type RegionOptions = {
    region: Region;
};
declare function createQuerySuggestionsClient({ appId: appIdOption, apiKey: apiKeyOption, authMode, algoliaAgents, region: regionOption, ...options }: CreateClientOptions & RegionOptions): {
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
     * Creates a new Query Suggestions configuration.  You can have up to 100 configurations per Algolia application.
     *
     * Required API Key ACLs:
     *  - editSettings
     * @param configurationWithIndex - The configurationWithIndex object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createConfig(configurationWithIndex: ConfigurationWithIndex, requestOptions?: RequestOptions): Promise<BaseResponse>;
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
     * Deletes a Query Suggestions configuration.  Deleting only removes the configuration and stops updates to the Query Suggestions index. To delete the Query Suggestions index itself, use the Search API and the `Delete an index` operation.
     *
     * Required API Key ACLs:
     *  - editSettings
     * @param deleteConfig - The deleteConfig object.
     * @param deleteConfig.indexName - Query Suggestions index name.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteConfig({ indexName }: DeleteConfigProps, requestOptions?: RequestOptions): Promise<BaseResponse>;
    /**
     * Retrieves all Query Suggestions configurations of your Algolia application.
     *
     * Required API Key ACLs:
     *  - settings
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getAllConfigs(requestOptions?: RequestOptions): Promise<Array<ConfigurationResponse>>;
    /**
     * Retrieves a single Query Suggestions configuration by its index name.
     *
     * Required API Key ACLs:
     *  - settings
     * @param getConfig - The getConfig object.
     * @param getConfig.indexName - Query Suggestions index name.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getConfig({ indexName }: GetConfigProps, requestOptions?: RequestOptions): Promise<ConfigurationResponse>;
    /**
     * Reports the status of a Query Suggestions index.
     *
     * Required API Key ACLs:
     *  - settings
     * @param getConfigStatus - The getConfigStatus object.
     * @param getConfigStatus.indexName - Query Suggestions index name.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getConfigStatus({ indexName }: GetConfigStatusProps, requestOptions?: RequestOptions): Promise<ConfigStatus>;
    /**
     * Retrieves the logs for a single Query Suggestions index.
     *
     * Required API Key ACLs:
     *  - settings
     * @param getLogFile - The getLogFile object.
     * @param getLogFile.indexName - Query Suggestions index name.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getLogFile({ indexName }: GetLogFileProps, requestOptions?: RequestOptions): Promise<LogFile>;
    /**
     * Updates a QuerySuggestions configuration.
     *
     * Required API Key ACLs:
     *  - editSettings
     * @param updateConfig - The updateConfig object.
     * @param updateConfig.indexName - Query Suggestions index name.
     * @param updateConfig.configuration - The configuration object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateConfig({ indexName, configuration }: UpdateConfigProps, requestOptions?: RequestOptions): Promise<BaseResponse>;
};

/**
 * Error.
 */
type ErrorBase = Record<string, any> & {
    message?: string;
};

type QuerySuggestionsClient = ReturnType<typeof createQuerySuggestionsClient>;

declare function querySuggestionsClient(appId: string, apiKey: string, region: Region, options?: ClientOptions): QuerySuggestionsClient;

export { type AppID, type BaseResponse, type ConfigStatus, type Configuration, type ConfigurationResponse, type ConfigurationWithIndex, type CustomDeleteProps, type CustomGetProps, type CustomPostProps, type CustomPutProps, type DeleteConfigProps, type ErrorBase, type Facet, type GetConfigProps, type GetConfigStatusProps, type GetLogFileProps, type Languages, type LogFile, type LogLevel, type QuerySuggestionsClient, type Region, type RegionOptions, type SourceIndex, type UpdateConfigProps, apiClientVersion, querySuggestionsClient };
