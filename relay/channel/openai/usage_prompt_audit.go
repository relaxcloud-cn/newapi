package openai

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

func recordUsageLogRawResponseBody(info *relaycommon.RelayInfo, body []byte) {
	if info == nil || len(body) == 0 {
		return
	}
	info.UsageLogRawResponseBody, info.UsageLogRawResponseBodyTruncated = service.CaptureUsagePromptAuditBytes(body)
}

func recordUsageLogSyntheticChatStreamResponse(info *relaycommon.RelayInfo, text string, lastEvent string) {
	if info == nil || (text == "" && lastEvent == "") {
		return
	}
	payload := map[string]interface{}{
		"object": "newapi.chat_stream_audit",
		"choices": []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": text,
				},
			},
		},
	}
	if lastEvent != "" {
		payload["last_stream_event"] = lastEvent
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return
	}
	recordUsageLogRawResponseBody(info, body)
}

func recordUsageLogSyntheticResponsesStreamResponse(info *relaycommon.RelayInfo, text string, lastEvent string) {
	if info == nil || (text == "" && lastEvent == "") {
		return
	}
	payload := map[string]interface{}{
		"object": "newapi.responses_stream_audit",
		"output": []map[string]interface{}{
			{
				"type": "message",
				"role": "assistant",
				"content": []map[string]interface{}{
					{
						"type": "output_text",
						"text": text,
					},
				},
			},
		},
	}
	if lastEvent != "" {
		payload["last_stream_event"] = lastEvent
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return
	}
	recordUsageLogRawResponseBody(info, body)
}
