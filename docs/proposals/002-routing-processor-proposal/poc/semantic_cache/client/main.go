package main

import (
	"context"
	"fmt"
	"log"
	"os"

	pb "github.com/envoyproxy/ai-gateway/docs/proposals/002-routing-processor-proposal/poc/semantic_cache/client/routing_processor"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
)

type Config struct {
	SemanticCache struct {
		Address             string  `yaml:"address"`
		SimilarityThreshold float32 `yaml:"similarity_threshold"`
	} `yaml:"semantic_cache"`
	OpenAI struct {
		APIKey  string `yaml:"api_key"`
		BaseURL string `yaml:"base_url"`
		Model   string `yaml:"model"`
	} `yaml:"openai"`
}

func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	return &config, nil
}

type SemanticCacheClient struct {
	cacheClient   pb.SemanticCacheServiceClient
	routingClient pb.RoutingProcessorClient
	openAIClient  *openai.Client
	threshold     float32
	model         string
	capabilities  *pb.CapabilitiesResponse
}

func NewSemanticCacheClient(config *Config) (*SemanticCacheClient, error) {
	// Connect to the semantic cache service
	conn, err := grpc.Dial(config.SemanticCache.Address, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cache service: %v", err)
	}

	// Create OpenAI client with custom configuration
	client := openai.NewClient(
		option.WithAPIKey(config.OpenAI.APIKey),
		option.WithBaseURL(config.OpenAI.BaseURL),
	)

	c := &SemanticCacheClient{
		cacheClient:   pb.NewSemanticCacheServiceClient(conn),
		routingClient: pb.NewRoutingProcessorClient(conn),
		openAIClient:  client,
		threshold:     config.SemanticCache.SimilarityThreshold,
		model:         config.OpenAI.Model,
	}

	// Get server capabilities
	ctx := context.Background()
	capabilities, err := c.routingClient.GetCapabilities(ctx, &pb.CapabilitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get server capabilities: %v", err)
	}
	c.capabilities = capabilities
	log.Printf("Server capabilities: %v", capabilities)

	return c, nil
}

func (c *SemanticCacheClient) ProcessChat(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (*openai.ChatCompletionMessage, error) {
	// Skip cache operations if semantic cache is not supported
	if !c.capabilities.StatelessSemanticCacheSupported {
		return c.callOpenAI(ctx, messages)
	}

	// Convert OpenAI messages to proto messages
	protoMessages := make([]*pb.Message, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case openai.ChatCompletionUserMessageParam:
			protoMessages[i] = &pb.Message{
				Role:    string(m.Role.Value),
				Content: m.Content.Value[0].(openai.ChatCompletionContentPartTextParam).Text.Value,
			}
		case openai.ChatCompletionAssistantMessageParam:
			protoMessages[i] = &pb.Message{
				Role:    string(m.Role.Value),
				Content: m.Content.Value[0].(openai.ChatCompletionContentPartTextParam).Text.Value,
			}
		}
	}

	// Try to get response from cache first
	cacheResp, err := c.cacheClient.SearchCache(ctx, &pb.SearchRequest{
		Messages:            protoMessages,
		SimilarityThreshold: c.threshold,
		Model:               c.model,
	})
	if err != nil {
		log.Printf("Cache search error: %v", err)
	} else if cacheResp.Found {
		log.Printf("Cache hit with similarity score: %f", cacheResp.SimilarityScore)
		// Return the cached response
		return &openai.ChatCompletionMessage{
			Role:    openai.ChatCompletionMessageRoleAssistant,
			Content: cacheResp.ResponseMessages[0].Content,
		}, nil
	}

	log.Println("Cache miss, calling OpenAI")

	// Call OpenAI with the new library
	resp, err := c.openAIClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(c.model),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %v", err)
	}

	// Store the response in cache
	responseMsg := resp.Choices[0].Message
	protoResponseMessages := []*pb.Message{{
		Role:    string(responseMsg.Role),
		Content: responseMsg.Content,
	}}

	_, err = c.cacheClient.StoreChat(ctx, &pb.StoreChatRequest{
		RequestMessages:  protoMessages,
		ResponseMessages: protoResponseMessages,
		Model:            c.model,
		Usage: &pb.Usage{
			PromptTokens:     int32(resp.Usage.PromptTokens),
			CompletionTokens: int32(resp.Usage.CompletionTokens),
			TotalTokens:      int32(resp.Usage.TotalTokens),
		},
	})
	if err != nil {
		log.Printf("Failed to store in cache: %v", err)
	}

	return &responseMsg, nil
}

func (c *SemanticCacheClient) ProcessChatStreaming(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (*openai.ChatCompletionMessage, error) {
	// Skip cache operations if stateful semantic cache is not supported
	if !c.capabilities.StatefulSemanticCacheSupported {
		return c.callOpenAI(ctx, messages)
	}

	// Convert OpenAI messages to proto messages
	protoMessages := make([]*pb.Message, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case openai.ChatCompletionUserMessageParam:
			protoMessages[i] = &pb.Message{
				Role:    string(m.Role.Value),
				Content: m.Content.Value[0].(openai.ChatCompletionContentPartTextParam).Text.Value,
			}
		case openai.ChatCompletionAssistantMessageParam:
			protoMessages[i] = &pb.Message{
				Role:    string(m.Role.Value),
				Content: m.Content.Value[0].(openai.ChatCompletionContentPartTextParam).Text.Value,
			}
		}
	}

	// Initiate cache search and get request ID
	initSearchResp, err := c.cacheClient.InitiateCacheSearch(ctx, &pb.InitiateCacheSearchRequest{
		Messages:            protoMessages,
		SimilarityThreshold: c.threshold,
		Model:               c.model,
	})
	if err != nil {
		log.Printf("Failed to initiate cache search: %v", err)
	} else {
		// Complete the cache search using the request ID
		cacheResp, err := c.cacheClient.CompleteCacheSearch(ctx, &pb.CompleteCacheSearchRequest{
			RequestId: initSearchResp.RequestId,
		})
		if err != nil {
			log.Printf("Cache search completion error: %v", err)
		} else if cacheResp.Found {
			log.Printf("Cache hit with similarity score: %f", cacheResp.SimilarityScore)
			return &openai.ChatCompletionMessage{
				Role:    openai.ChatCompletionMessageRoleAssistant,
				Content: cacheResp.ResponseMessages[0].Content,
			}, nil
		}
	}

	log.Println("Cache miss, calling OpenAI")

	// Call OpenAI
	resp, err := c.openAIClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(c.model),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %v", err)
	}

	// Initiate cache store
	initStoreResp, err := c.cacheClient.InitiateCacheStore(ctx, &pb.InitiateCacheStoreRequest{
		RequestMessages: protoMessages,
		Model:           c.model,
	})
	if err != nil {
		log.Printf("Failed to initiate cache store: %v", err)
	} else {
		// Complete the cache store with the response
		responseMsg := resp.Choices[0].Message
		protoResponseMessages := []*pb.Message{{
			Role:    string(responseMsg.Role),
			Content: responseMsg.Content,
		}}

		_, err = c.cacheClient.CompleteCacheStore(ctx, &pb.CompleteCacheStoreRequest{
			RequestId:        initStoreResp.RequestId,
			ResponseMessages: protoResponseMessages,
			Usage: &pb.Usage{
				PromptTokens:     int32(resp.Usage.PromptTokens),
				CompletionTokens: int32(resp.Usage.CompletionTokens),
				TotalTokens:      int32(resp.Usage.TotalTokens),
			},
		})
		if err != nil {
			log.Printf("Failed to complete cache store: %v", err)
		}
	}

	return &resp.Choices[0].Message, nil
}

// Helper method to encapsulate OpenAI API call
func (c *SemanticCacheClient) callOpenAI(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (*openai.ChatCompletionMessage, error) {
	resp, err := c.openAIClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(c.model),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %v", err)
	}
	return &resp.Choices[0].Message, nil
}

func main() {
	// Load configuration
	config, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create client with config
	client, err := NewSemanticCacheClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.ChatCompletionUserMessageParam{
			Role: openai.F(openai.ChatCompletionUserMessageParamRoleUser),
			Content: openai.F([]openai.ChatCompletionContentPartUnionParam{
				openai.ChatCompletionContentPartTextParam{
					Text: openai.F("What is the capital of France?"),
					Type: openai.F(openai.ChatCompletionContentPartTextTypeText),
				},
			}),
		},
	}

	// First attempt - should miss cache and call OpenAI
	log.Println("First attempt - expecting cache miss")
	resp1, err := client.ProcessChat(ctx, messages)
	if err != nil {
		log.Fatalf("First attempt failed: %v", err)
	}
	log.Printf("Response: %s\n", resp1.Content)

	// Second attempt with same question - should hit cache
	log.Println("\nSecond attempt - expecting cache hit")
	resp2, err := client.ProcessChat(ctx, messages)
	if err != nil {
		log.Fatalf("Second attempt failed: %v", err)
	}
	log.Printf("Response: %s\n", resp2.Content)

	// Try a similar question - should hit cache if similarity is high enough
	similarMessages := []openai.ChatCompletionMessageParamUnion{
		openai.ChatCompletionUserMessageParam{
			Role: openai.F(openai.ChatCompletionUserMessageParamRoleUser),
			Content: openai.F([]openai.ChatCompletionContentPartUnionParam{
				openai.ChatCompletionContentPartTextParam{
					Text: openai.F("Could you tell me what the capital city of France is?"),
					Type: openai.F(openai.ChatCompletionContentPartTextTypeText),
				},
			}),
		},
	}

	log.Println("\nThird attempt with similar question - may hit cache depending on similarity")
	resp3, err := client.ProcessChat(ctx, similarMessages)
	if err != nil {
		log.Fatalf("Third attempt failed: %v", err)
	}
	log.Printf("Response: %s\n", resp3.Content)

	// Test the streaming version
	log.Println("\nTesting streaming version with a new question")
	streamingMessages := []openai.ChatCompletionMessageParamUnion{
		openai.ChatCompletionUserMessageParam{
			Role: openai.F(openai.ChatCompletionUserMessageParamRoleUser),
			Content: openai.F([]openai.ChatCompletionContentPartUnionParam{
				openai.ChatCompletionContentPartTextParam{
					Text: openai.F("What is the population of Paris?"),
					Type: openai.F(openai.ChatCompletionContentPartTextTypeText),
				},
			}),
		},
	}

	resp4, err := client.ProcessChatStreaming(ctx, streamingMessages)
	if err != nil {
		log.Fatalf("Streaming attempt failed: %v", err)
	}
	log.Printf("Streaming Response: %s\n", resp4.Content)

	// Test streaming version with the same question (should hit cache)
	log.Println("\nTesting streaming version with same question - expecting cache hit")
	resp5, err := client.ProcessChatStreaming(ctx, streamingMessages)
	if err != nil {
		log.Fatalf("Second streaming attempt failed: %v", err)
	}
	log.Printf("Streaming Response (cached): %s\n", resp5.Content)
}
