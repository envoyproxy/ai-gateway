import * as _algolia_client_common from '@algolia/client-common';
import { CreateClientOptions, RequestOptions, ClientOptions } from '@algolia/client-common';

/**
 * Credentials for authenticating with an API key.
 */
type AuthAPIKeyPartial = {
    /**
     * API key. This field is `null` in the API response.
     */
    key?: string;
};

/**
 * Credentials for authenticating with the Algolia Insights API.
 */
type AuthAlgoliaInsightsPartial = {
    /**
     * Algolia application ID.
     */
    appID?: string;
    /**
     * Algolia API key with the ACL: `search`. This field is `null` in the API response.
     */
    apiKey?: string;
};

/**
 * Credentials for authenticating with Algolia.
 */
type AuthAlgoliaPartial = {
    /**
     * Algolia application ID.
     */
    appID?: string;
    /**
     * Algolia API key with the ACL: `addObject`, `deleteObject`, `settings`, `editSettings`, `listIndexes`, `deleteIndex`. This field is `null` in the API response.
     */
    apiKey?: string;
};

/**
 * Credentials for authenticating with user name and password.
 */
type AuthBasicPartial = {
    /**
     * Username.
     */
    username?: string;
    /**
     * Password. This field is `null` in the API response.
     */
    password?: string;
};

/**
 * Credentials for authenticating with a Google service account, such as BigQuery.
 */
type AuthGoogleServiceAccountPartial = {
    /**
     * Email address of the Google service account.
     */
    clientEmail?: string;
    /**
     * Private key of the Google service account. This field is `null` in the API response.
     */
    privateKey?: string;
};

/**
 * Credentials for authenticating with OAuth 2.0.
 */
type AuthOAuthPartial = {
    /**
     * URL for the OAuth endpoint.
     */
    url?: string;
    /**
     * Client ID.
     */
    client_id?: string;
    /**
     * Client secret. This field is `null` in the API response.
     */
    client_secret?: string;
    /**
     * OAuth scope.
     */
    scope?: string;
};

type AuthInputPartial = AuthGoogleServiceAccountPartial | AuthBasicPartial | AuthAPIKeyPartial | AuthOAuthPartial | AuthAlgoliaPartial | AuthAlgoliaInsightsPartial | {
    [key: string]: string;
};

/**
 * Type of authentication. This determines the type of credentials required in the `input` object.
 */
type AuthenticationType = 'googleServiceAccount' | 'basic' | 'apiKey' | 'oauth' | 'algolia' | 'algoliaInsights' | 'secrets';

/**
 * Name of an ecommerce platform with which to authenticate. This determines which authentication type you can select.
 */
type Platform = 'bigcommerce' | 'commercetools' | 'shopify';

/**
 * Resource representing the information required to authenticate with a source or a destination.
 */
type Authentication = {
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID: string;
    type: AuthenticationType;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    platform?: Platform | null;
    input: AuthInputPartial;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt?: string;
};

/**
 * Credentials for authenticating with an API key.
 */
type AuthAPIKey = {
    /**
     * API key. This field is `null` in the API response.
     */
    key: string;
};

/**
 * Credentials for authenticating with Algolia.
 */
type AuthAlgolia = {
    /**
     * Algolia application ID.
     */
    appID: string;
    /**
     * Algolia API key with the ACL: `addObject`, `deleteObject`, `settings`, `editSettings`, `listIndexes`, `deleteIndex`. This field is `null` in the API response.
     */
    apiKey: string;
};

/**
 * Credentials for authenticating with the Algolia Insights API.
 */
type AuthAlgoliaInsights = {
    /**
     * Algolia application ID.
     */
    appID: string;
    /**
     * Algolia API key with the ACL: `search`. This field is `null` in the API response.
     */
    apiKey: string;
};

/**
 * Credentials for authenticating with user name and password.
 */
type AuthBasic = {
    /**
     * Username.
     */
    username: string;
    /**
     * Password. This field is `null` in the API response.
     */
    password: string;
};

/**
 * Credentials for authenticating with a Google service account, such as BigQuery.
 */
type AuthGoogleServiceAccount = {
    /**
     * Email address of the Google service account.
     */
    clientEmail: string;
    /**
     * Private key of the Google service account. This field is `null` in the API response.
     */
    privateKey: string;
};

/**
 * Credentials for authenticating with OAuth 2.0.
 */
type AuthOAuth = {
    /**
     * URL for the OAuth endpoint.
     */
    url: string;
    /**
     * Client ID.
     */
    client_id: string;
    /**
     * Client secret. This field is `null` in the API response.
     */
    client_secret: string;
    /**
     * OAuth scope.
     */
    scope?: string;
};

type AuthInput = AuthGoogleServiceAccount | AuthBasic | AuthAPIKey | AuthOAuth | AuthAlgolia | AuthAlgoliaInsights | {
    [key: string]: string;
};

/**
 * Request body for creating a new authentication resource.
 */
type AuthenticationCreate = {
    type: AuthenticationType;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    platform?: Platform | null;
    input: AuthInput;
};

/**
 * API response for the successful creation of an authentication resource.
 */
type AuthenticationCreateResponse = {
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID: string;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

/**
 * Request body for searching for authentication resources.
 */
type AuthenticationSearch = {
    authenticationIDs: Array<string>;
};

/**
 * API response for a successful update of an authentication resource.
 */
type AuthenticationUpdateResponse = {
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID: string;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt: string;
};

type DeleteResponse = {
    /**
     * Date of deletion in RFC 3339 format.
     */
    deletedAt: string;
};

/**
 * Record type for ecommerce sources.
 */
type RecordType = 'product' | 'variant';

type DestinationIndexName = {
    /**
     * Algolia index name (case-sensitive).
     */
    indexName: string;
    recordType?: RecordType;
    /**
     * Attributes from your source to exclude from Algolia records.  Not all your data attributes will be useful for searching. Keeping your Algolia records small increases indexing and search performance.  - Exclude nested attributes with `.` notation. For example, `foo.bar` indexes the `foo` attribute and all its children **except** the `bar` attribute. - Exclude attributes from arrays with `[i]`, where `i` is the index of the array element.   For example, `foo.[0].bar` only excludes the `bar` attribute from the first element of the `foo` array, but indexes the complete `foo` attribute for all other elements.   Use `*` as wildcard: `foo.[*].bar` excludes `bar` from all elements of the `foo` array.
     */
    attributesToExclude?: Array<string>;
};

type DestinationInput = DestinationIndexName;

/**
 * Destination type.  - `search`.   Data is stored in an Algolia index.  - `insights`.   Data is recorded as user events in the Insights API.
 */
type DestinationType = 'search' | 'insights';

/**
 * Destinations are Algolia resources like indices or event streams.
 */
type Destination = {
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    type: DestinationType;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    input: DestinationInput;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt?: string;
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID?: string;
    transformationIDs?: Array<string>;
};

/**
 * API request body for creating a new destination.
 */
type DestinationCreate = {
    type: DestinationType;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    input: DestinationInput;
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID?: string;
    transformationIDs?: Array<string>;
};

/**
 * API response for creating a new destination.
 */
type DestinationCreateResponse = {
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

/**
 * API request body for searching destinations.
 */
type DestinationSearch = {
    destinationIDs: Array<string>;
};

/**
 * API response for updating a destination.
 */
type DestinationUpdateResponse = {
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    /**
     * Descriptive name for the resource.
     */
    name: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt: string;
};

type EventStatus = 'created' | 'started' | 'retried' | 'failed' | 'succeeded' | 'critical';

type EventType = 'fetch' | 'record' | 'log' | 'transform';

/**
 * An event describe a step of the task execution flow..
 */
type Event = {
    /**
     * Universally unique identifier (UUID) of an event.
     */
    eventID: string;
    /**
     * Universally unique identifier (UUID) of a task run.
     */
    runID: string;
    status: EventStatus;
    type: EventType;
    /**
     * The extracted record batch size.
     */
    batchSize: number;
    data?: {
        [key: string]: any;
    };
    /**
     * Date of publish RFC 3339 format.
     */
    publishedAt: string;
};

/**
 * Paginated API response.
 */
type Pagination = {
    /**
     * Number of pages in the API response.
     */
    nbPages: number;
    /**
     * Page of the API response to retrieve.
     */
    page: number;
    /**
     * Number of items in the API response.
     */
    nbItems: number;
    /**
     * Number of items per page.
     */
    itemsPerPage: number;
};

type ListAuthenticationsResponse = {
    authentications: Array<Authentication>;
    pagination: Pagination;
};

type ListDestinationsResponse = {
    destinations: Array<Destination>;
    pagination: Pagination;
};

/**
 * Time window by which to filter the observability data.
 */
type Window = {
    /**
     * Date in RFC 3339 format representing the oldest data in the time window.
     */
    startDate: string;
    /**
     * Date in RFC 3339 format representing the newest data in the time window.
     */
    endDate: string;
};

type ListEventsResponse = {
    events: Array<Event>;
    pagination: Pagination;
    window: Window;
};

type BigCommerceChannel = {
    /**
     * ID of the BigCommerce channel.
     */
    id: number;
    /**
     * Currencies for the given channel.
     */
    currencies?: Array<string>;
};

type BigCommerceMetafield = {
    /**
     * Namespace of the metafield.
     */
    namespace: string;
    /**
     * Key identifier of the metafield.
     */
    key: string;
};

type SourceBigCommerce = {
    /**
     * Store hash identifying your BigCommerce store.
     */
    storeHash: string;
    channel?: BigCommerceChannel;
    customFields?: Array<string>;
    productMetafields?: Array<BigCommerceMetafield>;
    variantMetafields?: Array<BigCommerceMetafield>;
};

type BigQueryDataType = 'ga4' | 'ga360';

type SourceBigQuery = {
    /**
     * Project ID of the BigQuery source.
     */
    projectID: string;
    /**
     * Dataset ID of the BigQuery source.
     */
    datasetID: string;
    dataType?: BigQueryDataType;
    /**
     * Table name for the BigQuery export.
     */
    table?: string;
    /**
     * Table prefix for a Google Analytics 4 data export to BigQuery.
     */
    tablePrefix?: string;
    /**
     * Custom SQL request to extract data from the BigQuery table.
     */
    customSQLRequest?: string;
    /**
     * Name of a column that contains a unique ID which will be used as `objectID` in Algolia.
     */
    uniqueIDColumn?: string;
};

type MappingTypeCSV = 'string' | 'integer' | 'float' | 'boolean' | 'json';

/**
 * HTTP method to be used for retrieving your data.
 */
type MethodType = 'GET' | 'POST';

type SourceCSV = {
    /**
     * URL of the file.
     */
    url: string;
    /**
     * Name of a column that contains a unique ID which will be used as `objectID` in Algolia.
     */
    uniqueIDColumn?: string;
    /**
     * Key-value pairs of column names and their expected types.
     */
    mapping?: {
        [key: string]: MappingTypeCSV;
    };
    method?: MethodType;
    /**
     * The character used to split the value on each line, default to a comma (\\r, \\n, 0xFFFD, and space are forbidden).
     */
    delimiter?: string;
};

/**
 * Custom fields from commercetools to add to the records.  For more information, see [Using Custom Types and Custom Fields](https://docs.commercetools.com/tutorials/custom-types).
 */
type CommercetoolsCustomFields = {
    /**
     * Inventory custom fields.
     */
    inventory?: Array<string> | null;
    /**
     * Price custom fields.
     */
    price?: Array<string> | null;
    /**
     * Category custom fields.
     */
    category?: Array<string> | null;
};

type SourceCommercetools = {
    storeKeys?: Array<string>;
    /**
     * Locales for your commercetools stores.
     */
    locales?: Array<string>;
    url: string;
    projectKey: string;
    /**
     * Whether a fallback value is stored in the Algolia record if there\'s no inventory information about the product.
     */
    fallbackIsInStockValue?: boolean;
    customFields?: CommercetoolsCustomFields;
};

/**
 * Image type.
 */
type DockerImageType = 'singer' | 'custom' | 'airbyte';

/**
 * Container registry name from where to pull the image.
 */
type DockerRegistry = 'dockerhub' | 'ghcr';

type SourceDocker = {
    imageType: DockerImageType;
    registry: DockerRegistry;
    /**
     * Docker image name.
     */
    image: string;
    /**
     * Docker image version.
     */
    version?: string;
    /**
     * Configuration of the spec.
     */
    configuration: Record<string, unknown>;
};

type SourceGA4BigQueryExport = {
    /**
     * GCP project ID that the BigQuery export writes to.
     */
    projectID: string;
    /**
     * BigQuery dataset ID that the BigQuery export writes to.
     */
    datasetID: string;
    /**
     * Prefix of the tables that the BigQuery Export writes to.
     */
    tablePrefix: string;
};

type SourceJSON = {
    /**
     * URL of the file.
     */
    url: string;
    /**
     * Name of a column that contains a unique ID which will be used as `objectID` in Algolia.
     */
    uniqueIDColumn?: string;
    method?: MethodType;
};

type SourceShopifyBase = {
    /**
     * URL of the Shopify store.
     */
    shopURL: string;
};

type SourceUpdateShopify = {
    /**
     * Feature flags for the Shopify source.
     */
    featureFlags?: {
        [key: string]: any;
    };
};

type SourceShopify = SourceUpdateShopify & SourceShopifyBase;

type SourceInput = SourceCommercetools | SourceBigCommerce | SourceJSON | SourceCSV | SourceBigQuery | SourceGA4BigQueryExport | SourceDocker | SourceShopify;

type SourceType = 'bigcommerce' | 'bigquery' | 'commercetools' | 'csv' | 'docker' | 'ga4BigqueryExport' | 'json' | 'shopify' | 'push';

type Source = {
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    type: SourceType;
    name: string;
    input?: SourceInput;
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID?: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt?: string;
};

type ListSourcesResponse = {
    sources: Array<Source>;
    pagination: Pagination;
};

/**
 * Action to perform on the Algolia index.
 */
type ActionType = 'replace' | 'save' | 'partial' | 'append';

/**
 * The strategy to use to fetch the data.
 */
type DockerStreamsSyncMode = 'incremental' | 'fullTable';

type DockerStreams = {
    /**
     * The name of the stream to fetch the data from (e.g. table name).
     */
    name: string;
    /**
     * The properties of the stream to select (e.g. column).
     */
    properties?: Array<string>;
    syncMode: DockerStreamsSyncMode;
};

/**
 * The selected streams of a singer or airbyte connector.
 */
type DockerStreamsInput = {
    streams: Array<DockerStreams>;
};

/**
 * Represents a market in Shopify.
 */
type ShopifyMarket = {
    countries: Array<string>;
    currencies: Array<string>;
    locales: Array<string>;
};

/**
 * Represents a metafield in Shopify.
 */
type ShopifyMetafield = {
    namespace: string;
    key: string;
    value: string;
};

/**
 * Represents the required elements of the task input when using a `shopify` source.
 */
type ShopifyInput = {
    metafields: Array<ShopifyMetafield>;
    market: ShopifyMarket;
};

/**
 * Mapping format schema.
 */
type MappingFormatSchema = 'mappingkit/v1';

/**
 * Describes how a field should be resolved by applying a set of directives.
 */
type MappingFieldDirective = {
    /**
     * Destination field key.
     */
    fieldKey: string;
    /**
     * How the destination field should be resolved from the source.
     */
    value: {
        [key: string]: any;
    };
};

/**
 * Describes how a destination object should be resolved by means of applying a set of directives.
 */
type MappingKitAction = {
    /**
     * ID to uniquely identify this action.
     */
    id?: string;
    /**
     * Whether this action has any effect.
     */
    enabled: boolean;
    /**
     * Condition which must be satisfied to apply the action. If this evaluates to false, the action is not applied, and the process attempts to apply the next action, if any.
     */
    trigger: string;
    fieldDirectives: Array<MappingFieldDirective>;
};

/**
 * Transformations to apply to the source, serialized as a JSON string.
 */
type MappingInput = {
    format: MappingFormatSchema;
    actions: Array<MappingKitAction>;
};

/**
 * Input for a `streaming` task whose source is of type `ga4BigqueryExport` and for which extracted data is continuously streamed.
 */
type StreamingInput = {
    mapping: MappingInput;
};

/**
 * Configuration of the task, depending on its type.
 */
type TaskInput = StreamingInput | DockerStreamsInput | ShopifyInput;

type Task = {
    /**
     * Universally unique identifier (UUID) of a task.
     */
    taskID: string;
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    /**
     * Cron expression for the task\'s schedule.
     */
    cron?: string;
    /**
     * The last time the scheduled task ran in RFC 3339 format.
     */
    lastRun?: string;
    /**
     * The next scheduled run of the task in RFC 3339 format.
     */
    nextRun?: string;
    input?: TaskInput;
    /**
     * Whether the task is enabled.
     */
    enabled: boolean;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
    action?: ActionType;
    /**
     * Date of the last cursor in RFC 3339 format.
     */
    cursor?: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt?: string;
};

/**
 * Configured tasks and pagination information.
 */
type ListTasksResponse = {
    tasks: Array<Task>;
    pagination: Pagination;
};

/**
 * Task is run manually, with the `/run` endpoint.
 */
type OnDemandTriggerType = 'onDemand';

/**
 * Trigger information for manually-triggered tasks.
 */
type OnDemandTrigger = {
    type: OnDemandTriggerType;
    /**
     * The last time the scheduled task ran in RFC 3339 format.
     */
    lastRun?: string;
};

/**
 * Task runs on a schedule.
 */
type ScheduleTriggerType = 'schedule';

/**
 * Trigger information for scheduled tasks.
 */
type ScheduleTrigger = {
    type: ScheduleTriggerType;
    /**
     * Cron expression for the task\'s schedule.
     */
    cron: string;
    /**
     * The last time the scheduled task ran in RFC 3339 format.
     */
    lastRun?: string;
    /**
     * The next scheduled run of the task in RFC 3339 format.
     */
    nextRun: string;
};

/**
 * Task runs continuously.
 */
type StreamingTriggerType = 'streaming';

/**
 * Trigger input for continuously running tasks.
 */
type StreamingTrigger = {
    type: StreamingTriggerType;
};

/**
 * Task runs after receiving subscribed event.
 */
type SubscriptionTriggerType = 'subscription';

/**
 * Trigger input for subscription tasks.
 */
type SubscriptionTrigger = {
    type: SubscriptionTriggerType;
};

/**
 * Trigger that runs the task.
 */
type Trigger = OnDemandTrigger | ScheduleTrigger | SubscriptionTrigger | StreamingTrigger;

/**
 * The V1 task object, please use methods and types that don\'t contain the V1 suffix.
 */
type TaskV1 = {
    /**
     * Universally unique identifier (UUID) of a task.
     */
    taskID: string;
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    trigger: Trigger;
    input?: TaskInput;
    /**
     * Whether the task is enabled.
     */
    enabled: boolean;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
    action?: ActionType;
    /**
     * Date of the last cursor in RFC 3339 format.
     */
    cursor?: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt?: string;
};

/**
 * Configured tasks and pagination information.
 */
type ListTasksResponseV1 = {
    tasks: Array<TaskV1>;
    pagination: Pagination;
};

type Transformation = {
    /**
     * Universally unique identifier (UUID) of a transformation.
     */
    transformationID: string;
    /**
     * The authentications associated with the current transformation.
     */
    authenticationIDs?: Array<string>;
    /**
     * The source code of the transformation.
     */
    code: string;
    /**
     * The uniquely identified name of your transformation.
     */
    name: string;
    /**
     * A descriptive name for your transformation of what it does.
     */
    description?: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt?: string;
};

/**
 * Configured transformations and pagination information.
 */
type ListTransformationsResponse = {
    transformations: Array<Transformation>;
    pagination: Pagination;
};

/**
 * Task run outcome.
 */
type RunOutcome = 'success' | 'failure' | 'processing';

type RunProgress = {
    expectedNbOfEvents?: number;
    receivedNbOfEvents?: number;
};

/**
 * A code for the task run\'s outcome. A readable description of the code is included in the `reason` response property.
 */
type RunReasonCode = 'internal' | 'critical' | 'no_events' | 'too_many_errors' | 'ok' | 'discarded' | 'blocking';

/**
 * Task run status.
 */
type RunStatus = 'created' | 'started' | 'idled' | 'finished' | 'skipped';

/**
 * Task run type.
 */
type RunType = 'reindex' | 'update' | 'discover' | 'validate' | 'push';

type Run = {
    /**
     * Universally unique identifier (UUID) of a task run.
     */
    runID: string;
    appID: string;
    /**
     * Universally unique identifier (UUID) of a task.
     */
    taskID: string;
    status: RunStatus;
    progress?: RunProgress;
    outcome?: RunOutcome;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
    /**
     * More information about the task run\'s outcome.
     */
    reason?: string;
    reasonCode?: RunReasonCode;
    type: RunType;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
    /**
     * Date of start in RFC 3339 format.
     */
    startedAt?: string;
    /**
     * Date of finish in RFC 3339 format.
     */
    finishedAt?: string;
};

type RunListResponse = {
    runs: Array<Run>;
    pagination: Pagination;
    window: Window;
};

/**
 * API response for running a task.
 */
type RunResponse = {
    /**
     * Universally unique identifier (UUID) of a task run.
     */
    runID: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

type RunSourceResponse = {
    /**
     * Map of taskID sent for reindex with the corresponding runID.
     */
    taskWithRunID: {
        [key: string]: string;
    };
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

type SourceCreate = {
    type: SourceType;
    /**
     * Descriptive name of the source.
     */
    name: string;
    input?: SourceInput;
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID?: string;
};

type SourceCreateResponse = {
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    /**
     * Descriptive name of the source.
     */
    name: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

type SourceSearch = {
    sourceIDs: Array<string>;
};

type SourceUpdateResponse = {
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    /**
     * Descriptive name of the source.
     */
    name: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt: string;
};

type SourceWatchResponse = {
    /**
     * Universally unique identifier (UUID) of a task run.
     */
    runID?: string;
    /**
     * depending on the source type, the validation returns sampling data of your source (JSON, CSV, BigQuery).
     */
    data?: Array<Record<string, unknown>>;
    /**
     * in case of error, observability events will be added to the response, if any.
     */
    events?: Array<Event>;
    /**
     * a message describing the outcome of a validate run.
     */
    message: string;
};

/**
 * API request body for creating a task.
 */
type TaskCreate = {
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    action: ActionType;
    /**
     * Cron expression for the task\'s schedule.
     */
    cron?: string;
    /**
     * Whether the task is enabled.
     */
    enabled?: boolean;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
    input?: TaskInput;
    /**
     * Date of the last cursor in RFC 3339 format.
     */
    cursor?: string;
};

/**
 * API response for creating a task.
 */
type TaskCreateResponse = {
    /**
     * Universally unique identifier (UUID) of a task.
     */
    taskID: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

/**
 * Trigger information for manually-triggered tasks.
 */
type OnDemandTriggerInput = {
    type: OnDemandTriggerType;
};

/**
 * Trigger input for scheduled tasks.
 */
type ScheduleTriggerInput = {
    type: ScheduleTriggerType;
    /**
     * Cron expression for the task\'s schedule.
     */
    cron: string;
};

type TaskCreateTrigger = OnDemandTriggerInput | ScheduleTriggerInput | SubscriptionTrigger | StreamingTrigger;

/**
 * API request body for creating a task using the V1 shape, please use methods and types that don\'t contain the V1 suffix.
 */
type TaskCreateV1 = {
    /**
     * Universally uniqud identifier (UUID) of a source.
     */
    sourceID: string;
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID: string;
    trigger: TaskCreateTrigger;
    action: ActionType;
    /**
     * Whether the task is enabled.
     */
    enabled?: boolean;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
    input?: TaskInput;
    /**
     * Date of the last cursor in RFC 3339 format.
     */
    cursor?: string;
};

type TaskSearch = {
    taskIDs: Array<string>;
};

/**
 * API response for updating a task.
 */
type TaskUpdateResponse = {
    /**
     * Universally unique identifier (UUID) of a task.
     */
    taskID: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt: string;
};

/**
 * API request body for creating a transformation.
 */
type TransformationCreate = {
    /**
     * The source code of the transformation.
     */
    code: string;
    /**
     * The uniquely identified name of your transformation.
     */
    name: string;
    /**
     * A descriptive name for your transformation of what it does.
     */
    description?: string;
    /**
     * The authentications associated with the current transformation.
     */
    authenticationIDs?: Array<string>;
};

/**
 * API response for creating a transformation.
 */
type TransformationCreateResponse = {
    /**
     * Universally unique identifier (UUID) of a transformation.
     */
    transformationID: string;
    /**
     * Date of creation in RFC 3339 format.
     */
    createdAt: string;
};

type TransformationSearch = {
    transformationIDs: Array<string>;
};

type TransformationTry = {
    /**
     * The source code of the transformation.
     */
    code: string;
    /**
     * The record to apply the given code to.
     */
    sampleRecord: Record<string, unknown>;
    authentications?: Array<AuthenticationCreate>;
};

/**
 * The error if the transformation failed.
 */
type TransformationError = {
    /**
     * The error status code.
     */
    code?: number;
    /**
     * A descriptive message explaining the failure.
     */
    message?: string;
};

type TransformationTryResponse = {
    /**
     * The array of records returned by the transformation service.
     */
    payloads: Array<Record<string, unknown>>;
    error?: TransformationError;
};

/**
 * API response for updating a transformation.
 */
type TransformationUpdateResponse = {
    /**
     * Universally unique identifier (UUID) of a transformation.
     */
    transformationID: string;
    /**
     * Date of last update in RFC 3339 format.
     */
    updatedAt: string;
};

/**
 * Property by which to sort the list of authentications.
 */
type AuthenticationSortKeys = 'name' | 'type' | 'platform' | 'updatedAt' | 'createdAt';

/**
 * Request body for updating an authentication resource.
 */
type AuthenticationUpdate = {
    type?: AuthenticationType;
    /**
     * Descriptive name for the resource.
     */
    name?: string;
    platform?: Platform | null;
    input?: AuthInputPartial;
};

/**
 * Property by which to sort the destinations.
 */
type DestinationSortKeys = 'name' | 'type' | 'updatedAt' | 'createdAt';

/**
 * API request body for updating a destination.
 */
type DestinationUpdate = {
    type?: DestinationType;
    /**
     * Descriptive name for the resource.
     */
    name?: string;
    input?: DestinationInput;
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID?: string;
    transformationIDs?: Array<string>;
};

/**
 * Property by which to sort the list of task run events.
 */
type EventSortKeys = 'status' | 'type' | 'publishedAt';

/**
 * Ascending or descending sort order.
 */
type OrderKeys = 'asc' | 'desc';

/**
 * Authentication resource not tied to any ecommerce platform, used for filtering.
 */
type PlatformNone = 'none';

type PlatformWithNone = Platform | PlatformNone;

/**
 * Type of indexing operation.
 */
type Action = 'addObject' | 'updateObject' | 'partialUpdateObject' | 'partialUpdateObjectNoCreate' | 'deleteObject' | 'delete' | 'clear';

type PushTaskRecords = Record<string, any> & {
    /**
     * Unique record identifier.
     */
    objectID: string;
};

type PushTaskPayload = {
    action: Action;
    records: Array<PushTaskRecords>;
};

/**
 * Property by which to sort the list of task runs.
 */
type RunSortKeys = 'status' | 'updatedAt' | 'createdAt';

/**
 * Type of entity to update.
 */
type EntityType = 'product' | 'collection';

type RunSourcePayload = {
    /**
     * List of index names to include in reidexing/update.
     */
    indexToInclude?: Array<string>;
    /**
     * List of index names to exclude in reidexing/update.
     */
    indexToExclude?: Array<string>;
    /**
     * List of entityID to update.
     */
    entityIDs?: Array<string>;
    entityType?: EntityType;
};

/**
 * Property by which to sort the list of sources.
 */
type SourceSortKeys = 'name' | 'type' | 'updatedAt' | 'createdAt';

type SourceUpdateCommercetools = {
    storeKeys?: Array<string>;
    /**
     * Locales for your commercetools stores.
     */
    locales?: Array<string>;
    url?: string;
    /**
     * Whether a fallback value is stored in the Algolia record if there\'s no inventory information about the product.
     */
    fallbackIsInStockValue?: boolean;
    customFields?: CommercetoolsCustomFields;
};

type SourceUpdateDocker = {
    registry?: DockerRegistry;
    /**
     * Docker image name.
     */
    image?: string;
    /**
     * Docker image version.
     */
    version?: string;
    /**
     * Configuration of the spec.
     */
    configuration: Record<string, unknown>;
};

type SourceUpdateInput = SourceUpdateCommercetools | SourceJSON | SourceCSV | SourceBigQuery | SourceGA4BigQueryExport | SourceUpdateDocker | SourceUpdateShopify;

type SourceUpdate = {
    /**
     * Descriptive name of the source.
     */
    name?: string;
    input?: SourceUpdateInput;
    /**
     * Universally unique identifier (UUID) of an authentication resource.
     */
    authenticationID?: string;
};

/**
 * Property by which to sort the list of tasks.
 */
type TaskSortKeys = 'enabled' | 'triggerType' | 'action' | 'updatedAt' | 'createdAt';

/**
 * API request body for updating a task.
 */
type TaskUpdate = {
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID?: string;
    /**
     * Cron expression for the task\'s schedule.
     */
    cron?: string;
    input?: TaskInput;
    /**
     * Whether the task is enabled.
     */
    enabled?: boolean;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
};

/**
 * Trigger for a task update.
 */
type TriggerUpdateInput = {
    /**
     * Cron expression for the task\'s schedule.
     */
    cron: string;
};

/**
 * API request body for updating a task using the V1 shape, please use methods and types that don\'t contain the V1 suffix.
 */
type TaskUpdateV1 = {
    /**
     * Universally unique identifier (UUID) of a destination resource.
     */
    destinationID?: string;
    trigger?: TriggerUpdateInput;
    input?: TaskInput;
    /**
     * Whether the task is enabled.
     */
    enabled?: boolean;
    /**
     * Maximum accepted percentage of failures for a task run to finish successfully.
     */
    failureThreshold?: number;
};

/**
 * Property by which to sort the list of transformations.
 */
type TransformationSortKeys = 'name' | 'updatedAt' | 'createdAt';

/**
 * Task trigger, describing when a task should run.  - `onDemand`.   Manually trigger the task with the `/run` endpoint.  - `schedule`.   Regularly trigger the task on a `cron` schedule.  - `subscription`.   Trigger the task after an event is received, such as, a webhook.  - `streaming`.   Run the task continuously.
 */
type TriggerType = 'onDemand' | 'schedule' | 'subscription' | 'streaming';

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
 * Properties for the `deleteAuthentication` method.
 */
type DeleteAuthenticationProps = {
    /**
     * Unique identifier of an authentication resource.
     */
    authenticationID: string;
};
/**
 * Properties for the `deleteDestination` method.
 */
type DeleteDestinationProps = {
    /**
     * Unique identifier of a destination.
     */
    destinationID: string;
};
/**
 * Properties for the `deleteSource` method.
 */
type DeleteSourceProps = {
    /**
     * Unique identifier of a source.
     */
    sourceID: string;
};
/**
 * Properties for the `deleteTask` method.
 */
type DeleteTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `deleteTaskV1` method.
 */
type DeleteTaskV1Props = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `deleteTransformation` method.
 */
type DeleteTransformationProps = {
    /**
     * Unique identifier of a transformation.
     */
    transformationID: string;
};
/**
 * Properties for the `disableTask` method.
 */
type DisableTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `disableTaskV1` method.
 */
type DisableTaskV1Props = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `enableTask` method.
 */
type EnableTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `enableTaskV1` method.
 */
type EnableTaskV1Props = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `getAuthentication` method.
 */
type GetAuthenticationProps = {
    /**
     * Unique identifier of an authentication resource.
     */
    authenticationID: string;
};
/**
 * Properties for the `getDestination` method.
 */
type GetDestinationProps = {
    /**
     * Unique identifier of a destination.
     */
    destinationID: string;
};
/**
 * Properties for the `getEvent` method.
 */
type GetEventProps = {
    /**
     * Unique identifier of a task run.
     */
    runID: string;
    /**
     * Unique identifier of an event.
     */
    eventID: string;
};
/**
 * Properties for the `getRun` method.
 */
type GetRunProps = {
    /**
     * Unique identifier of a task run.
     */
    runID: string;
};
/**
 * Properties for the `getSource` method.
 */
type GetSourceProps = {
    /**
     * Unique identifier of a source.
     */
    sourceID: string;
};
/**
 * Properties for the `getTask` method.
 */
type GetTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `getTaskV1` method.
 */
type GetTaskV1Props = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `getTransformation` method.
 */
type GetTransformationProps = {
    /**
     * Unique identifier of a transformation.
     */
    transformationID: string;
};
/**
 * Properties for the `listAuthentications` method.
 */
type ListAuthenticationsProps = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Type of authentication resource to retrieve.
     */
    type?: Array<AuthenticationType>;
    /**
     * Ecommerce platform for which to retrieve authentications.
     */
    platform?: Array<PlatformWithNone>;
    /**
     * Property by which to sort the list of authentications.
     */
    sort?: AuthenticationSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
};
/**
 * Properties for the `listDestinations` method.
 */
type ListDestinationsProps = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Destination type.
     */
    type?: Array<DestinationType>;
    /**
     * Authentication ID used by destinations.
     */
    authenticationID?: Array<string>;
    /**
     * Get the list of destinations used by a transformation.
     */
    transformationID?: string;
    /**
     * Property by which to sort the destinations.
     */
    sort?: DestinationSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
};
/**
 * Properties for the `listEvents` method.
 */
type ListEventsProps = {
    /**
     * Unique identifier of a task run.
     */
    runID: string;
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Event status for filtering the list of task runs.
     */
    status?: Array<EventStatus>;
    /**
     * Event type for filtering the list of task runs.
     */
    type?: Array<EventType>;
    /**
     * Property by which to sort the list of task run events.
     */
    sort?: EventSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
    /**
     * Date and time in RFC 3339 format for the earliest events to retrieve. By default, the current time minus three hours is used.
     */
    startDate?: string;
    /**
     * Date and time in RFC 3339 format for the latest events to retrieve. By default, the current time is used.
     */
    endDate?: string;
};
/**
 * Properties for the `listRuns` method.
 */
type ListRunsProps = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Run status for filtering the list of task runs.
     */
    status?: Array<RunStatus>;
    /**
     * Run type for filtering the list of task runs.
     */
    type?: Array<RunType>;
    /**
     * Task ID for filtering the list of task runs.
     */
    taskID?: string;
    /**
     * Property by which to sort the list of task runs.
     */
    sort?: RunSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
    /**
     * Date in RFC 3339 format for the earliest run to retrieve. By default, the current day minus seven days is used.
     */
    startDate?: string;
    /**
     * Date in RFC 3339 format for the latest run to retrieve. By default, the current day is used.
     */
    endDate?: string;
};
/**
 * Properties for the `listSources` method.
 */
type ListSourcesProps = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Source type. Some sources require authentication.
     */
    type?: Array<SourceType>;
    /**
     * Authentication IDs of the sources to retrieve. \'none\' returns sources that doesn\'t have an authentication.
     */
    authenticationID?: Array<string>;
    /**
     * Property by which to sort the list of sources.
     */
    sort?: SourceSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
};
/**
 * Properties for the `listTasks` method.
 */
type ListTasksProps = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Actions for filtering the list of tasks.
     */
    action?: Array<ActionType>;
    /**
     * Whether to filter the list of tasks by the `enabled` status.
     */
    enabled?: boolean;
    /**
     * Source IDs for filtering the list of tasks.
     */
    sourceID?: Array<string>;
    /**
     * Filters the tasks with the specified source type.
     */
    sourceType?: Array<SourceType>;
    /**
     * Destination IDs for filtering the list of tasks.
     */
    destinationID?: Array<string>;
    /**
     * Type of task trigger for filtering the list of tasks.
     */
    triggerType?: Array<TriggerType>;
    /**
     * Property by which to sort the list of tasks.
     */
    sort?: TaskSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
};
/**
 * Properties for the `listTasksV1` method.
 */
type ListTasksV1Props = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Actions for filtering the list of tasks.
     */
    action?: Array<ActionType>;
    /**
     * Whether to filter the list of tasks by the `enabled` status.
     */
    enabled?: boolean;
    /**
     * Source IDs for filtering the list of tasks.
     */
    sourceID?: Array<string>;
    /**
     * Destination IDs for filtering the list of tasks.
     */
    destinationID?: Array<string>;
    /**
     * Type of task trigger for filtering the list of tasks.
     */
    triggerType?: Array<TriggerType>;
    /**
     * Property by which to sort the list of tasks.
     */
    sort?: TaskSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
};
/**
 * Properties for the `listTransformations` method.
 */
type ListTransformationsProps = {
    /**
     * Number of items per page.
     */
    itemsPerPage?: number;
    /**
     * Page number of the paginated API response.
     */
    page?: number;
    /**
     * Property by which to sort the list of transformations.
     */
    sort?: TransformationSortKeys;
    /**
     * Sort order of the response, ascending or descending.
     */
    order?: OrderKeys;
};
/**
 * Properties for the `pushTask` method.
 */
type PushTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
    /**
     * Request body of a Search API `batch` request that will be pushed in the Connectors pipeline.
     */
    pushTaskPayload: PushTaskPayload;
    /**
     * When provided, the push operation will be synchronous and the API will wait for the ingestion to be finished before responding.
     */
    watch?: boolean;
};
/**
 * Properties for the `runSource` method.
 */
type RunSourceProps = {
    /**
     * Unique identifier of a source.
     */
    sourceID: string;
    /**
     *
     */
    runSourcePayload?: RunSourcePayload;
};
/**
 * Properties for the `runTask` method.
 */
type RunTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `runTaskV1` method.
 */
type RunTaskV1Props = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
};
/**
 * Properties for the `triggerDockerSourceDiscover` method.
 */
type TriggerDockerSourceDiscoverProps = {
    /**
     * Unique identifier of a source.
     */
    sourceID: string;
};
/**
 * Properties for the `tryTransformationBeforeUpdate` method.
 */
type TryTransformationBeforeUpdateProps = {
    /**
     * Unique identifier of a transformation.
     */
    transformationID: string;
    transformationTry: TransformationTry;
};
/**
 * Properties for the `updateAuthentication` method.
 */
type UpdateAuthenticationProps = {
    /**
     * Unique identifier of an authentication resource.
     */
    authenticationID: string;
    authenticationUpdate: AuthenticationUpdate;
};
/**
 * Properties for the `updateDestination` method.
 */
type UpdateDestinationProps = {
    /**
     * Unique identifier of a destination.
     */
    destinationID: string;
    destinationUpdate: DestinationUpdate;
};
/**
 * Properties for the `updateSource` method.
 */
type UpdateSourceProps = {
    /**
     * Unique identifier of a source.
     */
    sourceID: string;
    sourceUpdate: SourceUpdate;
};
/**
 * Properties for the `updateTask` method.
 */
type UpdateTaskProps = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
    taskUpdate: TaskUpdate;
};
/**
 * Properties for the `updateTaskV1` method.
 */
type UpdateTaskV1Props = {
    /**
     * Unique identifier of a task.
     */
    taskID: string;
    taskUpdate: TaskUpdateV1;
};
/**
 * Properties for the `updateTransformation` method.
 */
type UpdateTransformationProps = {
    /**
     * Unique identifier of a transformation.
     */
    transformationID: string;
    transformationCreate: TransformationCreate;
};
/**
 * Properties for the `validateSourceBeforeUpdate` method.
 */
type ValidateSourceBeforeUpdateProps = {
    /**
     * Unique identifier of a source.
     */
    sourceID: string;
    sourceUpdate: SourceUpdate;
};

declare const apiClientVersion = "1.17.0";
declare const REGIONS: readonly ["eu", "us"];
type Region = (typeof REGIONS)[number];
type RegionOptions = {
    region: Region;
};
/**
 * Guard: Return strongly typed specific OnDemandTrigger for a given Trigger.
 *
 * @summary Guard method that returns a strongly typed specific OnDemandTrigger for a given Trigger.
 * @param trigger - The given Task Trigger.
 */
declare function isOnDemandTrigger(trigger: TaskCreateTrigger | Trigger): trigger is OnDemandTrigger;
/**
 * Guard: Return strongly typed specific ScheduleTrigger for a given Trigger.
 *
 * @summary Guard method that returns a strongly typed specific ScheduleTrigger for a given Trigger.
 * @param trigger - The given Task Trigger.
 */
declare function isScheduleTrigger(trigger: TaskCreateTrigger | Trigger): trigger is ScheduleTrigger;
/**
 * Guard: Return strongly typed specific SubscriptionTrigger for a given Trigger.
 *
 * @summary Guard method that returns a strongly typed specific SubscriptionTrigger for a given Trigger.
 * @param trigger - The given Task Trigger.
 */
declare function isSubscriptionTrigger(trigger: TaskCreateTrigger | Trigger): trigger is SubscriptionTrigger;
declare function createIngestionClient({ appId: appIdOption, apiKey: apiKeyOption, authMode, algoliaAgents, region: regionOption, ...options }: CreateClientOptions & RegionOptions): {
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
     * Creates a new authentication resource.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param authenticationCreate -
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createAuthentication(authenticationCreate: AuthenticationCreate, requestOptions?: RequestOptions): Promise<AuthenticationCreateResponse>;
    /**
     * Creates a new destination.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param destinationCreate -
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createDestination(destinationCreate: DestinationCreate, requestOptions?: RequestOptions): Promise<DestinationCreateResponse>;
    /**
     * Creates a new source.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param sourceCreate -
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createSource(sourceCreate: SourceCreate, requestOptions?: RequestOptions): Promise<SourceCreateResponse>;
    /**
     * Creates a new task.
     * @param taskCreate - Request body for creating a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createTask(taskCreate: TaskCreate, requestOptions?: RequestOptions): Promise<TaskCreateResponse>;
    /**
     * Creates a new task using the v1 endpoint, please use `createTask` instead.
     * @param taskCreate - Request body for creating a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createTaskV1(taskCreate: TaskCreateV1, requestOptions?: RequestOptions): Promise<TaskCreateResponse>;
    /**
     * Creates a new transformation.
     * @param transformationCreate - Request body for creating a transformation.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    createTransformation(transformationCreate: TransformationCreate, requestOptions?: RequestOptions): Promise<TransformationCreateResponse>;
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
     * Deletes an authentication resource. You can\'t delete authentication resources that are used by a source or a destination.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param deleteAuthentication - The deleteAuthentication object.
     * @param deleteAuthentication.authenticationID - Unique identifier of an authentication resource.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteAuthentication({ authenticationID }: DeleteAuthenticationProps, requestOptions?: RequestOptions): Promise<DeleteResponse>;
    /**
     * Deletes a destination by its ID. You can\'t delete destinations that are referenced in tasks.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param deleteDestination - The deleteDestination object.
     * @param deleteDestination.destinationID - Unique identifier of a destination.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteDestination({ destinationID }: DeleteDestinationProps, requestOptions?: RequestOptions): Promise<DeleteResponse>;
    /**
     * Deletes a source by its ID. You can\'t delete sources that are referenced in tasks.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param deleteSource - The deleteSource object.
     * @param deleteSource.sourceID - Unique identifier of a source.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteSource({ sourceID }: DeleteSourceProps, requestOptions?: RequestOptions): Promise<DeleteResponse>;
    /**
     * Deletes a task by its ID.
     * @param deleteTask - The deleteTask object.
     * @param deleteTask.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteTask({ taskID }: DeleteTaskProps, requestOptions?: RequestOptions): Promise<DeleteResponse>;
    /**
     * Deletes a task by its ID using the v1 endpoint, please use `deleteTask` instead.
     * @param deleteTaskV1 - The deleteTaskV1 object.
     * @param deleteTaskV1.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteTaskV1({ taskID }: DeleteTaskV1Props, requestOptions?: RequestOptions): Promise<DeleteResponse>;
    /**
     * Deletes a transformation by its ID.
     * @param deleteTransformation - The deleteTransformation object.
     * @param deleteTransformation.transformationID - Unique identifier of a transformation.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    deleteTransformation({ transformationID }: DeleteTransformationProps, requestOptions?: RequestOptions): Promise<DeleteResponse>;
    /**
     * Disables a task.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param disableTask - The disableTask object.
     * @param disableTask.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    disableTask({ taskID }: DisableTaskProps, requestOptions?: RequestOptions): Promise<TaskUpdateResponse>;
    /**
     * Disables a task using the v1 endpoint, please use `disableTask` instead.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param disableTaskV1 - The disableTaskV1 object.
     * @param disableTaskV1.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    disableTaskV1({ taskID }: DisableTaskV1Props, requestOptions?: RequestOptions): Promise<TaskUpdateResponse>;
    /**
     * Enables a task.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param enableTask - The enableTask object.
     * @param enableTask.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    enableTask({ taskID }: EnableTaskProps, requestOptions?: RequestOptions): Promise<TaskUpdateResponse>;
    /**
     * Enables a task using the v1 endpoint, please use `enableTask` instead.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param enableTaskV1 - The enableTaskV1 object.
     * @param enableTaskV1.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    enableTaskV1({ taskID }: EnableTaskV1Props, requestOptions?: RequestOptions): Promise<TaskUpdateResponse>;
    /**
     * Retrieves an authentication resource by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getAuthentication - The getAuthentication object.
     * @param getAuthentication.authenticationID - Unique identifier of an authentication resource.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getAuthentication({ authenticationID }: GetAuthenticationProps, requestOptions?: RequestOptions): Promise<Authentication>;
    /**
     * Retrieves a destination by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getDestination - The getDestination object.
     * @param getDestination.destinationID - Unique identifier of a destination.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getDestination({ destinationID }: GetDestinationProps, requestOptions?: RequestOptions): Promise<Destination>;
    /**
     * Retrieves a single task run event by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getEvent - The getEvent object.
     * @param getEvent.runID - Unique identifier of a task run.
     * @param getEvent.eventID - Unique identifier of an event.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getEvent({ runID, eventID }: GetEventProps, requestOptions?: RequestOptions): Promise<Event>;
    /**
     * Retrieve a single task run by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getRun - The getRun object.
     * @param getRun.runID - Unique identifier of a task run.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getRun({ runID }: GetRunProps, requestOptions?: RequestOptions): Promise<Run>;
    /**
     * Retrieve a source by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getSource - The getSource object.
     * @param getSource.sourceID - Unique identifier of a source.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getSource({ sourceID }: GetSourceProps, requestOptions?: RequestOptions): Promise<Source>;
    /**
     * Retrieves a task by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getTask - The getTask object.
     * @param getTask.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getTask({ taskID }: GetTaskProps, requestOptions?: RequestOptions): Promise<Task>;
    /**
     * Retrieves a task by its ID using the v1 endpoint, please use `getTask` instead.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getTaskV1 - The getTaskV1 object.
     * @param getTaskV1.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getTaskV1({ taskID }: GetTaskV1Props, requestOptions?: RequestOptions): Promise<TaskV1>;
    /**
     * Retrieves a transformation by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param getTransformation - The getTransformation object.
     * @param getTransformation.transformationID - Unique identifier of a transformation.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    getTransformation({ transformationID }: GetTransformationProps, requestOptions?: RequestOptions): Promise<Transformation>;
    /**
     * Retrieves a list of all authentication resources.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listAuthentications - The listAuthentications object.
     * @param listAuthentications.itemsPerPage - Number of items per page.
     * @param listAuthentications.page - Page number of the paginated API response.
     * @param listAuthentications.type - Type of authentication resource to retrieve.
     * @param listAuthentications.platform - Ecommerce platform for which to retrieve authentications.
     * @param listAuthentications.sort - Property by which to sort the list of authentications.
     * @param listAuthentications.order - Sort order of the response, ascending or descending.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listAuthentications({ itemsPerPage, page, type, platform, sort, order }?: ListAuthenticationsProps, requestOptions?: RequestOptions | undefined): Promise<ListAuthenticationsResponse>;
    /**
     * Retrieves a list of destinations.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listDestinations - The listDestinations object.
     * @param listDestinations.itemsPerPage - Number of items per page.
     * @param listDestinations.page - Page number of the paginated API response.
     * @param listDestinations.type - Destination type.
     * @param listDestinations.authenticationID - Authentication ID used by destinations.
     * @param listDestinations.transformationID - Get the list of destinations used by a transformation.
     * @param listDestinations.sort - Property by which to sort the destinations.
     * @param listDestinations.order - Sort order of the response, ascending or descending.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listDestinations({ itemsPerPage, page, type, authenticationID, transformationID, sort, order }?: ListDestinationsProps, requestOptions?: RequestOptions | undefined): Promise<ListDestinationsResponse>;
    /**
     * Retrieves a list of events for a task run, identified by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listEvents - The listEvents object.
     * @param listEvents.runID - Unique identifier of a task run.
     * @param listEvents.itemsPerPage - Number of items per page.
     * @param listEvents.page - Page number of the paginated API response.
     * @param listEvents.status - Event status for filtering the list of task runs.
     * @param listEvents.type - Event type for filtering the list of task runs.
     * @param listEvents.sort - Property by which to sort the list of task run events.
     * @param listEvents.order - Sort order of the response, ascending or descending.
     * @param listEvents.startDate - Date and time in RFC 3339 format for the earliest events to retrieve. By default, the current time minus three hours is used.
     * @param listEvents.endDate - Date and time in RFC 3339 format for the latest events to retrieve. By default, the current time is used.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listEvents({ runID, itemsPerPage, page, status, type, sort, order, startDate, endDate }: ListEventsProps, requestOptions?: RequestOptions): Promise<ListEventsResponse>;
    /**
     * Retrieve a list of task runs.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listRuns - The listRuns object.
     * @param listRuns.itemsPerPage - Number of items per page.
     * @param listRuns.page - Page number of the paginated API response.
     * @param listRuns.status - Run status for filtering the list of task runs.
     * @param listRuns.type - Run type for filtering the list of task runs.
     * @param listRuns.taskID - Task ID for filtering the list of task runs.
     * @param listRuns.sort - Property by which to sort the list of task runs.
     * @param listRuns.order - Sort order of the response, ascending or descending.
     * @param listRuns.startDate - Date in RFC 3339 format for the earliest run to retrieve. By default, the current day minus seven days is used.
     * @param listRuns.endDate - Date in RFC 3339 format for the latest run to retrieve. By default, the current day is used.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listRuns({ itemsPerPage, page, status, type, taskID, sort, order, startDate, endDate }?: ListRunsProps, requestOptions?: RequestOptions | undefined): Promise<RunListResponse>;
    /**
     * Retrieves a list of sources.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listSources - The listSources object.
     * @param listSources.itemsPerPage - Number of items per page.
     * @param listSources.page - Page number of the paginated API response.
     * @param listSources.type - Source type. Some sources require authentication.
     * @param listSources.authenticationID - Authentication IDs of the sources to retrieve. \'none\' returns sources that doesn\'t have an authentication.
     * @param listSources.sort - Property by which to sort the list of sources.
     * @param listSources.order - Sort order of the response, ascending or descending.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listSources({ itemsPerPage, page, type, authenticationID, sort, order }?: ListSourcesProps, requestOptions?: RequestOptions | undefined): Promise<ListSourcesResponse>;
    /**
     * Retrieves a list of tasks.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listTasks - The listTasks object.
     * @param listTasks.itemsPerPage - Number of items per page.
     * @param listTasks.page - Page number of the paginated API response.
     * @param listTasks.action - Actions for filtering the list of tasks.
     * @param listTasks.enabled - Whether to filter the list of tasks by the `enabled` status.
     * @param listTasks.sourceID - Source IDs for filtering the list of tasks.
     * @param listTasks.sourceType - Filters the tasks with the specified source type.
     * @param listTasks.destinationID - Destination IDs for filtering the list of tasks.
     * @param listTasks.triggerType - Type of task trigger for filtering the list of tasks.
     * @param listTasks.sort - Property by which to sort the list of tasks.
     * @param listTasks.order - Sort order of the response, ascending or descending.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listTasks({ itemsPerPage, page, action, enabled, sourceID, sourceType, destinationID, triggerType, sort, order, }?: ListTasksProps, requestOptions?: RequestOptions | undefined): Promise<ListTasksResponse>;
    /**
     * Retrieves a list of tasks using the v1 endpoint, please use `getTasks` instead.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listTasksV1 - The listTasksV1 object.
     * @param listTasksV1.itemsPerPage - Number of items per page.
     * @param listTasksV1.page - Page number of the paginated API response.
     * @param listTasksV1.action - Actions for filtering the list of tasks.
     * @param listTasksV1.enabled - Whether to filter the list of tasks by the `enabled` status.
     * @param listTasksV1.sourceID - Source IDs for filtering the list of tasks.
     * @param listTasksV1.destinationID - Destination IDs for filtering the list of tasks.
     * @param listTasksV1.triggerType - Type of task trigger for filtering the list of tasks.
     * @param listTasksV1.sort - Property by which to sort the list of tasks.
     * @param listTasksV1.order - Sort order of the response, ascending or descending.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listTasksV1({ itemsPerPage, page, action, enabled, sourceID, destinationID, triggerType, sort, order }?: ListTasksV1Props, requestOptions?: RequestOptions | undefined): Promise<ListTasksResponseV1>;
    /**
     * Retrieves a list of transformations.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param listTransformations - The listTransformations object.
     * @param listTransformations.itemsPerPage - Number of items per page.
     * @param listTransformations.page - Page number of the paginated API response.
     * @param listTransformations.sort - Property by which to sort the list of transformations.
     * @param listTransformations.order - Sort order of the response, ascending or descending.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    listTransformations({ itemsPerPage, page, sort, order }?: ListTransformationsProps, requestOptions?: RequestOptions | undefined): Promise<ListTransformationsResponse>;
    /**
     * Push a `batch` request payload through the Pipeline. You can check the status of task pushes with the observability endpoints.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param pushTask - The pushTask object.
     * @param pushTask.taskID - Unique identifier of a task.
     * @param pushTask.pushTaskPayload - Request body of a Search API `batch` request that will be pushed in the Connectors pipeline.
     * @param pushTask.watch - When provided, the push operation will be synchronous and the API will wait for the ingestion to be finished before responding.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    pushTask({ taskID, pushTaskPayload, watch }: PushTaskProps, requestOptions?: RequestOptions): Promise<RunResponse>;
    /**
     * Runs all tasks linked to a source, only available for Shopify sources. It will create 1 run per task.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param runSource - The runSource object.
     * @param runSource.sourceID - Unique identifier of a source.
     * @param runSource.runSourcePayload -
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    runSource({ sourceID, runSourcePayload }: RunSourceProps, requestOptions?: RequestOptions): Promise<RunSourceResponse>;
    /**
     * Runs a task. You can check the status of task runs with the observability endpoints.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param runTask - The runTask object.
     * @param runTask.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    runTask({ taskID }: RunTaskProps, requestOptions?: RequestOptions): Promise<RunResponse>;
    /**
     * Runs a task using the v1 endpoint, please use `runTask` instead. You can check the status of task runs with the observability endpoints.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param runTaskV1 - The runTaskV1 object.
     * @param runTaskV1.taskID - Unique identifier of a task.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    runTaskV1({ taskID }: RunTaskV1Props, requestOptions?: RequestOptions): Promise<RunResponse>;
    /**
     * Searches for authentication resources.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param authenticationSearch - The authenticationSearch object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    searchAuthentications(authenticationSearch: AuthenticationSearch, requestOptions?: RequestOptions): Promise<Array<Authentication>>;
    /**
     * Searches for destinations.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param destinationSearch - The destinationSearch object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    searchDestinations(destinationSearch: DestinationSearch, requestOptions?: RequestOptions): Promise<Array<Destination>>;
    /**
     * Searches for sources.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param sourceSearch - The sourceSearch object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    searchSources(sourceSearch: SourceSearch, requestOptions?: RequestOptions): Promise<Array<Source>>;
    /**
     * Searches for tasks.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param taskSearch - The taskSearch object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    searchTasks(taskSearch: TaskSearch, requestOptions?: RequestOptions): Promise<Array<Task>>;
    /**
     * Searches for tasks using the v1 endpoint, please use `searchTasks` instead.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param taskSearch - The taskSearch object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    searchTasksV1(taskSearch: TaskSearch, requestOptions?: RequestOptions): Promise<Array<TaskV1>>;
    /**
     * Searches for transformations.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param transformationSearch - The transformationSearch object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    searchTransformations(transformationSearch: TransformationSearch, requestOptions?: RequestOptions): Promise<Array<Transformation>>;
    /**
     * Triggers a stream-listing request for a source. Triggering stream-listing requests only works with sources with `type: docker` and `imageType: singer`.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param triggerDockerSourceDiscover - The triggerDockerSourceDiscover object.
     * @param triggerDockerSourceDiscover.sourceID - Unique identifier of a source.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    triggerDockerSourceDiscover({ sourceID }: TriggerDockerSourceDiscoverProps, requestOptions?: RequestOptions): Promise<SourceWatchResponse>;
    /**
     * Try a transformation before creating it.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param transformationTry - The transformationTry object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    tryTransformation(transformationTry: TransformationTry, requestOptions?: RequestOptions): Promise<TransformationTryResponse>;
    /**
     * Try a transformation before updating it.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param tryTransformationBeforeUpdate - The tryTransformationBeforeUpdate object.
     * @param tryTransformationBeforeUpdate.transformationID - Unique identifier of a transformation.
     * @param tryTransformationBeforeUpdate.transformationTry - The transformationTry object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    tryTransformationBeforeUpdate({ transformationID, transformationTry }: TryTransformationBeforeUpdateProps, requestOptions?: RequestOptions): Promise<TransformationTryResponse>;
    /**
     * Updates an authentication resource.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param updateAuthentication - The updateAuthentication object.
     * @param updateAuthentication.authenticationID - Unique identifier of an authentication resource.
     * @param updateAuthentication.authenticationUpdate - The authenticationUpdate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateAuthentication({ authenticationID, authenticationUpdate }: UpdateAuthenticationProps, requestOptions?: RequestOptions): Promise<AuthenticationUpdateResponse>;
    /**
     * Updates the destination by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param updateDestination - The updateDestination object.
     * @param updateDestination.destinationID - Unique identifier of a destination.
     * @param updateDestination.destinationUpdate - The destinationUpdate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateDestination({ destinationID, destinationUpdate }: UpdateDestinationProps, requestOptions?: RequestOptions): Promise<DestinationUpdateResponse>;
    /**
     * Updates a source by its ID.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param updateSource - The updateSource object.
     * @param updateSource.sourceID - Unique identifier of a source.
     * @param updateSource.sourceUpdate - The sourceUpdate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateSource({ sourceID, sourceUpdate }: UpdateSourceProps, requestOptions?: RequestOptions): Promise<SourceUpdateResponse>;
    /**
     * Updates a task by its ID.
     * @param updateTask - The updateTask object.
     * @param updateTask.taskID - Unique identifier of a task.
     * @param updateTask.taskUpdate - The taskUpdate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateTask({ taskID, taskUpdate }: UpdateTaskProps, requestOptions?: RequestOptions): Promise<TaskUpdateResponse>;
    /**
     * Updates a task by its ID using the v1 endpoint, please use `updateTask` instead.
     * @param updateTaskV1 - The updateTaskV1 object.
     * @param updateTaskV1.taskID - Unique identifier of a task.
     * @param updateTaskV1.taskUpdate - The taskUpdate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateTaskV1({ taskID, taskUpdate }: UpdateTaskV1Props, requestOptions?: RequestOptions): Promise<TaskUpdateResponse>;
    /**
     * Updates a transformation by its ID.
     * @param updateTransformation - The updateTransformation object.
     * @param updateTransformation.transformationID - Unique identifier of a transformation.
     * @param updateTransformation.transformationCreate - The transformationCreate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    updateTransformation({ transformationID, transformationCreate }: UpdateTransformationProps, requestOptions?: RequestOptions): Promise<TransformationUpdateResponse>;
    /**
     * Validates a source payload to ensure it can be created and that the data source can be reached by Algolia.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param sourceCreate -
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    validateSource(sourceCreate: SourceCreate, requestOptions?: RequestOptions | undefined): Promise<SourceWatchResponse>;
    /**
     * Validates an update of a source payload to ensure it can be created and that the data source can be reached by Algolia.
     *
     * Required API Key ACLs:
     *  - addObject
     *  - deleteIndex
     *  - editSettings
     * @param validateSourceBeforeUpdate - The validateSourceBeforeUpdate object.
     * @param validateSourceBeforeUpdate.sourceID - Unique identifier of a source.
     * @param validateSourceBeforeUpdate.sourceUpdate - The sourceUpdate object.
     * @param requestOptions - The requestOptions to send along with the query, they will be merged with the transporter requestOptions.
     */
    validateSourceBeforeUpdate({ sourceID, sourceUpdate }: ValidateSourceBeforeUpdateProps, requestOptions?: RequestOptions): Promise<SourceWatchResponse>;
};

/**
 * Error.
 */
type ErrorBase = Record<string, any> & {
    message?: string;
};

declare function ingestionClient(appId: string, apiKey: string, region: Region, options?: ClientOptions): IngestionClient;
type IngestionClient = ReturnType<typeof createIngestionClient>;

export { type Action, type ActionType, type AuthAPIKey, type AuthAPIKeyPartial, type AuthAlgolia, type AuthAlgoliaInsights, type AuthAlgoliaInsightsPartial, type AuthAlgoliaPartial, type AuthBasic, type AuthBasicPartial, type AuthGoogleServiceAccount, type AuthGoogleServiceAccountPartial, type AuthInput, type AuthInputPartial, type AuthOAuth, type AuthOAuthPartial, type Authentication, type AuthenticationCreate, type AuthenticationCreateResponse, type AuthenticationSearch, type AuthenticationSortKeys, type AuthenticationType, type AuthenticationUpdate, type AuthenticationUpdateResponse, type BigCommerceChannel, type BigCommerceMetafield, type BigQueryDataType, type CommercetoolsCustomFields, type CustomDeleteProps, type CustomGetProps, type CustomPostProps, type CustomPutProps, type DeleteAuthenticationProps, type DeleteDestinationProps, type DeleteResponse, type DeleteSourceProps, type DeleteTaskProps, type DeleteTaskV1Props, type DeleteTransformationProps, type Destination, type DestinationCreate, type DestinationCreateResponse, type DestinationIndexName, type DestinationInput, type DestinationSearch, type DestinationSortKeys, type DestinationType, type DestinationUpdate, type DestinationUpdateResponse, type DisableTaskProps, type DisableTaskV1Props, type DockerImageType, type DockerRegistry, type DockerStreams, type DockerStreamsInput, type DockerStreamsSyncMode, type EnableTaskProps, type EnableTaskV1Props, type EntityType, type ErrorBase, type Event, type EventSortKeys, type EventStatus, type EventType, type GetAuthenticationProps, type GetDestinationProps, type GetEventProps, type GetRunProps, type GetSourceProps, type GetTaskProps, type GetTaskV1Props, type GetTransformationProps, type IngestionClient, type ListAuthenticationsProps, type ListAuthenticationsResponse, type ListDestinationsProps, type ListDestinationsResponse, type ListEventsProps, type ListEventsResponse, type ListRunsProps, type ListSourcesProps, type ListSourcesResponse, type ListTasksProps, type ListTasksResponse, type ListTasksResponseV1, type ListTasksV1Props, type ListTransformationsProps, type ListTransformationsResponse, type MappingFieldDirective, type MappingFormatSchema, type MappingInput, type MappingKitAction, type MappingTypeCSV, type MethodType, type OnDemandTrigger, type OnDemandTriggerInput, type OnDemandTriggerType, type OrderKeys, type Pagination, type Platform, type PlatformNone, type PlatformWithNone, type PushTaskPayload, type PushTaskProps, type PushTaskRecords, type RecordType, type Region, type RegionOptions, type Run, type RunListResponse, type RunOutcome, type RunProgress, type RunReasonCode, type RunResponse, type RunSortKeys, type RunSourcePayload, type RunSourceProps, type RunSourceResponse, type RunStatus, type RunTaskProps, type RunTaskV1Props, type RunType, type ScheduleTrigger, type ScheduleTriggerInput, type ScheduleTriggerType, type ShopifyInput, type ShopifyMarket, type ShopifyMetafield, type Source, type SourceBigCommerce, type SourceBigQuery, type SourceCSV, type SourceCommercetools, type SourceCreate, type SourceCreateResponse, type SourceDocker, type SourceGA4BigQueryExport, type SourceInput, type SourceJSON, type SourceSearch, type SourceShopify, type SourceShopifyBase, type SourceSortKeys, type SourceType, type SourceUpdate, type SourceUpdateCommercetools, type SourceUpdateDocker, type SourceUpdateInput, type SourceUpdateResponse, type SourceUpdateShopify, type SourceWatchResponse, type StreamingInput, type StreamingTrigger, type StreamingTriggerType, type SubscriptionTrigger, type SubscriptionTriggerType, type Task, type TaskCreate, type TaskCreateResponse, type TaskCreateTrigger, type TaskCreateV1, type TaskInput, type TaskSearch, type TaskSortKeys, type TaskUpdate, type TaskUpdateResponse, type TaskUpdateV1, type TaskV1, type Transformation, type TransformationCreate, type TransformationCreateResponse, type TransformationError, type TransformationSearch, type TransformationSortKeys, type TransformationTry, type TransformationTryResponse, type TransformationUpdateResponse, type Trigger, type TriggerDockerSourceDiscoverProps, type TriggerType, type TriggerUpdateInput, type TryTransformationBeforeUpdateProps, type UpdateAuthenticationProps, type UpdateDestinationProps, type UpdateSourceProps, type UpdateTaskProps, type UpdateTaskV1Props, type UpdateTransformationProps, type ValidateSourceBeforeUpdateProps, type Window, apiClientVersion, ingestionClient, isOnDemandTrigger, isScheduleTrigger, isSubscriptionTrigger };
