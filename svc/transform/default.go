package transform

import "github.com/bizshuk/proxy/model"

// NewDefaultRegistry assembles every supported source/target protocol pair.
func NewDefaultRegistry() (*Registry, error) {
	return NewRegistry(
		AnthropicIdentity(),
		newPair(model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_CHAT, AnthropicToChatRequest, ChatToAnthropicResponse, NewChatToAnthropicStream),
		newPair(model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES, AnthropicToResponsesRequest, ResponsesToAnthropicResponse, NewResponsesToAnthropicStream),
		newPair(model.FORMAT_OPENAI_CHAT, model.FORMAT_ANTHROPIC_MESSAGES, ChatToAnthropicRequest, AnthropicToChatResponse, NewAnthropicToChatStream),
		ChatIdentity(),
		newPair(model.FORMAT_OPENAI_CHAT, model.FORMAT_OPENAI_RESPONSES, ChatToResponsesRequest, ResponsesToChatResponse, NewResponsesToChatStream),
		newPair(model.FORMAT_OPENAI_RESPONSES, model.FORMAT_ANTHROPIC_MESSAGES, ResponsesToAnthropicRequest, AnthropicToResponsesResponse, NewAnthropicToResponsesStream),
		newPair(model.FORMAT_OPENAI_RESPONSES, model.FORMAT_OPENAI_CHAT, ResponsesToChatRequest, ChatToResponsesResponse, NewChatToResponsesStream),
		ResponsesIdentity(),
	)
}
