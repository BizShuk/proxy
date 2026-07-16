package transform

import "github.com/bizshuk/proxy/protocol"

// NewDefaultRegistry assembles every supported source/target protocol pair.
func NewDefaultRegistry() (*Registry, error) {
	return NewRegistry(
		AnthropicIdentity(),
		newPair(protocol.FORMAT_ANTHROPIC_MESSAGES, protocol.FORMAT_OPENAI_CHAT, AnthropicToChatRequest, ChatToAnthropicResponse, NewChatToAnthropicStream),
		newPair(protocol.FORMAT_ANTHROPIC_MESSAGES, protocol.FORMAT_OPENAI_RESPONSES, AnthropicToResponsesRequest, ResponsesToAnthropicResponse, NewResponsesToAnthropicStream),
		newPair(protocol.FORMAT_OPENAI_CHAT, protocol.FORMAT_ANTHROPIC_MESSAGES, ChatToAnthropicRequest, AnthropicToChatResponse, NewAnthropicToChatStream),
		ChatIdentity(),
		newPair(protocol.FORMAT_OPENAI_CHAT, protocol.FORMAT_OPENAI_RESPONSES, ChatToResponsesRequest, ResponsesToChatResponse, NewResponsesToChatStream),
		newPair(protocol.FORMAT_OPENAI_RESPONSES, protocol.FORMAT_ANTHROPIC_MESSAGES, ResponsesToAnthropicRequest, AnthropicToResponsesResponse, NewAnthropicToResponsesStream),
		newPair(protocol.FORMAT_OPENAI_RESPONSES, protocol.FORMAT_OPENAI_CHAT, ResponsesToChatRequest, ChatToResponsesResponse, NewChatToResponsesStream),
		ResponsesIdentity(),
	)
}
