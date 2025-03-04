package translator

func NewChatCompletionOpenAIToAzureOpenAITranslator() OpenAIChatCompletionTranslator {
	return &openAIToAzureOpenAITranslatorV1ChatCompletion{}
}

type openAIToAzureOpenAITranslatorV1ChatCompletion struct {
	stream       bool
	bufferedBody []byte
}
