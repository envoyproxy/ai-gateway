package main

import (
	"context"
	"fmt"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	client := openai.NewClient(option.WithBaseURL("http://localhost:1062/v1/"))
	
	
	for i := 0; i < 10; i++ {
		ctx := context.Background()
		chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			}),
			Model: openai.F("gpt-4o-mini"),
		})

		if err != nil {
			fmt.Printf("Attempt %d - Error: %v\n", i+1, err)
			time.Sleep(time.Second)
			continue
		}

		for _, choice := range chatCompletion.Choices {
			fmt.Printf("Choice: %s\n", choice.Message.Content)
		}
		return
	}
}